"""Checkpoint replace, tool-call pairing, undo persistence, and visible save failures."""

from __future__ import annotations

import json
from pathlib import Path

import pytest
from langchain_core.messages import AIMessage, HumanMessage, SystemMessage, ToolMessage

from tiancode.cli.commands import SlashCommandHandler
from tiancode.core.compactor import ContextCompactor
from tiancode.core.stream_events import (
    INTERRUPTED_TOOL_NOTE,
    close_pending_tool_calls,
    persist_engine_session,
)
from tests.test_session_resume import _contents, _isolate, _make_engine


def _tool_call(call_id: str, name: str = "read_file") -> dict:
    return {"name": name, "args": {"filename": "a.py"}, "id": call_id, "type": "tool_call"}


def _assert_tool_pairs(messages: list) -> None:
    """Every tool call has exactly one following result before the next turn."""
    pending: list[str] = []
    seen: set[str] = set()
    for message in messages:
        if isinstance(message, AIMessage) and getattr(message, "tool_calls", None):
            assert not pending
            pending = [str(call["id"]) for call in message.tool_calls if call.get("id")]
            continue
        if isinstance(message, ToolMessage):
            assert pending, "orphan tool result"
            assert message.tool_call_id == pending[0]
            assert message.tool_call_id not in seen
            seen.add(message.tool_call_id)
            pending.pop(0)
            continue
        assert not pending, "open tool call before the next message"
    assert not pending


def test_close_pending_pulls_tool_result_back_across_a_system_card() -> None:
    assistant = AIMessage(content="", tool_calls=[_tool_call("call-a"), _tool_call("call-b")])
    card = SystemMessage(content="📋 【上下文智能折叠摘要】\nfolded")
    result_b = ToolMessage(content="file-b", tool_call_id="call-b", name="read_file")
    closed = close_pending_tool_calls([
        HumanMessage(content="start"),
        assistant,
        card,
        result_b,
        HumanMessage(content="go on"),
    ])

    assert [type(message).__name__ for message in closed] == [
        "HumanMessage",
        "AIMessage",
        "ToolMessage",
        "ToolMessage",
        "SystemMessage",
        "HumanMessage",
    ]
    assert closed[2].content == INTERRUPTED_TOOL_NOTE
    assert closed[2].tool_call_id == "call-a"
    assert closed[3] is result_b
    assert closed[4] is card
    assert len(close_pending_tool_calls(closed)) == len(closed)
    _assert_tool_pairs(closed)


def test_compact_keeps_tool_pairs_when_the_card_would_split_them() -> None:
    opening = AIMessage(content="edit the file", tool_calls=[_tool_call("call-1")])
    tool_result = ToolMessage(content="x" * 4000, tool_call_id="call-1", name="read_file")
    tail = [HumanMessage(content=f"note {index} " + ("y" * 200)) for index in range(8)]
    compacted = ContextCompactor(max_retained_messages=4, max_context_chars=1200).compact(
        [opening, tool_result, *tail]
    )
    _assert_tool_pairs(compacted)
    assert any(getattr(message, "tool_call_id", None) == "call-1" for message in compacted)


def test_compact_replaces_checkpoint_instead_of_appending(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    _isolate(monkeypatch, tmp_path)
    work = (tmp_path / "work").resolve()
    work.mkdir()
    engine = _make_engine(work, tmp_path / "config.json", git_backed=False)
    try:
        engine.active_session_id = "compact-me"
        engine.settings.max_history_messages = 4
        engine.settings.max_context_chars = 800
        config = {"configurable": {"thread_id": "compact-me"}}
        history = [HumanMessage(content=f"turn {index} " + ("z" * 180)) for index in range(12)]
        engine.graph.update_state(config, {"messages": history})
        before = len(_contents(engine, "compact-me"))

        SlashCommandHandler(engine)._handle_compact()
        after = len(_contents(engine, "compact-me"))
        assert after < before

        SlashCommandHandler(engine)._handle_compact()
        assert len(_contents(engine, "compact-me")) <= after
    finally:
        engine.close()


def test_undo_persists_live_transcript_not_the_stale_file(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    _isolate(monkeypatch, tmp_path)
    work = (tmp_path / "work").resolve()
    work.mkdir()
    target = work / "notes.txt"
    target.write_text("live\n", encoding="utf-8")
    engine = _make_engine(work, tmp_path / "config.json", git_backed=False)
    try:
        engine.active_session_id = "undo-me"
        engine.graph.update_state(
            {"configurable": {"thread_id": "undo-me"}},
            {"messages": [HumanMessage(content="LIVE-TURN")]},
        )
        engine.session_store.save(
            session_id="undo-me",
            work_dir=str(work),
            messages=[HumanMessage(content="STALE-DISK")],
            plan=None,
            snapshots=[],
        )
        engine.snapshot_store.record_snapshot(str(target), "snap\n", is_new=False)
        target.write_text("dirty\n", encoding="utf-8")

        SlashCommandHandler(engine)._handle_undo()

        assert target.read_text(encoding="utf-8") == "snap\n"
        saved = json.loads((tmp_path / "sessions" / "undo-me.json").read_text(encoding="utf-8"))
        blob = json.dumps(saved["messages"], ensure_ascii=False)
        assert "LIVE-TURN" in blob
        assert "STALE-DISK" not in blob
        assert saved["snapshots"] == []
    finally:
        engine.close()


def test_persist_failure_is_visible_and_does_not_raise(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    _isolate(monkeypatch, tmp_path)
    work = (tmp_path / "work").resolve()
    work.mkdir()
    engine = _make_engine(work, tmp_path / "config.json", git_backed=False)
    try:
        engine.graph.update_state(
            {"configurable": {"thread_id": "persist-me"}},
            {"messages": [HumanMessage(content="keep")]},
        )

        def _boom(**_kwargs):
            raise OSError("disk full")

        monkeypatch.setattr(engine.session_store, "save", _boom)
        error = persist_engine_session(engine, "persist-me", {"configurable": {"thread_id": "persist-me"}})
        assert error is not None
        assert "disk full" in error
        assert engine.last_persist_error == error
        assert _contents(engine, "persist-me") == ["keep"]
    finally:
        engine.close()
