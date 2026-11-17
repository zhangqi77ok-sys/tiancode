"""Domain interfaces and protocols for TianCode."""

from typing import Protocol, Dict, Optional, List
from tiancode.domain.models import FileSnapshot, FileDiff


class SnapshotStore(Protocol):
    """Protocol for managing file modification snapshots and rollbacks."""

    def record_snapshot(self, filename: str, content: str, is_new: bool = False) -> None:
        """Record original state before a file is touched."""
        ...

    def get_snapshot(self, filename: str) -> Optional[FileSnapshot]:
        """Retrieve the snapshot for a given file."""
        ...

    def get_all_snapshots(self) -> Dict[str, FileSnapshot]:
        """Retrieve all recorded snapshots in current session."""
        ...

    def rollback(self) -> List[str]:
        """Revert all modified files to original states and delete newly created files."""
        ...

    def clear(self) -> None:
        """Clear all snapshots."""
        ...

    def compute_diffs(self) -> List[FileDiff]:
        """Compute unified diffs for all modified files."""
        ...
