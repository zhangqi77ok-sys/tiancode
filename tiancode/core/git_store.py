"""Git-native snapshot store and commit manager."""

import os
import subprocess
from pathlib import Path
from typing import Dict, List, Optional

from tiancode.domain.interfaces import SnapshotStore
from tiancode.domain.models import FileDiff, FileSnapshot


class GitSnapshotStore(SnapshotStore):
    """Git-backed snapshot store supporting rollback, diffs, and auto-commits."""

    def __init__(self, work_dir: Optional[Path] = None) -> None:
        self.work_dir = (work_dir or Path(".")).resolve()
        self._touched_files: set[str] = set()

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

    def is_git_repo(self) -> bool:
        """Check if work_dir is inside a valid git repository."""
        res = self._run_git(["rev-parse", "--is-inside-work-tree"])
        return res.returncode == 0 and res.stdout.strip() == "true"

    def record_snapshot(self, filename: str, content: str, is_new: bool = False) -> None:
        """Record file as touched in current session."""
        rel_name = os.path.relpath(filename, self.work_dir)
        self._touched_files.add(rel_name)

    def get_snapshot(self, filename: str) -> Optional[FileSnapshot]:
        """Fetch the original content from git HEAD."""
        rel_name = os.path.relpath(filename, self.work_dir)
        res = self._run_git(["show", f"HEAD:{rel_name}"])
        if res.returncode == 0:
            return FileSnapshot(filename=rel_name, original_content=res.stdout, is_created_new=False)
        return None

    def get_all_snapshots(self) -> Dict[str, FileSnapshot]:
        """Return snapshots of all files with uncommitted changes."""
        res = self._run_git(["status", "--porcelain"])
        snapshots: Dict[str, FileSnapshot] = {}
        for line in res.stdout.splitlines():
            if len(line) > 3:
                status, fname = line[:2].strip(), line[3:].strip()
                snap = self.get_snapshot(fname)
                if snap:
                    snapshots[fname] = snap
                else:
                    snapshots[fname] = FileSnapshot(filename=fname, original_content="", is_created_new=True)
        return snapshots

    def rollback(self) -> List[str]:
        """Revert uncommitted modifications for touched files safely."""
        if not self.is_git_repo():
            return []

        reverted: List[str] = []
        targets = set(self._touched_files)

        # Check git status for modified files
        status_res = self._run_git(["status", "--porcelain"])
        for line in status_res.stdout.splitlines():
            if len(line) > 3:
                code, fname = line[:2].strip(), line[3:].strip()
                # If targets were recorded, only rollback touched files; otherwise rollback all tracked modified files
                if targets and fname not in targets:
                    continue

                if code.startswith("??"):
                    # Only delete untracked file if it was explicitly touched/created by this session
                    if fname in targets:
                        target_path = self.work_dir / fname
                        if target_path.is_file():
                            target_path.unlink(missing_ok=True)
                            reverted.append(f"{fname} (删除新增)")
                else:
                    self._run_git(["restore", "--staged", fname])
                    self._run_git(["restore", fname])
                    reverted.append(f"{fname} (Git还原)")

        self._touched_files.clear()
        return reverted

    def clear(self) -> None:
        """Clear touched file tracking."""
        self._touched_files.clear()

    def compute_diffs(self) -> List[FileDiff]:
        """Compute unified diffs for all modified and untracked files."""
        if not self.is_git_repo():
            return []

        diffs: List[FileDiff] = []

        # 1. Diffs for tracked files (staged & unstaged)
        res = self._run_git(["diff", "HEAD"])
        if res.returncode != 0:
            # Fallback for initial commit where HEAD doesn't exist yet
            res = self._run_git(["diff"])

        raw_diff = res.stdout
        if raw_diff.strip():
            # Parse diff by file blocks
            file_blocks = raw_diff.split("diff --git ")
            for block in file_blocks:
                if not block.strip():
                    continue
                lines = block.splitlines()
                # Line 0 is usually: a/filename b/filename
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

        # 2. Check untracked files
        status_res = self._run_git(["status", "--porcelain"])
        for line in status_res.stdout.splitlines():
            if line.startswith("??"):
                untracked_fname = line[3:].strip()
                untracked_path = self.work_dir / untracked_fname
                if untracked_path.is_file():
                    content = open(untracked_path, "r", encoding="utf-8", errors="replace").read()
                    line_count = len(content.splitlines())
                    diffs.append(
                        FileDiff(
                            filename=untracked_fname,
                            diff_text=f"+++ b/{untracked_fname} (新文件)\n" + content,
                            additions=line_count,
                            deletions=0,
                            is_empty=False,
                        )
                    )

        return diffs

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
