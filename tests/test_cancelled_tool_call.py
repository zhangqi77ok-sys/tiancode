"""Cancelling a turn after a tool call is announced must leave a continuable transcript.

No network and no real credential: the model is a local stub, and every on-disk
TianCode path points at the test tmp directory.
"""

from __future__ import annotations

from pathlib import Path

import pytest
from langchain_core.messages import AIMessage, HumanMessage, ToolMessage

from tiancode.cli.formatter import TerminalFormatter
from tiancode.config.settings import Settings
from tiancode.core.engine import TianCodeEngine
from tiancode.core.history import InMemorySnapshotStore
from tiancode.core.stream_events import INTERRUPTED_TOOL_NOTE, close_pending_tool_calls


def _isolate(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    monkeypatch.setenv("TIANCODE_SESSIONS_PATH", str(tmp_path / "sessions"))
    monkeypatch.setenv("TIANCODE_CONFIG_PATH", str(tmp_path / "config.json"))
    monkeypatch.setenv("TIANCODE_MEMORY_PATH", str(tmp_path / "memory.json"))
    monkeypatch.setenv("TIANCODE_MCP_CONFIG", str(tmp_path / "mcp.json"))
    monkeypatch.setenv("TIANCODE_RULES_PATH", str(tmp_path / "rules"))
    monkeypatch.setenv("TIANSHELL_RUNTIME_PATH", str(tmp_path / "no-tianshell-runtime.json"))
    (tmp_path / "config.json").write_text("{}", encoding="utf-8")
    (tmp_path / "mcp.json").write_text('{"mcpServers":{}}', encoding="utf-8")
    (tmp_path / "rules").mkdir()


def _pending_ids(messages: list) -> list[str]:
    pending: list[str] = []
    for message in messages:
        tool_calls = getattr(message, "tool_calls", None) or []
        if isinstance(message, AIMessage) and tool_calls:
            pending.extend(str(call.get("id")) for call in tool_calls if call.get("id"))
        elif isinstance(message, ToolMessage) and message.tool_call_id in pending:
            pending.remove(message.tool_call_id)
        elif isinstance(message, HumanMessage) and pending:
            return list(pending)
    return pending


def test_close_pending_tool_calls_fills_only_missing_ids() -> None:
    assistant = AIMessage(
        content="",
        tool_calls=[
            {"name": "read_file", "args": {}, "id": "call-a", "type": "tool_call"},
            {"name": "read_file", "args": {}, "id": "call-b", "type": "tool_call"},
        ],
    )
    answered = ToolMessage(content="already-read", tool_call_id="call-a", name="read_file")
    follow_up = HumanMessage(content="go on")
    closed = close_pending_tool_calls([
        HumanMessage(content="start"),
        assistant,
        answered,
        follow_up,
    ])

    assert [type(message).__name__ for message in closed] == [
        "HumanMessage",
        "AIMessage",
        "ToolMessage",
        "ToolMessage",
        "HumanMessage",
    ]
    assert closed[2] is answered
    assert closed[3].tool_call_id == "call-b"
    assert closed[3].name == "read_file"
    assert closed[3].content == INTERRUPTED_TOOL_NOTE
    assert closed[4] is follow_up
    assert len(close_pending_tool_calls(closed)) == len(closed)

    done = ToolMessage(content="ok", tool_call_id="only", name="read_file")
    complete = [
        AIMessage(
            content="",
            tool_calls=[{"name": "read_file", "args": {}, "id": "only", "type": "tool_call"}],
        ),
        done,
    ]
    assert close_pending_tool_calls(complete)[1] is done


def test_cancel_at_tool_start_keeps_file_and_next_turn_valid(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    _isolate(monkeypatch, tmp_path)
    work = (tmp_path / "work").resolve()
    work.mkdir()
    (work / "note.txt").write_text("keep\n", encoding="utf-8")
    settings = Settings.load(api_key="sk-test", work_dir=work, config_path=tmp_path / "config.json")
    settings.auto_heal = False
    engine = TianCodeEngine(settings=settings, snapshot_store=InMemorySnapshotStore(work_dir=work))

    class ScriptedModel:
        def __init__(self) -> None:
            self.seen: list[list] = []

        def invoke(self, messages):
            self.seen.append(list(messages))
            if len(self.seen) == 1:
                return AIMessage(
                    content="",
                    tool_calls=[{
                        "name": "write_file",
                        "args": {"filename": "cancel_out.txt", "content": "SHOULD-NOT-LAND"},
                        "id": "call_cancel",
                        "type": "tool_call",
                    }],
                )
            pending = _pending_ids(messages)
            if pending:
                raise AssertionError(f"provider would reject unanswered tool calls: {pending}")
            return AIMessage(content="continued")

    model = ScriptedModel()
    engine.sync_config_if_changed = lambda *args, **kwargs: False
    engine.llm_with_tools = model
    engine.set_permission_mode("auto")

    def _cancel_on_tool_start(cls, state, event):
        if isinstance(event, dict) and event.get("type") == "tool_start":
            raise KeyboardInterrupt
        return None

    monkeypatch.setattr(TerminalFormatter, "_dispatch_stream_event", classmethod(_cancel_on_tool_start))

    try:
        with pytest.raises(KeyboardInterrupt):
            TerminalFormatter.render_task_stream(
                engine.stream_task("write something", thread_id="sess-cancel"),
                use_spinner=False,
            )

        assert not (work / "cancel_out.txt").exists()
        assert (work / "note.txt").read_text(encoding="utf-8") == "keep\n"
        saved = engine.session_store.load("sess-cancel")
        assert saved is not None
        tool_results = [
            message for message in saved.messages
            if message.get("type") == "tool" or (message.get("data") or {}).get("tool_call_id") == "call_cancel"
        ]
        assert tool_results, saved.messages
        blob = str(saved.messages)
        assert "call_cancel" in blob
        assert "没有执行成功" in blob

        list(engine.stream_task("please continue", thread_id="sess-cancel"))
        assert len(model.seen) == 2
        follow = model.seen[1]
        assert _pending_ids(follow) == []
        assert any(
            isinstance(message, ToolMessage) and message.tool_call_id == "call_cancel"
            for message in follow
        )
        assert not (work / "cancel_out.txt").exists()
    finally:
        engine.close()


def test_resume_of_legacy_dangling_tool_call_closes_it(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    _isolate(monkeypatch, tmp_path)
    work = (tmp_path / "work").resolve()
    work.mkdir()
    settings = Settings.load(api_key="sk-test", work_dir=work, config_path=tmp_path / "config.json")
    settings.auto_heal = False
    engine = TianCodeEngine(settings=settings, snapshot_store=InMemorySnapshotStore(work_dir=work))

    class ScriptedModel:
        def __init__(self) -> None:
            self.seen: list[list] = []

        def invoke(self, messages):
            self.seen.append(list(messages))
            pending = _pending_ids(messages)
            if pending:
                raise AssertionError(f"provider would reject unanswered tool calls: {pending}")
            return AIMessage(content="continued")

    try:
        engine.session_store.save(
            session_id="legacy-cancel",
            work_dir=str(work),
            messages=[
                HumanMessage(content="write it"),
                AIMessage(
                    content="",
                    tool_calls=[{
                        "name": "write_file",
                        "args": {"filename": "cancel_out.txt", "content": "NO"},
                        "id": "call_old",
                        "type": "tool_call",
                    }],
                ),
            ],
        )
        assert engine.resume_session("legacy-cancel") is True
        restored = engine.graph.get_state({"configurable": {"thread_id": "legacy-cancel"}}).values.get("messages", [])
        assert _pending_ids(restored) == []
        assert any(isinstance(message, ToolMessage) and message.tool_call_id == "call_old" for message in restored)

        model = ScriptedModel()
        engine.sync_config_if_changed = lambda *args, **kwargs: False
        engine.llm_with_tools = model
        engine.set_permission_mode("auto")
        list(engine.stream_task("continue from disk", thread_id="legacy-cancel"))
        assert model.seen
        assert _pending_ids(model.seen[0]) == []
        assert not (work / "cancel_out.txt").exists()
    finally:
        engine.close()
