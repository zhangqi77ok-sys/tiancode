"""Evaluation harness and sandbox runner for benchmark problems."""

import os
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional

from tiancode.config.settings import Settings
from tiancode.core.engine import TianCodeEngine
from tiancode.eval.challenges import get_all_challenges, get_challenge
from tiancode.eval.models import BenchmarkProblem, EvalReport, EvalResult


class AgentTurnTracker:
    """Accurately tracks agent tool execution cycles from stream_task events."""

    def __init__(self, max_turns: int = 5) -> None:
        self.max_turns = max_turns
        self.tool_turns = 0
        self.has_token_output = False

    def observe(self, event: Dict[str, Any]) -> bool:
        """Record an event and return True if execution should terminate."""
        if not isinstance(event, dict):
            return False

        event_type = event.get("type")
        if event_type == "token":
            self.has_token_output = True
        elif event_type == "tool_end":
            self.tool_turns += 1
            if self.tool_turns >= self.max_turns:
                return True
        return False

    @property
    def final_turn_count(self) -> int:
        """Return the actual turn count (at least 1 if tokens were emitted, otherwise tool_turns)."""
        return max(self.tool_turns, 1 if self.has_token_output else 0)


_NOFOLLOW = getattr(os, "O_NOFOLLOW", 0)
_DIRECTORY = getattr(os, "O_DIRECTORY", 0)
_CLOEXEC = getattr(os, "O_CLOEXEC", 0)


def _sandbox_error(rel_path: str) -> ValueError:
    return ValueError(f"评测文件路径越出沙箱: {rel_path}")


def _sandbox_parts(rel_path: str) -> tuple[str, ...]:
    raw = Path(rel_path)
    if (
        not str(rel_path).strip()
        or "\x00" in str(rel_path)
        or raw.is_absolute()
        or any(part in ("", ".", "..") for part in raw.parts)
    ):
        raise _sandbox_error(rel_path)
    return raw.parts


def sandbox_destination(sandbox: Path, rel_path: str) -> Path:
    """Return a file path inside the sandbox.

    Absolute paths, `..`, and a symlink at any component are refused. The
    destination is not created and a link target is not opened. Writing uses
    `materialize_sandbox_files`, which does not trust this check across a race.
    """
    root = sandbox.resolve()
    parts = _sandbox_parts(rel_path)
    current = root
    for part in parts[:-1]:
        current = current / part
        if current.is_symlink():
            raise _sandbox_error(rel_path)
        try:
            current.resolve().relative_to(root)
        except ValueError as exc:
            raise _sandbox_error(rel_path) from exc
    leaf = current / parts[-1]
    if leaf.is_symlink():
        raise _sandbox_error(rel_path)
    return leaf


def _enter_sandbox_dir(parent_fd: int, name: str, rel_path: str) -> int:
    """Open a child directory of parent_fd. A symlink is not followed."""
    flags = os.O_RDONLY | _DIRECTORY | _NOFOLLOW | _CLOEXEC
    try:
        return os.open(name, flags, dir_fd=parent_fd)
    except FileNotFoundError:
        pass
    except OSError as exc:
        raise _sandbox_error(rel_path) from exc
    try:
        os.mkdir(name, 0o755, dir_fd=parent_fd)
    except FileExistsError:
        pass
    except OSError as exc:
        raise _sandbox_error(rel_path) from exc
    try:
        return os.open(name, flags, dir_fd=parent_fd)
    except OSError as exc:
        raise _sandbox_error(rel_path) from exc


def _create_sandbox_file(dir_fd: int, name: str, content: str, rel_path: str) -> None:
    flags = os.O_WRONLY | os.O_CREAT | os.O_TRUNC | _NOFOLLOW | _CLOEXEC
    try:
        fd = os.open(name, flags, 0o644, dir_fd=dir_fd)
    except OSError as exc:
        raise _sandbox_error(rel_path) from exc
    try:
        payload = content.encode("utf-8")
        view = memoryview(payload)
        while view:
            written = os.write(fd, view)
            if written <= 0:
                raise _sandbox_error(rel_path)
            view = view[written:]
        os.fsync(fd)
    except OSError as exc:
        raise _sandbox_error(rel_path) from exc
    finally:
        os.close(fd)


def _write_sandbox_file(root_fd: int, rel_path: str, content: str) -> None:
    """Create `rel_path` under root_fd. Each step uses O_NOFOLLOW.

    Holding the directory fd means a parent swapped for a symlink after the
    path check cannot redirect the write. The leaf open fails if that name
    is itself a symlink.
    """
    parts = _sandbox_parts(rel_path)
    current = os.dup(root_fd)
    try:
        for part in parts[:-1]:
            nxt = _enter_sandbox_dir(current, part, rel_path)
            os.close(current)
            current = nxt
        _create_sandbox_file(current, parts[-1], content, rel_path)
    finally:
        os.close(current)


def materialize_sandbox_files(sandbox: Path, files: Dict[str, str]) -> None:
    """Write benchmark files through directory fds. Do not follow a symlink."""
    if not files:
        return
    root = sandbox.resolve()
    try:
        root_fd = os.open(root, os.O_RDONLY | _DIRECTORY | _CLOEXEC)
    except OSError as exc:
        raise ValueError(f"评测沙箱不可用: {root}") from exc
    try:
        for rel_path, content in files.items():
            _write_sandbox_file(root_fd, rel_path, content)
    finally:
        os.close(root_fd)


def _hidden_pytest_command(sandbox_path: Path) -> List[str]:
    return [
        sys.executable,
        "-m",
        "pytest",
        "-q",
        "-p",
        "no:cacheprovider",
        "--noconftest",
        "--confcutdir",
        str(sandbox_path),
        "--rootdir",
        str(sandbox_path),
        "--override-ini",
        "testpaths=.",
        ".",
    ]


def _process_text(raw: bytes | str | None) -> str:
    if raw is None:
        return ""
    if isinstance(raw, str):
        return raw
    return raw.decode("utf-8", errors="replace")


def run_hidden_pytest(sandbox_path: Path, timeout_seconds: int) -> tuple[int, str, str, bool]:
    """Run sandbox tests. On timeout, kill the process group and keep partial output.

    The return is `(exit_code, stdout, stderr, timed_out)`. A timeout is not
    raised: one hung problem must not abort the rest of the suite.
    """
    proc = subprocess.Popen(
        _hidden_pytest_command(sandbox_path),
        cwd=str(sandbox_path),
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        start_new_session=(sys.platform != "win32"),
    )
    try:
        raw_out, raw_err = proc.communicate(timeout=timeout_seconds)
        code = proc.returncode if proc.returncode is not None else 1
        return code, _process_text(raw_out), _process_text(raw_err), False
    except subprocess.TimeoutExpired:
        from tiancode.tools.execution import kill_process_tree

        kill_process_tree(proc)
        try:
            raw_out, raw_err = proc.communicate(timeout=2)
        except Exception:
            raw_out, raw_err = b"", b""
        return -1, _process_text(raw_out), _process_text(raw_err), True


class BenchmarkRunner:
    """Executes benchmark problems within isolated environments."""

    def __init__(
        self,
        engine: Optional[TianCodeEngine] = None,
        settings: Optional[Settings] = None,
    ) -> None:
        self.engine = engine
        self.settings = settings or (engine.settings if engine else Settings.load())

    def run_single_problem(
        self,
        problem: BenchmarkProblem,
        solve_fn: Optional[Callable[[Path], None]] = None,
    ) -> EvalResult:
        """Run a single problem inside a sandboxed workspace and record metrics."""
        start_time = time.time()
        turn_count = 0
        diff_summary = ""

        with tempfile.TemporaryDirectory() as tmpdir:
            sandbox_path = Path(tmpdir).resolve()

            # 1. Materialize starter files inside the sandbox only.
            try:
                materialize_sandbox_files(sandbox_path, problem.starter_files)
            except ValueError as exc:
                return EvalResult(
                    problem_id=problem.id,
                    title=problem.title,
                    success=False,
                    duration_seconds=time.time() - start_time,
                    turn_count=0,
                    error_message=str(exc),
                )

            # 2. Initialize Git repo in sandbox for clean tracking
            from tiancode.core.git_store import git_workspace_env

            git_env = git_workspace_env()
            subprocess.run(["git", "init"], cwd=str(sandbox_path), env=git_env, capture_output=True, check=False)
            subprocess.run(["git", "config", "user.name", "TianCode Eval"], cwd=str(sandbox_path), env=git_env, capture_output=True, check=False)
            subprocess.run(["git", "config", "user.email", "eval@tiancode.dev"], cwd=str(sandbox_path), env=git_env, capture_output=True, check=False)
            subprocess.run(["git", "add", "-A"], cwd=str(sandbox_path), env=git_env, capture_output=True, check=False)
            subprocess.run(["git", "commit", "-m", "starter code"], cwd=str(sandbox_path), env=git_env, capture_output=True, check=False)

            # 3. Autonomous Execution: solve_fn or TianCodeEngine
            sandbox_engine: Optional[TianCodeEngine] = None
            try:
                if solve_fn is not None:
                    solve_fn(sandbox_path)
                    turn_count = 1
                else:
                    # Spawn isolated engine for this sandbox with auto_heal disabled.
                    # Sessions and memory stay inside the sandbox so a benchmark
                    # cannot overwrite the operator's real history.
                    eval_settings = Settings(
                        api_key=self.settings.api_key,
                        base_url=self.settings.base_url,
                        model=self.settings.model,
                        work_dir=sandbox_path,
                        auto_heal=False,
                    )
                    sandbox_engine = TianCodeEngine(settings=eval_settings)
                    self._isolate_eval_runtime(sandbox_engine, sandbox_path)
                    tracker = AgentTurnTracker(max_turns=problem.max_turns)

                    try:
                        for step in sandbox_engine.stream_task(problem.description, thread_id=f"eval_{problem.id}"):
                            if tracker.observe(step):
                                break
                        turn_count = tracker.final_turn_count
                    except Exception as exc:
                        duration = time.time() - start_time
                        return EvalResult(
                            problem_id=problem.id,
                            title=problem.title,
                            success=False,
                            duration_seconds=duration,
                            turn_count=tracker.final_turn_count,
                            error_message=f"Agent runtime exception: {str(exc)}",
                        )

                # 4. Capture Diff Summary
                diff_proc = subprocess.run(
                    ["git", "diff", "HEAD"],
                    cwd=str(sandbox_path),
                    env=git_env,
                    capture_output=True,
                    text=True,
                    encoding="utf-8",
                    errors="replace",
                )
                diff_summary = diff_proc.stdout.strip()

                # 5. Materialize hidden test files inside the sandbox only.
                try:
                    materialize_sandbox_files(sandbox_path, problem.test_files)
                except ValueError as exc:
                    return EvalResult(
                        problem_id=problem.id,
                        title=problem.title,
                        success=False,
                        duration_seconds=time.time() - start_time,
                        turn_count=turn_count,
                        diff_summary=diff_summary,
                        error_message=str(exc),
                    )

                # 6. Execute hidden test suite. A timeout becomes a failed result.
                code, stdout, stderr, timed_out = run_hidden_pytest(sandbox_path, problem.timeout_seconds)
                duration = time.time() - start_time
                if timed_out:
                    note = f"隐藏测试超时（超过 {problem.timeout_seconds} 秒被强制终止）"
                    tail = f"{stdout}\n{stderr}".strip()
                    return EvalResult(
                        problem_id=problem.id,
                        title=problem.title,
                        success=False,
                        duration_seconds=duration,
                        turn_count=turn_count,
                        diff_summary=diff_summary,
                        error_message=f"{note}\n{tail}".strip(),
                    )
                is_success = code == 0
                error_msg = None if is_success else f"{stdout}\n{stderr}".strip()

                return EvalResult(
                    problem_id=problem.id,
                    title=problem.title,
                    success=is_success,
                    duration_seconds=duration,
                    turn_count=turn_count,
                    diff_summary=diff_summary,
                    error_message=error_msg,
                )
            finally:
                if sandbox_engine is not None:
                    sandbox_engine.close()

    @staticmethod
    def _isolate_eval_runtime(engine: TianCodeEngine, sandbox_path: Path) -> None:
        """Point session and memory writes at the sandbox, not the user profile."""
        from tiancode.core.cognitive_memory import CognitiveMemoryStore
        from tiancode.core.session_store import SessionStore

        engine.session_store = SessionStore(sessions_dir=sandbox_path / ".tiancode-eval-sessions")
        engine.memory_store = CognitiveMemoryStore(memory_path=sandbox_path / ".tiancode-eval-memory.json")

    def run_suite(self, problem_ids: Optional[List[str]] = None) -> EvalReport:
        """Run all or selected benchmark problems and return aggregate report."""
        if problem_ids:
            problems = [p for pid in problem_ids if (p := get_challenge(pid))]
        else:
            problems = get_all_challenges()

        results: List[EvalResult] = []
        for problem in problems:
            print(f"🚀 [Eval] 正在评测: {problem.id} - {problem.title} ...")
            res = self.run_single_problem(problem)
            status_tag = "✔ PASSED" if res.success else "✘ FAILED"
            print(f"   ↳ {status_tag} (耗时: {res.duration_seconds:.2f}s, 步数: {res.turn_count})")
            results.append(res)

        return EvalReport(results=results)
