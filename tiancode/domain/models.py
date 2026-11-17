"""Domain models for TianCode."""

from dataclasses import dataclass, field
from typing import List, Optional


@dataclass(frozen=True)
class FileDiff:
    """Represents a unified diff comparison for a file."""

    filename: str
    diff_text: str
    additions: int = 0
    deletions: int = 0
    is_empty: bool = True


@dataclass(frozen=True)
class ExecutionResult:
    """Represents the output from running a command or script."""

    exit_code: int
    stdout: str
    stderr: str
    timed_out: bool = False

    @property
    def is_success(self) -> bool:
        """Check if execution completed without error."""
        return self.exit_code == 0 and not self.timed_out


@dataclass
class FileSnapshot:
    """Snapshot of a file's state before modification."""

    filename: str
    original_content: str
    is_created_new: bool = False
