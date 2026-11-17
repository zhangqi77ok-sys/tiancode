"""Domain interfaces and protocols for TianCode."""

from typing import Any, Protocol, Dict, Optional, List
from tiancode.domain.models import FileSnapshot, FileDiff


class SnapshotStore(Protocol):
    """Protocol for managing file modification snapshots, checkpoints, and rollbacks."""

    def record_snapshot(self, filename: str, content: str, is_new: bool = False) -> None:
        """Record original state before a file is touched."""
        ...

    def get_snapshot(self, filename: str) -> Optional[FileSnapshot]:
        """Retrieve the snapshot for a given file."""
        ...

    def get_all_snapshots(self) -> Dict[str, FileSnapshot]:
        """Retrieve all recorded snapshots in current session."""
        ...

    def create_checkpoint(self, tag: str = "") -> str:
        """Create a point-in-time checkpoint and return its identifier."""
        ...

    def rollback(self, checkpoint_id: Optional[str] = None) -> List[str]:
        """Revert all modified files to original states and delete newly created files."""
        ...

    def rollback_file(self, filename: str) -> bool:
        """Rollback a single file to its pre-modification state."""
        ...

    def clear(self) -> None:
        """Clear all snapshots and checkpoints."""
        ...

    def compute_diffs(self) -> List[FileDiff]:
        """Compute unified diffs for all modified files."""
        ...

    def export_snapshots(self) -> List[Dict[str, Any]]:
        """Export all recorded snapshots for persistent storage."""
        ...

    def import_snapshots(self, snapshots_data: List[Dict[str, Any]]) -> None:
        """Import snapshots from persistent storage to restore rollback state."""
        ...
