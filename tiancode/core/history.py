"""Snapshot store and file rollback manager for TianCode."""

import difflib
from pathlib import Path
from typing import Any, Dict, List, Optional

from tiancode.core.path_safety import (
    WorkspacePathError,
    read_workspace_text,
    remove_workspace_file,
    restore_workspace_text,
    workspace_directory_entry,
)
from tiancode.domain.interfaces import SnapshotStore
from tiancode.domain.models import FileDiff, FileSnapshot


class InMemorySnapshotStore(SnapshotStore):
    """In-memory implementation of snapshot store for session-level undo/diff."""

    def __init__(self, work_dir: Optional[Path] = None) -> None:
        self.work_dir = work_dir or Path(".").resolve()
        self._snapshots: Dict[str, FileSnapshot] = {}

    def record_snapshot(self, filename: str, content: str, is_new: bool = False) -> None:
        """Save initial snapshot if not already recorded."""
        norm_name = str(Path(filename))
        if norm_name not in self._snapshots:
            self._snapshots[norm_name] = FileSnapshot(
                filename=norm_name,
                original_content=content,
                is_created_new=is_new,
            )

    def get_snapshot(self, filename: str) -> Optional[FileSnapshot]:
        """Get snapshot for given filename."""
        return self._snapshots.get(str(Path(filename)))

    def get_all_snapshots(self) -> Dict[str, FileSnapshot]:
        """Return shallow copy of all snapshots."""
        return dict(self._snapshots)

    def rollback(self) -> List[str]:
        """Revert all files to snapshot state."""
        reverted: List[str] = []
        for filename, snap in self._snapshots.items():
            try:
                entry = workspace_directory_entry(self.work_dir, filename)
            except WorkspacePathError:
                reverted.append(f"{filename} (已拒绝越界回滚)")
                continue
            if snap.is_created_new:
                if remove_workspace_file(entry):
                    reverted.append(f"{filename} (已删除新建文件)")
            else:
                restore_workspace_text(entry, snap.original_content)
                reverted.append(f"{filename} (已还原原内容)")
        self._snapshots.clear()
        return reverted

    def clear(self) -> None:
        """Clear all snapshots."""
        self._snapshots.clear()

    def export_snapshots(self) -> List[Dict[str, Any]]:
        """Export all recorded snapshots for persistent storage."""
        return [
            {
                "filename": snap.filename,
                "original_content": snap.original_content,
                "is_created_new": snap.is_created_new,
            }
            for snap in self._snapshots.values()
        ]

    def import_snapshots(self, snapshots_data: List[Dict[str, Any]]) -> None:
        """Import snapshots from persistent storage to restore rollback state."""
        for item in snapshots_data or []:
            fname = item.get("filename", "")
            if fname:
                norm_name = str(Path(fname))
                self._snapshots[norm_name] = FileSnapshot(
                    filename=norm_name,
                    original_content=item.get("original_content", ""),
                    is_created_new=item.get("is_created_new", False),
                )

    def compute_diffs(self) -> List[FileDiff]:
        """Generate unified diffs between original snapshots and current disk states."""
        diffs: List[FileDiff] = []
        for filename, snap in self._snapshots.items():
            current_content = ""
            try:
                current_content = read_workspace_text(workspace_directory_entry(self.work_dir, filename))
            except WorkspacePathError:
                current_content = ""

            diff_lines = list(
                difflib.unified_diff(
                    snap.original_content.splitlines(keepends=True),
                    current_content.splitlines(keepends=True),
                    fromfile=f"a/{filename}",
                    tofile=f"b/{filename}",
                    n=2,
                )
            )

            diff_text = "".join(diff_lines)
            adds = sum(1 for line in diff_lines if line.startswith("+") and not line.startswith("+++"))
            dels = sum(1 for line in diff_lines if line.startswith("-") and not line.startswith("---"))

            diffs.append(
                FileDiff(
                    filename=filename,
                    diff_text=diff_text,
                    additions=adds,
                    deletions=dels,
                    is_empty=len(diff_lines) == 0,
                )
            )
        return diffs
