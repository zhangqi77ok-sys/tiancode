"""Benchmark runs close the engine and keep session files inside the sandbox."""

from __future__ import annotations

from pathlib import Path

import pytest

from tiancode.config.settings import Settings
from tiancode.core.engine import TianCodeEngine
from tiancode.eval.models import BenchmarkProblem
from tiancode.eval.runner import (
    BenchmarkRunner,
    materialize_sandbox_files,
    run_hidden_pytest,
    sandbox_destination,
)
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


def test_eval_refuses_a_path_outside_the_sandbox(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    import tempfile

    monkeypatch.setattr(tempfile, "tempdir", str(tmp_path))
    problem = BenchmarkProblem(
        id="escape",
        title="escape",
        description="do not write outside",
        starter_files={"../outside.txt": "CANARY-OUTSIDE"},
        test_files={},
    )

    def _must_not_run(_sandbox: Path) -> None:
        raise AssertionError("solver must not run")

    result = BenchmarkRunner(
        settings=Settings(api_key="sk-test", work_dir=tmp_path, auto_heal=False)
    ).run_single_problem(problem, solve_fn=_must_not_run)

    assert result.success is False
    assert "沙箱" in (result.error_message or "")
    assert list(tmp_path.rglob("outside.txt")) == []


def test_materialize_writes_a_nested_file(tmp_path: Path) -> None:
    root = tmp_path / "box"
    root.mkdir()

    materialize_sandbox_files(root, {"pkg/a.py": "VALUE = 1\n"})

    written = root / "pkg" / "a.py"
    assert written.read_text(encoding="utf-8") == "VALUE = 1\n"
    assert written.is_file() and not written.is_symlink()


def test_materialize_does_not_follow_a_directory_symlink(tmp_path: Path) -> None:
    root = tmp_path / "box"
    outside = tmp_path / "outside"
    root.mkdir()
    outside.mkdir()
    (root / "link").symlink_to(outside, target_is_directory=True)

    with pytest.raises(ValueError, match="沙箱"):
        materialize_sandbox_files(root, {"link/secret.txt": "CANARY-OUTSIDE"})

    assert list(outside.iterdir()) == []


def test_materialize_does_not_follow_a_leaf_symlink(tmp_path: Path) -> None:
    root = tmp_path / "box"
    outside = tmp_path / "outside.txt"
    root.mkdir()
    outside.write_text("KEEP\n", encoding="utf-8")
    (root / "leaf").symlink_to(outside)

    with pytest.raises(ValueError, match="沙箱"):
        materialize_sandbox_files(root, {"leaf": "CANARY-OUTSIDE"})

    assert outside.read_text(encoding="utf-8") == "KEEP\n"
    assert (root / "leaf").is_symlink()


def test_sandbox_destination_rejects_a_symlink_parent(tmp_path: Path) -> None:
    root = tmp_path / "box"
    outside = tmp_path / "outside"
    root.mkdir()
    outside.mkdir()
    (root / "link").symlink_to(outside, target_is_directory=True)

    with pytest.raises(ValueError, match="沙箱"):
        sandbox_destination(root, "link/secret.txt")

    assert list(outside.iterdir()) == []


def test_hidden_pytest_timeout_kills_the_group_and_keeps_output(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    import subprocess

    killed: dict[str, int] = {}

    class _Proc:
        def __init__(self, *_args: object, **_kwargs: object) -> None:
            self.pid = 99
            self.returncode = None
            self._calls = 0

        def communicate(self, timeout: float | None = None) -> tuple[bytes, bytes]:
            self._calls += 1
            if self._calls == 1:
                raise subprocess.TimeoutExpired("pytest", timeout or 1)
            return b"partial-out", b"partial-err"

        def poll(self) -> int | None:
            return None

        def kill(self) -> None:
            self.returncode = -9

    monkeypatch.setattr("tiancode.eval.runner.subprocess.Popen", _Proc)
    monkeypatch.setattr(
        "tiancode.tools.execution.kill_process_tree",
        lambda proc: killed.setdefault("pid", proc.pid),
    )

    code, stdout, stderr, timed_out = run_hidden_pytest(tmp_path, 1)

    assert timed_out is True
    assert code == -1
    assert "partial-out" in stdout
    assert "partial-err" in stderr
    assert killed["pid"] == 99


def test_eval_timeout_is_a_failed_result(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(
        "tiancode.eval.runner.run_hidden_pytest",
        lambda *_args: (-1, "partial-out", "partial-err", True),
    )
    problem = BenchmarkProblem(
        id="slow",
        title="slow",
        description="hang",
        starter_files={"pkg/a.py": "VALUE = 1\n"},
        test_files={"test_a.py": "def test_ok():\n    assert True\n"},
        timeout_seconds=1,
    )

    result = BenchmarkRunner(
        settings=Settings(api_key="sk-test", work_dir=tmp_path, auto_heal=False)
    ).run_single_problem(problem, solve_fn=lambda _path: None)

    assert result.success is False
    assert "超时" in (result.error_message or "")
    assert "partial-out" in (result.error_message or "")
    assert "partial-err" in (result.error_message or "")
