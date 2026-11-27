"""Benchmark runs close the engine and keep session files inside the sandbox."""

from __future__ import annotations

from pathlib import Path

import pytest

from tiancode.config.settings import Settings
from tiancode.core.engine import TianCodeEngine
from tiancode.eval.models import BenchmarkProblem
from tiancode.eval.runner import BenchmarkRunner
from tests.test_session_resume import _isolate


def test_eval_closes_engine_and_keeps_sessions_in_the_sandbox(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    _isolate(monkeypatch, tmp_path)
    closed: dict[str, object] = {}

    def fake_stream(self: TianCodeEngine, prompt: str, thread_id: str = "x"):
        assert "tiancode-eval-sessions" in str(self.session_store.storage_dir)
        assert self.session_store.storage_dir.resolve().is_relative_to(self.settings.work_dir.resolve())
        assert ".tiancode-eval-memory.json" in str(self.memory_store.memory_path)
        assert self.memory_store.memory_path.resolve().is_relative_to(self.settings.work_dir.resolve())
        self.session_store.save(thread_id, str(self.settings.work_dir), [], plan=None, snapshots=[])
        yield {"type": "tool_end", "name": "read_file", "content": "ok"}
        yield {"type": "tool_end", "name": "read_file", "content": "again"}

    def tracking_close(self: TianCodeEngine) -> None:
        closed["called"] = True
        closed["work"] = str(self.settings.work_dir)

    monkeypatch.setattr(TianCodeEngine, "stream_task", fake_stream)
    monkeypatch.setattr(TianCodeEngine, "close", tracking_close)

    problem = BenchmarkProblem(
        id="tiny",
        title="tiny",
        description="do nothing",
        starter_files={"pkg/a.py": "VALUE = 1\n"},
        test_files={"test_a.py": "def test_ok():\n    assert True\n"},
        max_turns=1,
    )
    home_sessions = tmp_path / "sessions"
    result = BenchmarkRunner(
        settings=Settings(api_key="sk-test", work_dir=tmp_path, auto_heal=False)
    ).run_single_problem(problem)

    assert result.success is True
    assert result.turn_count == 1
    assert closed.get("called") is True
    assert list(home_sessions.glob("*.json")) == []
    assert closed["work"] != str(tmp_path.resolve())
