"""Snapshot store and file rollback manager for TianCode."""

import difflib
from pathlib import Path
from typing import Any, Dict, List, Optional

from tiancode.core.diff import credential_file_diff
from tiancode.core.path_safety import (
    WorkspacePathError,
    is_credential_path,
    read_workspace_text,
    restore_snapshot_entry,
    workspace_directory_entry,
)
from tiancode.domain.interfaces import SnapshotStore
from tiancode.domain.models import FileDiff, FileSnapshot, export_snapshot, normalize_link_target, snapshot_from_dict


class InMemorySnapshotStore(SnapshotStore):
    """In-memory implementation of snapshot store for session-level undo/diff."""

    def __init__(self, work_dir: Optional[Path] = None) -> None:
        self.work_dir = work_dir or Path(".").resolve()
        self._snapshots: Dict[str, FileSnapshot] = {}

    def record_snapshot(
        self,
        filename: str,
        content: str,
        is_new: bool = False,
        link_target: Optional[str] = None,
    ) -> None:
        """Save initial snapshot if not already recorded."""
        norm_name = str(Path(filename))
        if norm_name not in self._snapshots:
            recorded_link = normalize_link_target(link_target)
            self._snapshots[norm_name] = FileSnapshot(
                filename=norm_name,
                original_content="" if recorded_link else content,
                is_created_new=False if recorded_link else is_new,
                link_target=recorded_link,
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
            label = restore_snapshot_entry(
                entry,
                link_target=snap.link_target,
                original_content=snap.original_content,
                is_created_new=snap.is_created_new,
            )
            reverted.append(f"{filename} ({label})")
        self._snapshots.clear()
        return reverted

    def clear(self) -> None:
        """Clear all snapshots."""
        self._snapshots.clear()

    def export_snapshots(self) -> List[Dict[str, Any]]:
        """Export all recorded snapshots for persistent storage."""
        return [export_snapshot(snap) for snap in self._snapshots.values()]

    def import_snapshots(self, snapshots_data: List[Dict[str, Any]]) -> None:
        """Import snapshots from persistent storage to restore rollback state."""
        for item in snapshots_data or []:
            snap = snapshot_from_dict(item) if isinstance(item, dict) else None
            if snap is None:
                continue
            norm_name = str(Path(snap.filename))
            self._snapshots[norm_name] = FileSnapshot(
                filename=norm_name,
                original_content=snap.original_content,
                is_created_new=snap.is_created_new,
                link_target=snap.link_target,
            )

    def compute_diffs(self) -> List[FileDiff]:
        """Generate unified diffs between original snapshots and current disk states."""
        diffs: List[FileDiff] = []
        for filename, snap in self._snapshots.items():
            if is_credential_path(filename):
                diffs.append(credential_file_diff(filename))
                continue
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
