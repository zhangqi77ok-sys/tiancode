"""Resume must swap one session in place and leave live state untouched on failure.

The checkpoint reducer appends messages. Importing undo snapshots used to merge
them with the session already in memory. Both are exercised here without a
network call or a real credential.
"""

from __future__ import annotations

import json
import shutil
import subprocess
from pathlib import Path

import pytest
from langchain_core.messages import AIMessage, HumanMessage

from tiancode.config.settings import Settings
from tiancode.core.engine import TianCodeEngine
from tiancode.core.git_store import GitSnapshotStore
from tiancode.core.history import InMemorySnapshotStore
from tiancode.domain.models import PlanStep, TaskPlan


def _isolate(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    """Point every on-disk TianCode path at this test's tmp dir."""
    monkeypatch.setenv("TIANCODE_SESSIONS_PATH", str(tmp_path / "sessions"))
    monkeypatch.setenv("TIANCODE_CONFIG_PATH", str(tmp_path / "config.json"))
    monkeypatch.setenv("TIANCODE_MEMORY_PATH", str(tmp_path / "memory.json"))
    monkeypatch.setenv("TIANCODE_MCP_CONFIG", str(tmp_path / "mcp.json"))
    monkeypatch.setenv("TIANCODE_RULES_PATH", str(tmp_path / "rules"))
    monkeypatch.setenv("TIANSHELL_RUNTIME_PATH", str(tmp_path / "no-tianshell-runtime.json"))
    (tmp_path / "config.json").write_text("{}", encoding="utf-8")
    (tmp_path / "mcp.json").write_text('{"mcpServers":{}}', encoding="utf-8")
    (tmp_path / "rules").mkdir()


def _plan(title: str) -> TaskPlan:
    return TaskPlan(
        task_title=title,
        steps=[PlanStep(step_id=1, title=title, description=title)],
    )


def _settings(work: Path, config: Path) -> Settings:
    settings = Settings.load(api_key="sk-test", work_dir=work, config_path=config)
    settings.auto_heal = False
    return settings


def _make_engine(work: Path, config: Path, *, git_backed: bool) -> TianCodeEngine:
    settings = _settings(work, config)
    if not git_backed:
        return TianCodeEngine(settings=settings, snapshot_store=InMemorySnapshotStore(work_dir=work))
    if shutil.which("git") is None:
        pytest.skip("git is not installed")
    subprocess.run(["git", "init"], cwd=work, check=True, capture_output=True, text=True)
    engine = TianCodeEngine(settings=settings)
    assert isinstance(engine.snapshot_store, GitSnapshotStore)
    return engine


def _contents(engine: TianCodeEngine, thread_id: str) -> list[str]:
    state = engine.graph.get_state({"configurable": {"thread_id": thread_id}})
    messages = (getattr(state, "values", None) or {}).get("messages") or []
    return [str(message.content) for message in messages]


def _total_tokens(engine: TianCodeEngine) -> int:
    tokens, _cost = engine.usage_tracker.get_session_stats()
    return tokens.total_tokens


@pytest.mark.parametrize("git_backed", [False, True])
def test_resume_replaces_transcript_and_undo_set(tmp_path: Path, monkeypatch: pytest.MonkeyPatch, git_backed: bool) -> None:
    _isolate(monkeypatch, tmp_path)
    work = (tmp_path / "work").resolve()
    work.mkdir()
    file_a = work / "a.py"
    file_b = work / "b.py"
    file_a.write_text("live-A\n", encoding="utf-8")
    file_b.write_text("live-B\n", encoding="utf-8")

    engine = _make_engine(work, tmp_path / "config.json", git_backed=git_backed)
    try:
        engine.graph.update_state(
            {"configurable": {"thread_id": "session-B"}},
            {"messages": [HumanMessage(content="STALE")]},
        )
        engine.active_session_id = "session-A"
        engine.planner.active_plan = _plan("keep-me")
        engine.snapshot_store.record_snapshot(str(file_a), "snap-A\n", is_new=False)
        engine.usage_tracker.record_turn(
            "deepseek-chat",
            {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
        )
        engine.set_permission_mode("ask")
        engine.permission_guard._session_approved_tools.add("shell")
        assert engine.permission_guard.check("shell", {}).allowed is True

        engine.session_store.save(
            session_id="session-B",
            work_dir=str(work),
            messages=[HumanMessage(content="from B"), AIMessage(content="ack B")],
            plan=_plan("plan-B"),
            snapshots=[{
                "filename": str(file_b),
                "original_content": "snap-B\n",
                "is_created_new": False,
            }],
        )

        assert engine.resume_session("session-B") is True
        assert engine.resume_session("session-B") is True
        assert _contents(engine, "session-B") == ["from B", "ack B"]
        assert engine.active_session_id == "session-B"
        assert engine.planner.active_plan is not None
        assert engine.planner.active_plan.task_title == "plan-B"
        assert _total_tokens(engine) == 0
        assert engine.permission_guard.check("shell", {}).allowed is False
        assert engine.permission_guard.check("read_file", {}).allowed is True

        engine.snapshot_store.rollback()
        assert file_a.read_text(encoding="utf-8") == "live-A\n"
        assert file_b.read_text(encoding="utf-8") == "snap-B\n"

        engine.planner.active_plan = _plan("should-clear")
        engine.snapshot_store.record_snapshot(str(file_a), "snap-A\n", is_new=False)
        engine.graph.update_state(
            {"configurable": {"thread_id": "session-D"}},
            {"messages": [HumanMessage(content="STALE-D")]},
        )
        engine.session_store.save(
            session_id="session-C",
            work_dir=str(work),
            messages=[HumanMessage(content="only C")],
            plan=None,
            snapshots=[],
        )
        engine.session_store.save(
            session_id="session-D",
            work_dir=str(work),
            messages=[],
            plan=None,
            snapshots=[],
        )

        assert engine.resume_session("session-C") is True
        assert engine.planner.active_plan is None
        assert _contents(engine, "session-C") == ["only C"]
        assert engine.resume_session("session-D") is True
        assert _contents(engine, "session-D") == []
        assert engine.snapshot_store.rollback() == []
        assert file_a.read_text(encoding="utf-8") == "live-A\n"
        assert file_b.read_text(encoding="utf-8") == "snap-B\n"
    finally:
        engine.close()


@pytest.mark.parametrize("git_backed", [False, True])
def test_resume_from_another_workspace_does_not_write_its_snapshots(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, git_backed: bool
) -> None:
    _isolate(monkeypatch, tmp_path)
    origin = (tmp_path / "origin").resolve()
    current = (tmp_path / "current").resolve()
    origin.mkdir()
    current.mkdir()
    (origin / "notes.txt").write_text("LIVE-ORIGIN\n", encoding="utf-8")
    (current / "notes.txt").write_text("LIVE-CURRENT\n", encoding="utf-8")

    engine = _make_engine(current, tmp_path / "config.json", git_backed=git_backed)
    try:
        engine.session_store.save(
            session_id="from-origin",
            work_dir=str(origin),
            messages=[HumanMessage(content="from origin")],
            plan=None,
            snapshots=[{
                "filename": "notes.txt",
                "original_content": "SNAP-ORIGIN\n",
                "is_created_new": False,
            }],
        )
        engine.session_store.save(
            session_id="from-current",
            work_dir=str(current),
            messages=[HumanMessage(content="from current")],
            plan=None,
            snapshots=[{
                "filename": "notes.txt",
                "original_content": "SNAP-CURRENT\n",
                "is_created_new": False,
            }],
        )

        assert engine.resume_session("from-origin") is True
        assert _contents(engine, "from-origin") == ["from origin"]
        engine.snapshot_store.rollback()
        assert (current / "notes.txt").read_text(encoding="utf-8") == "LIVE-CURRENT\n"
        assert (origin / "notes.txt").read_text(encoding="utf-8") == "LIVE-ORIGIN\n"

        assert engine.resume_session("from-current") is True
        assert _contents(engine, "from-current") == ["from current"]
        engine.snapshot_store.rollback()
        assert (current / "notes.txt").read_text(encoding="utf-8") == "SNAP-CURRENT\n"
        assert (origin / "notes.txt").read_text(encoding="utf-8") == "LIVE-ORIGIN\n"
    finally:
        engine.close()


def test_failed_resume_keeps_live_plan_snapshots_and_transcript(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    _isolate(monkeypatch, tmp_path)
    work = (tmp_path / "work").resolve()
    work.mkdir()
    file_a = work / "a.py"
    file_a.write_text("live-A\n", encoding="utf-8")

    engine = _make_engine(work, tmp_path / "config.json", git_backed=False)
    try:
        engine.active_session_id = "session-A"
        engine.planner.active_plan = _plan("keep-me")
        engine.snapshot_store.record_snapshot(str(file_a), "snap-A\n", is_new=False)
        engine.graph.update_state(
            {"configurable": {"thread_id": "session-A"}},
            {"messages": [HumanMessage(content="live")]},
        )
        engine.usage_tracker.record_turn(
            "deepseek-chat",
            {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
        )
        engine.set_permission_mode("ask")
        engine.permission_guard._session_approved_tools.add("shell")

        assert engine.resume_session("missing-session") is False
        assert engine.active_session_id == "session-A"
        assert engine.planner.active_plan is not None
        assert engine.planner.active_plan.task_title == "keep-me"
        assert _contents(engine, "session-A") == ["live"]
        assert engine.permission_guard.check("shell", {}).allowed is True

        payload = {
            "metadata": {
                "session_id": "session-BAD",
                "work_dir": str(work),
                "created_at": "t0",
                "updated_at": "t1",
                "task_title": "bad",
                "steps_total": 0,
                "steps_completed": 0,
                "message_count": 1,
                "total_tokens": 0,
                "estimated_cost": 0.0,
            },
            "messages": [{"type": "not_a_type", "data": {"content": "x"}}],
            "plan": {"task_title": "OVERWRITTEN", "steps": [], "current_step_index": 0},
            "snapshots": [{
                "filename": str(file_a),
                "original_content": "CORRUPTED\n",
                "is_created_new": False,
            }],
            "usage": {"tokens": {"total_tokens": 999}, "cost": {}, "last_model": "x"},
        }
        engine.session_store._file_path("session-BAD").write_text(
            json.dumps(payload),
            encoding="utf-8",
        )

        assert engine.resume_session("session-BAD") is False
        assert engine.active_session_id == "session-A"
        assert engine.planner.active_plan.task_title == "keep-me"
        assert _contents(engine, "session-A") == ["live"]
        assert _total_tokens(engine) == 15
        assert engine.permission_guard.check("shell", {}).allowed is True
        assert file_a.read_text(encoding="utf-8") == "live-A\n"

        file_a.write_text("dirty\n", encoding="utf-8")
        engine.snapshot_store.rollback()
        assert file_a.read_text(encoding="utf-8") == "snap-A\n"
    finally:
        engine.close()
