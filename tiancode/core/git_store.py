"""Git-native snapshot store and commit manager with checkpoint support."""

import os
import subprocess
from pathlib import Path
from typing import Any, Dict, List, Optional

from tiancode.core.path_safety import (
    WorkspacePathError,
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
                try:
                    current_text = read_workspace_text(workspace_directory_entry(self.work_dir, rel_name))
                except WorkspacePathError:
                    current_text = ""
                d = compute_text_diff(rel_name, snap.original_content, current_text)
                if not d.is_empty:
                    diffs.append(d)
            return diffs

        diffs: List[FileDiff] = []
        res = self._run_git(["diff", "HEAD"])
        if res.returncode != 0:
            res = self._run_git(["diff"])

        raw_diff = res.stdout
        if raw_diff.strip():
            file_blocks = raw_diff.split("diff --git ")
            for block in file_blocks:
                if not block.strip():
                    continue
                lines = block.splitlines()
                first_line = lines[0]
                target_fname = first_line.split()[-1].replace("b/", "")
                diff_text = "diff --git " + block
                adds = sum(1 for l in lines if l.startswith("+") and not l.startswith("+++"))
                dels = sum(1 for l in lines if l.startswith("-") and not l.startswith("---"))
                diffs.append(
                    FileDiff(
                        filename=target_fname,
                        diff_text=diff_text,
                        additions=adds,
                        deletions=dels,
                        is_empty=False,
                    )
                )

        status_res = self._run_git(["status", "--porcelain"])
        for line in status_res.stdout.splitlines():
            if line.startswith("??"):
                untracked = self._diff_for_untracked(line[3:].strip())
                if untracked is not None:
                    diffs.append(untracked)

        return diffs

    def _diff_for_untracked(self, filename: str) -> Optional[FileDiff]:
        """Read one untracked file. A symlink is named, and its target is not."""
        if not filename:
            return None
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

    def commit(self, message: str) -> Optional[str]:
        """Stage all changes and create a Git commit, returning the short commit hash."""
        if not self.is_git_repo():
            return None

        self._run_git(["add", "-A"])
        commit_res = self._run_git(["commit", "-m", message])
        if commit_res.returncode != 0:
            return None

        hash_res = self._run_git(["rev-parse", "--short", "HEAD"])
        return hash_res.stdout.strip() if hash_res.returncode == 0 else None
