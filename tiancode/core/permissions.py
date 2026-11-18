"""Human-in-the-loop (HITL) permission guard, human correction responses, and safety modes."""

from dataclasses import dataclass
from enum import Enum
from typing import Any, Callable, Dict, Optional, Set, Union


class PermissionMode(str, Enum):
    """Execution governance modes controlling mutating and shell tool access."""

    ASK = "ask"
    AUTO = "auto"
    READ_ONLY = "read_only"

    @classmethod
    def from_value(cls, value: Union["PermissionMode", str]) -> "PermissionMode":
        """Parse and validate a permission mode name or enum instance."""
        if isinstance(value, cls):
            return value
        normalized = str(value).strip().lower().replace("-", "_")
        for member in cls:
            if member.value == normalized:
                return member
        valid = ", ".join(m.value for m in cls)
        raise ValueError(f"不支持的权限模式 '{value}'，可选值: {valid}")


class PermissionDecision(str, Enum):
    """User or policy response for an approval-gated tool invocation."""

    ALLOW_ONCE = "allow_once"
    ALLOW_SESSION = "allow_session"
    DENY = "deny"


@dataclass(frozen=True)
class ApprovalResponse:
    """Rich approval outcome carrying optional human correction feedback for the agent."""

    decision: PermissionDecision
    feedback: str = ""


@dataclass(frozen=True)
class PermissionCheckResult:
    """Outcome of checking a tool invocation against PermissionGuard."""

    allowed: bool
    reason: str = ""


ApprovalPromptFn = Callable[[str, Dict[str, Any]], Union[PermissionDecision, ApprovalResponse]]

SAFE_READ_TOOLS = frozenset({
    "read_file",
    "grep_search",
    "list_files",
    "get_repo_map",
    "show_task_plan",
    "create_task_plan",
    "complete_step",
    "fail_step",
    "invoke_subagent",
    "recall_memory",
})


class PermissionGuard:
    """Thread-safe coordinator gate enforcing read-only, auto, and interactive approval/steering."""

    def __init__(
        self,
        mode: Union[PermissionMode, str] = PermissionMode.ASK,
        prompt_fn: Optional[ApprovalPromptFn] = None,
    ) -> None:
        self._mode: PermissionMode = PermissionMode.from_value(mode)
        self.prompt_fn: Optional[ApprovalPromptFn] = prompt_fn
        self._session_approved_tools: Set[str] = set()

    @property
    def mode(self) -> PermissionMode:
        """Current active permission mode."""
        return self._mode

    def set_mode(
        self,
        mode: Union[PermissionMode, str],
        prompt_fn: Optional[ApprovalPromptFn] = None,
    ) -> PermissionMode:
        """Switch mode dynamically; optionally update approval callback."""
        self._mode = PermissionMode.from_value(mode)
        if prompt_fn is not None:
            self.prompt_fn = prompt_fn
        return self._mode

    def clear_session_approvals(self) -> None:
        """Clear tools granted session-wide approval."""
        self._session_approved_tools.clear()

    def check(self, tool_name: str, args: Any) -> PermissionCheckResult:
        """Evaluate whether tool_name may execute; must run on coordinator thread before dispatch."""
        if tool_name in SAFE_READ_TOOLS:
            return PermissionCheckResult(allowed=True)

        if self._mode is PermissionMode.READ_ONLY:
            return PermissionCheckResult(
                allowed=False,
                reason=f"⛔ 【只读模式拦截 (Read-Only Mode)】禁止在只读模式下执行变更或命令工具 '{tool_name}'。",
            )

        if self._mode is PermissionMode.AUTO or tool_name in self._session_approved_tools:
            return PermissionCheckResult(allowed=True)

        return self._request_approval(tool_name, args if isinstance(args, dict) else {"raw_args": args})

    def _request_approval(self, tool_name: str, args: Dict[str, Any]) -> PermissionCheckResult:
        """Prompt operator via callback using strict fail-closed and human-steering semantics."""
        if self.prompt_fn is None:
            return PermissionCheckResult(
                allowed=False,
                reason=f"⛔ 【权限拒绝】交互确认模式 (ask) 下未提供确认回调，已拒绝执行 '{tool_name}'。",
            )

        try:
            raw_outcome = self.prompt_fn(tool_name, args)
        except Exception as exc:
            return PermissionCheckResult(
                allowed=False,
                reason=f"⛔ 【权限异常拒绝】确认过程出错 ({exc})，已拒绝执行 '{tool_name}'。",
            )

        if isinstance(raw_outcome, ApprovalResponse):
            decision = raw_outcome.decision
            feedback = raw_outcome.feedback.strip()
        else:
            decision = raw_outcome
            feedback = ""

        if decision is PermissionDecision.ALLOW_ONCE:
            return PermissionCheckResult(allowed=True)
        if decision is PermissionDecision.ALLOW_SESSION:
            self._session_approved_tools.add(tool_name)
            return PermissionCheckResult(allowed=True)

        if feedback:
            return PermissionCheckResult(
                allowed=False,
                reason=(
                    f"⛔ 【人工纠正指导 (Human Correction)】用户拦截了工具 '{tool_name}' 的本次调用，"
                    f"并给出明确修正指令：“{feedback}”。请严格按照该人工指令调整下一步操作！"
                ),
            )

        return PermissionCheckResult(
            allowed=False,
            reason=f"⛔ 【用户拒绝执行】操作 '{tool_name}' 未获得授权，请调整方案或向用户确认需求。",
        )
