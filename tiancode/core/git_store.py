"""Git-native snapshot store and commit manager with checkpoint support."""

import os
import subprocess
import tempfile
from pathlib import Path
from typing import Any, Dict, List, Optional, Set, Tuple

from tiancode.core.diff import credential_file_diff
from tiancode.core.path_safety import (
    WorkspacePathError,
    is_credential_path,
    is_shared_hardlink,
    read_workspace_text,
    restore_snapshot_entry,
    workspace_directory_entry,
)
from tiancode.domain.interfaces import SnapshotStore
from tiancode.domain.models import (
    FileDiff,
    FileSnapshot,
    export_snapshot,
    normalize_link_target,
    snapshot_from_dict,
)


def _strip_diff_side(token: str, prefix: str) -> str:
    """Drop one leading a/ or b/ prefix. Later path segments stay intact."""
    text = token.strip()
    if text.startswith(prefix):
        return text[len(prefix):]
    return text


def _header_paths(first_line: str) -> Tuple[str, str]:
    """Return the a/ and b/ paths from one `diff --git` header."""
    tokens = first_line.split()
    if len(tokens) < 2:
        token = tokens[-1] if tokens else ""
        return "", _strip_diff_side(token, "b/")
    return _strip_diff_side(tokens[0], "a/"), _strip_diff_side(tokens[-1], "b/")


def _cached_rename_pairs(payload: str) -> List[Tuple[str, str]]:
    """Parse `git diff --cached -M --name-status -z` rename records."""
    parts = payload.split("\0")
    pairs: List[Tuple[str, str]] = []
    index = 0
    while index < len(parts):
        status = parts[index]
        if not status:
            index += 1
            continue
        if status[0] in {"R", "C"}:
            if index + 2 >= len(parts):
                break
            pairs.append((parts[index + 1], parts[index + 2]))
            index += 3
            continue
        index += 2
    return pairs


class GitSnapshotStore(SnapshotStore):
    """Git-backed snapshot store supporting checkpointing, rollback, diffs, and auto-commits."""

    def __init__(self, work_dir: Optional[Path] = None) -> None:
        self.work_dir = (work_dir or Path(".")).resolve()
        self._touched_files: set[str] = set()
        self._memory_snapshots: Dict[str, FileSnapshot] = {}
        self._checkpoints: Dict[str, Dict[str, FileSnapshot]] = {}

    def _run_git(self, args: List[str]) -> subprocess.CompletedProcess:
        """Helper to run git command inside work_dir."""
        return subprocess.run(
            ["git"] + args,
            cwd=str(self.work_dir),
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
        )

    def _normalize_path(self, filename: str) -> tuple[Path, str]:
        """Resolve absolute path and normalized relative name to work_dir."""
        p = Path(filename)
        target = p if p.is_absolute() else (self.work_dir / p)
        try:
            rel_name = os.path.relpath(target, self.work_dir)
        except ValueError:
            rel_name = str(target)
        return target, rel_name

    def is_git_repo(self) -> bool:
        """Check if work_dir is inside a valid git repository."""
        res = self._run_git(["rev-parse", "--is-inside-work-tree"])
        return res.returncode == 0 and res.stdout.strip() == "true"

    def record_snapshot(
        self,
        filename: str,
        content: str,
        is_new: bool = False,
        link_target: Optional[str] = None,
    ) -> None:
        """Record file snapshot in memory and track as touched in current session."""
        _, rel_name = self._normalize_path(filename)
        self._touched_files.add(rel_name)
        if rel_name not in self._memory_snapshots:
            recorded_link = normalize_link_target(link_target)
            self._memory_snapshots[rel_name] = FileSnapshot(
                filename=rel_name,
                original_content="" if recorded_link else content,
                is_created_new=False if recorded_link else is_new,
                link_target=recorded_link,
            )

    def create_checkpoint(self, tag: str = "") -> str:
        """Snapshot current state and return unique checkpoint ID."""
        ckpt_id = f"ckpt_{len(self._checkpoints) + 1}_{tag}" if tag else f"ckpt_{len(self._checkpoints) + 1}"
        self._checkpoints[ckpt_id] = {
            k: FileSnapshot(v.filename, v.original_content, v.is_created_new, v.link_target)
            for k, v in self._memory_snapshots.items()
        }
        return ckpt_id

    def get_snapshot(self, filename: str) -> Optional[FileSnapshot]:
        """Fetch the original content from memory snapshot or git HEAD."""
        _, rel_name = self._normalize_path(filename)
        if rel_name in self._memory_snapshots:
            return self._memory_snapshots[rel_name]

        res = self._run_git(["show", f"HEAD:{rel_name}"])
        if res.returncode == 0:
            return FileSnapshot(filename=rel_name, original_content=res.stdout, is_created_new=False)
        return None

    def get_all_snapshots(self) -> Dict[str, FileSnapshot]:
        """Return snapshots of all files with uncommitted changes."""
        snapshots = dict(self._memory_snapshots)
        if not self.is_git_repo():
            return snapshots

        res = self._run_git(["status", "--porcelain"])
        for line in res.stdout.splitlines():
            if len(line) > 3:
                _, fname = line[:2].strip(), line[3:].strip()
                if fname not in snapshots:
                    snap = self.get_snapshot(fname)
                    if snap:
                        snapshots[fname] = snap
                    else:
                        snapshots[fname] = FileSnapshot(filename=fname, original_content="", is_created_new=True)
        return snapshots

    def rollback_file(self, filename: str) -> bool:
        """Revert a single touched file to its original state."""
        _, rel_name = self._normalize_path(filename)

        try:
            entry = workspace_directory_entry(self.work_dir, rel_name)
        except WorkspacePathError:
            return False

        snap = self._memory_snapshots.pop(rel_name, None)
        self._touched_files.discard(rel_name)

        if snap:
            restore_snapshot_entry(
                entry,
                link_target=snap.link_target,
                original_content=snap.original_content,
                is_created_new=snap.is_created_new,
            )

        return snap is not None or entry.exists()

    def rollback(self, checkpoint_id: Optional[str] = None) -> List[str]:
        """Revert uncommitted modifications safely to baseline or specified checkpoint."""
        reverted: List[str] = []
        snapshots_to_restore = (
            self._checkpoints.get(checkpoint_id, self._memory_snapshots)
            if checkpoint_id
            else dict(self._memory_snapshots)
        )

        for rel_name, snap in list(snapshots_to_restore.items()):
            try:
                entry = workspace_directory_entry(self.work_dir, rel_name)
            except WorkspacePathError:
                reverted.append(f"{rel_name} (已拒绝越界回滚)")
                continue
            label = restore_snapshot_entry(
                entry,
                link_target=snap.link_target,
                original_content=snap.original_content,
                is_created_new=snap.is_created_new,
            )
            reverted.append(f"{rel_name} ({label})")

        if reverted:
            reverted.append("提示：由终端命令间接修改或新建的文件不在快照追踪中，请手动核对。")

        self._touched_files.clear()
        self._memory_snapshots.clear()
        return reverted

    def clear(self) -> None:
        """Clear touched file tracking, memory snapshots, and checkpoints."""
        self._touched_files.clear()
        self._memory_snapshots.clear()
        self._checkpoints.clear()

    def export_snapshots(self) -> List[Dict[str, Any]]:
        """Export all recorded snapshots for persistent storage."""
        return [export_snapshot(snap) for snap in self._memory_snapshots.values()]

    def import_snapshots(self, snapshots_data: List[Dict[str, Any]]) -> None:
        """Import snapshots from persistent storage to restore rollback state."""
        for item in snapshots_data or []:
            snap = snapshot_from_dict(item) if isinstance(item, dict) else None
            if snap is None:
                continue
            _, rel_name = self._normalize_path(snap.filename)
            self._touched_files.add(rel_name)
            self._memory_snapshots[rel_name] = FileSnapshot(
                filename=rel_name,
                original_content=snap.original_content,
                is_created_new=snap.is_created_new,
                link_target=snap.link_target,
            )

    def compute_diffs(self) -> List[FileDiff]:
        """Compute unified diffs for all modified and untracked files."""
        if not self.is_git_repo():
            from tiancode.core.diff import compute_text_diff

            diffs: List[FileDiff] = []
            for rel_name, snap in self._memory_snapshots.items():
                if is_credential_path(rel_name):
                    diffs.append(credential_file_diff(rel_name))
                    continue
                try:
                    current_text = read_workspace_text(workspace_directory_entry(self.work_dir, rel_name))
                except WorkspacePathError:
                    current_text = ""
                d = compute_text_diff(rel_name, snap.original_content, current_text)
                if not d.is_empty:
                    diffs.append(d)
            return diffs

        diffs: List[FileDiff] = []
        blocked = self._credential_rename_paths()
        changed = self._changed_paths()
        secret = [path for path in changed if is_credential_path(path) or path in blocked]
        safe = [path for path in changed if path not in set(secret)]
        for path in secret:
            diffs.append(credential_file_diff(path))
        if safe:
            res = self._run_git(["diff", "HEAD", "--", *safe])
            if res.returncode != 0:
                res = self._run_git(["diff", "--", *safe])
            diffs.extend(self._diffs_from_text(res.stdout, safe, blocked))

        status_res = self._run_git(["status", "--porcelain"])
        for line in status_res.stdout.splitlines():
            if line.startswith("??"):
                untracked = self._diff_for_untracked(line[3:].strip(), blocked)
                if untracked is not None:
                    diffs.append(untracked)

        return diffs

    def _changed_paths(self) -> List[str]:
        """Tracked paths changed since HEAD, using NUL records so quotes are not parsed."""
        res = self._run_git(["diff", "--name-only", "-z", "HEAD"])
        if res.returncode != 0:
            res = self._run_git(["diff", "--name-only", "-z"])
        if res.returncode != 0 or not res.stdout:
            return []
        return [part for part in res.stdout.split("\0") if part]

    def _diffs_from_text(self, raw_diff: str, paths: List[str], blocked: Set[str]) -> List[FileDiff]:
        """Turn one unified diff into file records. Names come from the NUL list when they line up."""
        if not raw_diff.strip():
            return []
        blocks = [block for block in raw_diff.split("diff --git ") if block.strip()]
        diffs: List[FileDiff] = []
        for index, block in enumerate(blocks):
            lines = block.splitlines()
            source_name, target_fname = _header_paths(lines[0])
            filename = paths[index] if index < len(paths) else (target_fname or source_name)
            if self._hides_body(source_name, target_fname, blocked) or is_credential_path(filename) or filename in blocked:
                diffs.append(credential_file_diff(filename))
                continue
            adds = sum(1 for line in lines if line.startswith("+") and not line.startswith("+++"))
            dels = sum(1 for line in lines if line.startswith("-") and not line.startswith("---"))
            diffs.append(
                FileDiff(
                    filename=filename,
                    diff_text="diff --git " + block,
                    additions=adds,
                    deletions=dels,
                    is_empty=False,
                )
            )
        return diffs

    def _hides_body(self, source_name: str, target_name: str, blocked: Set[str]) -> bool:
        """True when either rename end, or the path itself, must not be inlined."""
        names = [name for name in (source_name, target_name) if name]
        return any(is_credential_path(name) or name in blocked for name in names)

    def _credential_rename_paths(self) -> Set[str]:
        """Both ends of a rename when either name is a credential path.

        The lookup uses a temporary index, so the real index and worktree stay as they are.
        """
        if not self.is_git_repo():
            return set()
        handle = tempfile.NamedTemporaryFile(prefix="tiancode-index-", delete=False)
        handle.close()
        env = os.environ.copy()
        env["GIT_INDEX_FILE"] = handle.name
        base = ["git", "-C", str(self.work_dir)]
        try:
            read_tree = subprocess.run(
                [*base, "read-tree", "HEAD"],
                env=env,
                capture_output=True,
                text=True,
                check=False,
            )
            if read_tree.returncode != 0:
                return set()
            added = subprocess.run([*base, "add", "-A"], env=env, capture_output=True, text=True, check=False)
            if added.returncode != 0:
                return set()
            listed = subprocess.run(
                [*base, "diff", "--cached", "--find-renames", "--name-status", "-z", "HEAD"],
                env=env,
                capture_output=True,
                text=True,
                check=False,
            )
        finally:
            os.unlink(handle.name)
        blocked: Set[str] = set()
        if listed.returncode != 0:
            return blocked
        for old, new in _cached_rename_pairs(listed.stdout):
            if is_credential_path(old) or is_credential_path(new):
                blocked.add(old)
                blocked.add(new)
        return blocked

    def _diff_for_untracked(self, filename: str, blocked: Optional[Set[str]] = None) -> Optional[FileDiff]:
        """Read one untracked file. A symlink is named, and its target is not."""
        if not filename:
            return None
        if is_credential_path(filename) or filename in (blocked or set()):
            return credential_file_diff(filename)
        try:
            entry = workspace_directory_entry(self.work_dir, filename)
        except WorkspacePathError:
            return None
        if entry.is_symlink():
            return FileDiff(
                filename=filename,
                diff_text=f"+++ b/{filename} (符号链接，未读取目标)\n",
                additions=0,
                deletions=0,
                is_empty=False,
            )
        if is_shared_hardlink(entry):
            return FileDiff(
                filename=filename,
                diff_text=f"+++ b/{filename} (硬链接，未读取内容)\n",
                additions=0,
                deletions=0,
                is_empty=False,
            )
        if not entry.is_file():
            return None
        try:
            content = read_workspace_text(entry)
        except WorkspacePathError:
            return None
        return FileDiff(
            filename=filename,
            diff_text=f"+++ b/{filename} (新文件)\n" + content,
            additions=len(content.splitlines()),
            deletions=0,
            is_empty=False,
        )

    def _unstage_credential_files(self) -> None:
        """Drop credential paths, and a whole rename when either end is one.

        Resetting only the new name of a rename leaves the old path deleted in the index.
        Both ends go back to HEAD together. The worktree is not moved.
        """
        listed = self._run_git(["diff", "--cached", "--find-renames", "--name-status", "-z"])
        if listed.returncode == 0 and listed.stdout:
            for old, new in _cached_rename_pairs(listed.stdout):
                if is_credential_path(old) or is_credential_path(new):
                    self._run_git(["reset", "-q", "--", old, new])
        names = self._run_git(["diff", "--cached", "--name-only", "-z"])
        if names.returncode != 0 or not names.stdout:
            return
        for name in names.stdout.split("\0"):
            if name and is_credential_path(name):
                self._run_git(["reset", "-q", "--", name])

    def commit(self, message: str) -> Optional[str]:
        """Stage all changes and create a Git commit, returning the short commit hash."""
        if not self.is_git_repo():
            return None

        self._run_git(["add", "-A"])
        self._unstage_credential_files()
        commit_res = self._run_git(["commit", "-m", message])
        if commit_res.returncode != 0:
            return None

        hash_res = self._run_git(["rev-parse", "--short", "HEAD"])
        return hash_res.stdout.strip() if hash_res.returncode == 0 else None
