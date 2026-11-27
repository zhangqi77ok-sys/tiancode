"""Read-only plan gates and doom-loop accounting."""

from __future__ import annotations

from langchain_core.messages import AIMessage

from tiancode.core.permissions import PermissionDecision, PermissionGuard
from tiancode.core.tool_dispatcher import SafeToolDispatcher


class _Echo:
    name = "echo"

    def invoke(self, args: dict) -> str:
        return f"ran:{args.get('text')}"


def _state(text: str, call_id: str = "1") -> dict:
    return {
        "messages": [
            AIMessage(
                content="",
                tool_calls=[{"name": "echo", "args": {"text": text}, "id": call_id, "type": "tool_call"}],
            )
        ]
    }


def test_read_only_blocks_plan_mutations_and_allows_inspection() -> None:
    guard = PermissionGuard(mode="read_only")
    assert guard.check("show_task_plan", {}).allowed is True
    assert guard.check("read_file", {}).allowed is True
    for name in ("create_task_plan", "complete_step", "fail_step"):
        result = guard.check(name, {})
        assert result.allowed is False
        assert "只读" in result.reason


def test_denied_calls_do_not_trip_the_doom_loop() -> None:
    guard = PermissionGuard(mode="ask", prompt_fn=lambda _name, _args: PermissionDecision.DENY)
    dispatcher = SafeToolDispatcher([_Echo()], permission_guard=guard, max_loop_repeats=3)
    for index in range(5):
        outcome = dispatcher(_state("same", call_id=str(index)))
        text = outcome["messages"][0].content
        assert "拒绝" in text
        assert "死循环" not in text


def test_repeated_execution_still_trips_the_doom_loop() -> None:
    guard = PermissionGuard(mode="auto")
    dispatcher = SafeToolDispatcher([_Echo()], permission_guard=guard, max_loop_repeats=3)
    first = dispatcher(_state("same", "1"))["messages"][0].content
    second = dispatcher(_state("same", "2"))["messages"][0].content
    third = dispatcher(_state("same", "3"))["messages"][0].content
    assert first.startswith("ran:")
    assert second.startswith("ran:")
    assert "死循环" in third


def test_alternating_calls_trip_ping_pong_without_counting_denials() -> None:
    guard = PermissionGuard(mode="auto")
    dispatcher = SafeToolDispatcher([_Echo()], permission_guard=guard, max_loop_repeats=9)
    texts = []
    for index, text in enumerate(("a", "b", "a", "b", "a")):
        texts.append(dispatcher(_state(text, str(index)))["messages"][0].content)
    assert all(item.startswith("ran:") for item in texts[:4])
    assert "死循环" in texts[4]
