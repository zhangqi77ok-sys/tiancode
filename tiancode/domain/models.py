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

    @property
    def is_terminated(self) -> bool:
        """Check if all steps have reached a terminal state (COMPLETED or FAILED)."""
        return len(self.steps) > 0 and all(
            s.status in (StepStatus.COMPLETED, StepStatus.FAILED) for s in self.steps
        )

    @property
    def has_failed(self) -> bool:
        """Check if any step has failed."""
        return any(s.status == StepStatus.FAILED for s in self.steps)

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
        """Mark current step as completed and advance current_step_index."""
        curr = self.current_step()
        if curr is None or curr.step_id != step_id:
            return False
        curr.status = StepStatus.COMPLETED
        curr.result_summary = summary
        next_idx = self.current_step_index + 1
        if next_idx < len(self.steps):
            self.current_step_index = next_idx
            if self.steps[next_idx].status == StepStatus.TODO:
                self.steps[next_idx].status = StepStatus.IN_PROGRESS
        else:
            self.current_step_index = len(self.steps)
        return True

    def fail_step(self, step_id: int, reason: str = "") -> bool:
        """Mark current step as failed and advance current_step_index."""
        curr = self.current_step()
        if curr is None or curr.step_id != step_id:
            return False
        curr.status = StepStatus.FAILED
        curr.result_summary = reason
        next_idx = self.current_step_index + 1
        if next_idx < len(self.steps):
            self.current_step_index = next_idx
            if self.steps[next_idx].status == StepStatus.TODO:
                self.steps[next_idx].status = StepStatus.IN_PROGRESS
        else:
            self.current_step_index = len(self.steps)
        return True


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


@dataclass
class SubagentReport:
    """Synthesized research report returned by an isolated subagent."""

    role: str
    objective: str
    key_findings: List[str] = field(default_factory=list)
    referenced_files: List[str] = field(default_factory=list)
    summary_text: str = ""

    def format_for_master(self) -> str:
        """Format the report into clean, high-density observation text for the master agent."""
        lines = [
            f"📋 【子智能体调研报告 (Role: {self.role})】",
            f"• 调研目标: {self.objective}",
            f"• 核心结论: {self.summary_text}",
        ]
        if self.key_findings:
            lines.append("• 关键事实清单:")
            for item in self.key_findings:
                lines.append(f"  - {item}")
        if self.referenced_files:
            lines.append(f"• 关联文件: {', '.join(self.referenced_files)}")
        return "\n".join(lines)


@dataclass
class SessionMetadata:
    """Metadata describing a saved TianCode session."""

    session_id: str
    work_dir: str
    created_at: str
    updated_at: str
    task_title: str = ""
    steps_total: int = 0
    steps_completed: int = 0
    message_count: int = 0


@dataclass
class PersistedSession:
    """Full session payload persisted to disk."""

    metadata: SessionMetadata
    messages: List[dict]
    plan: Optional[dict] = None
    snapshots: Optional[List[dict]] = None
