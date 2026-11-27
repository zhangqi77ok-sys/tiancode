"""Evaluation harness and sandbox runner for benchmark problems."""

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

            # 1. Materialize starter files
            for rel_path, content in problem.starter_files.items():
                dest = sandbox_path / rel_path
                dest.parent.mkdir(parents=True, exist_ok=True)
                dest.write_text(content, encoding="utf-8")

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

                # 5. Materialize hidden test files
                for rel_path, content in problem.test_files.items():
                    test_dest = sandbox_path / rel_path
                    test_dest.parent.mkdir(parents=True, exist_ok=True)
                    test_dest.write_text(content, encoding="utf-8")

                # 6. Execute hidden test suite using pytest or unittest
                test_proc = subprocess.run(
                    [
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
                    ],
                    cwd=str(sandbox_path),
                    capture_output=True,
                    text=True,
                    encoding="utf-8",
                    errors="replace",
                    timeout=problem.timeout_seconds,
                )

                duration = time.time() - start_time
                is_success = (test_proc.returncode == 0)
                error_msg = None if is_success else (test_proc.stdout + "\n" + test_proc.stderr).strip()

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
