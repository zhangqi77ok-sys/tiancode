"""Safe tool dispatcher with parallel read-only execution and Doom-Loop circuit breaking."""

from collections import deque
from concurrent.futures import ThreadPoolExecutor
import json
from typing import Any, Deque, Dict, FrozenSet, List, Optional, Sequence, Tuple
from langchain_core.messages import ToolMessage
from langchain_core.tools import BaseTool

from tiancode.core.permissions import PermissionGuard

READ_ONLY_TOOLS: FrozenSet[str] = frozenset({
    "grep_search",
    "read_file",
    "list_files",
    "get_repo_map",
    "web_fetch",
    "git_status",
    "git_diff",
})

DEFAULT_MAX_WORKERS = 4
DEFAULT_MAX_LOOP_REPEATS = 3


class DoomLoopDetector:
    """Detects consecutive and alternating ping-pong tool call loops to protect token budget."""

    def __init__(self, max_repeats: int = DEFAULT_MAX_LOOP_REPEATS) -> None:
        self.max_repeats = max(2, max_repeats)
        self._last_signature: Optional[str] = None
        self._repeat_count: int = 0
        self._history: Deque[str] = deque(maxlen=8)

    @staticmethod
    def _build_signature(name: str, args: Any) -> str:
        try:
            args_canon = json.dumps(args, sort_keys=True, ensure_ascii=False, default=str)
        except Exception:
            args_canon = str(args)
        return f"{name}|{args_canon}"

    def _is_ping_pong_loop(self) -> bool:
        """Detect A -> B -> A -> B -> A alternating cycle across recent calls."""
        if len(self._history) < 5:
            return False
        a, b, c, d, e = (
            self._history[-5],
            self._history[-4],
            self._history[-3],
            self._history[-2],
            self._history[-1],
        )
        return a == c == e and b == d and a != b

    def should_block(self, name: str, args: Any) -> bool:
        """Record call signature and return True if consecutive or ping-pong loop hits threshold."""
        sig = self._build_signature(name, args)
        self._history.append(sig)
        if sig == self._last_signature:
            self._repeat_count += 1
        else:
            self._last_signature = sig
            self._repeat_count = 1
        return self._repeat_count >= self.max_repeats or self._is_ping_pong_loop()

    def reset(self) -> None:
        """Clear loop tracking state on session reset or switch."""
        self._last_signature = None
        self._repeat_count = 0
        self._history.clear()


class SafeToolDispatcher:
    """LangGraph tool node executing pure read-only batches concurrently and mutating batches serially."""

    def __init__(
        self,
        tools: Sequence[BaseTool],
        *,
        max_loop_repeats: int = DEFAULT_MAX_LOOP_REPEATS,
        max_workers: int = DEFAULT_MAX_WORKERS,
        permission_guard: Optional[PermissionGuard] = None,
    ) -> None:
        self._tool_map: Dict[str, BaseTool] = {t.name: t for t in tools}
        self._loop_detector = DoomLoopDetector(max_repeats=max_loop_repeats)
        self._max_workers = max(1, max_workers)
        self._permission_guard = permission_guard

    def reset_loop_guard(self) -> None:
        """Reset underlying Doom-Loop detection counters."""
        self._loop_detector.reset()

    @staticmethod
    def _extract_tool_calls(state: Dict[str, Any]) -> List[Dict[str, Any]]:
        messages = state.get("messages", [])
        if not messages:
            return []
        last_msg = messages[-1]
        return list(getattr(last_msg, "tool_calls", None) or [])

    def _build_loop_breaker_msg(self, tc: Dict[str, Any]) -> ToolMessage:
        name = str(tc.get("name", "unknown"))
        call_id = str(tc.get("id", ""))
        content = (
            f"🛑 【死循环熔断拦截 (Doom-Loop Guard)】\n"
            f"检测到连续 {self._loop_detector.max_repeats} 次使用完全相同的参数重复调用 '{name}'。\n"
            f"已阻断本次重复执行以保护推理预算。请立即调整思路：检查报错原因、改用 grep_search / read_file 重新核实源码，或调用 rollback_turn 安全回滚。"
        )
        return ToolMessage(content=content, tool_call_id=call_id, name=name)

    @staticmethod
    def _build_permission_denied_msg(tc: Dict[str, Any], reason: str) -> ToolMessage:
        name = str(tc.get("name", "unknown"))
        call_id = str(tc.get("id", ""))
        return ToolMessage(content=reason, tool_call_id=call_id, name=name)

    @staticmethod
    def _parse_args(raw_args: Any) -> Any:
        if raw_args is None:
            return {}
        if isinstance(raw_args, str):
            try:
                return json.loads(raw_args)
            except Exception:
                return raw_args
        return raw_args

    def _execute_single(self, tc: Dict[str, Any]) -> ToolMessage:
        name = str(tc.get("name", "unknown"))
        call_id = str(tc.get("id", ""))
        args = self._parse_args(tc.get("args"))

        tool_obj = self._tool_map.get(name)
        if tool_obj is None:
            avail = sorted(self._tool_map.keys())
            msg = f"❌ 工具调用失败：未注册的工具 '{name}'。可用工具: {avail}"
            return ToolMessage(content=msg, tool_call_id=call_id, name=name)

        try:
            raw = tool_obj.invoke(args)
            text = raw if isinstance(raw, str) else str(raw)
            return ToolMessage(content=text, tool_call_id=call_id, name=name)
        except Exception as exc:
            msg = f"❌ 工具 '{name}' 参数或执行异常: {exc}"
            return ToolMessage(content=msg, tool_call_id=call_id, name=name)

    def __call__(self, state: Dict[str, Any]) -> Dict[str, List[ToolMessage]]:
        """Dispatch tool_calls safely with Doom-Loop blocking and read-only parallelism."""
        tool_calls = self._extract_tool_calls(state)
        if not tool_calls:
            return {"messages": []}

        results_by_idx: Dict[int, ToolMessage] = {}
        allowed: List[Tuple[int, Dict[str, Any]]] = []

        # Pre-check loop breaker and permissions sequentially on coordinator thread (race-free)
        for idx, tc in enumerate(tool_calls):
            name = str(tc.get("name", "unknown"))
            args = self._parse_args(tc.get("args"))
            if self._loop_detector.should_block(name, args):
                results_by_idx[idx] = self._build_loop_breaker_msg(tc)
                continue
            if self._permission_guard is not None:
                check_res = self._permission_guard.check(name, args)
                if not check_res.allowed:
                    results_by_idx[idx] = self._build_permission_denied_msg(tc, check_res.reason)
                    continue
            allowed.append((idx, tc))

        if allowed:
            # Batch optimization: defer post-edit test verification to the final mutating call in a batch
            mutating_indices = [
                i for i, (_, tc) in enumerate(allowed)
                if str(tc.get("name", "")) in ("edit_file", "multi_edit", "write_file")
            ]
            if len(mutating_indices) > 1:
                for pos in mutating_indices[:-1]:
                    idx, tc = allowed[pos]
                    parsed = self._parse_args(tc.get("args"))
                    if isinstance(parsed, dict) and "skip_verify" not in parsed:
                        allowed[pos] = (idx, {**tc, "args": {**parsed, "skip_verify": True}})

            is_pure_read_only = len(allowed) > 1 and all(
                str(tc.get("name", "")) in READ_ONLY_TOOLS for _, tc in allowed
            )
            if is_pure_read_only:
                workers = min(self._max_workers, len(allowed))
                with ThreadPoolExecutor(max_workers=workers) as pool:
                    futures = [(idx, pool.submit(self._execute_single, tc)) for idx, tc in allowed]
                    for idx, fut in futures:
                        results_by_idx[idx] = fut.result()
            else:
                for idx, tc in allowed:
                    results_by_idx[idx] = self._execute_single(tc)

        ordered = [results_by_idx[i] for i in range(len(tool_calls))]
        return {"messages": ordered}
