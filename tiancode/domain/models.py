"""Domain models for TianCode."""

from dataclasses import dataclass, field
from enum import Enum
from typing import List, Optional


class StepStatus(str, Enum):
    """Execution status for a task plan step."""

    TODO = "todo"
    IN_PROGRESS = "in_progress"
    COMPLETED = "completed"
    FAILED = "failed"


@dataclass
class PlanStep:
    """Represents a single atomic engineering task."""

    step_id: int
    title: str
    description: str
    status: StepStatus = StepStatus.TODO
    verification: str = ""
    result_summary: str = ""


@dataclass
class TaskPlan:
    """Structured graph plan managing multi-step engineering progression."""

    task_title: str
    steps: List[PlanStep] = field(default_factory=list)
    current_step_index: int = 0

    @property
    def is_finished(self) -> bool:
        """Check if all steps are completed."""
        return len(self.steps) > 0 and all(s.status == StepStatus.COMPLETED for s in self.steps)

    def current_step(self) -> Optional[PlanStep]:
        """Return the current step object."""
        if 0 <= self.current_step_index < len(self.steps):
            return self.steps[self.current_step_index]
        return None

    def start_step(self, step_id: int) -> bool:
        """Mark step as in-progress and update current_step_index."""
        for idx, s in enumerate(self.steps):
            if s.step_id == step_id:
                s.status = StepStatus.IN_PROGRESS
                self.current_step_index = idx
                return True
        return False

    def complete_step(self, step_id: int, summary: str = "") -> bool:
        """Mark step as completed and advance current_step_index."""
        for idx, s in enumerate(self.steps):
            if s.step_id == step_id:
                s.status = StepStatus.COMPLETED
                s.result_summary = summary
                next_idx = idx + 1
                if next_idx < len(self.steps):
                    self.current_step_index = next_idx
                    if self.steps[next_idx].status == StepStatus.TODO:
                        self.steps[next_idx].status = StepStatus.IN_PROGRESS
                return True
        return False

    def fail_step(self, step_id: int, reason: str = "") -> bool:
        """Mark step as failed."""
        for s in self.steps:
            if s.step_id == step_id:
                s.status = StepStatus.FAILED
                s.result_summary = reason
                return True
        return False


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
