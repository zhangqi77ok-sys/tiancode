"""Evaluation harness and sandbox runner for benchmark problems."""

import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import Callable, List, Optional

from tiancode.config.settings import Settings
from tiancode.core.engine import TianCodeEngine
from tiancode.eval.challenges import get_all_challenges, get_challenge
from tiancode.eval.models import BenchmarkProblem, EvalReport, EvalResult


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
            subprocess.run(["git", "init"], cwd=str(sandbox_path), capture_output=True, check=False)
            subprocess.run(["git", "config", "user.name", "TianCode Eval"], cwd=str(sandbox_path), capture_output=True, check=False)
            subprocess.run(["git", "config", "user.email", "eval@tiancode.dev"], cwd=str(sandbox_path), capture_output=True, check=False)
            subprocess.run(["git", "add", "-A"], cwd=str(sandbox_path), capture_output=True, check=False)
            subprocess.run(["git", "commit", "-m", "starter code"], cwd=str(sandbox_path), capture_output=True, check=False)

            # 3. Autonomous Execution: solve_fn or TianCodeEngine
            if solve_fn is not None:
                solve_fn(sandbox_path)
                turn_count = 1
            else:
                # Spawn isolated engine for this sandbox
                eval_settings = Settings(
                    api_key=self.settings.api_key,
                    base_url=self.settings.base_url,
                    model=self.settings.model,
                    work_dir=sandbox_path,
                )
                sandbox_engine = TianCodeEngine(settings=eval_settings)

                try:
                    for step in sandbox_engine.stream_task(problem.description, thread_id=f"eval_{problem.id}"):
                        turn_count += 1
                        if turn_count >= problem.max_turns * 3:
                            break
                except Exception as exc:
                    duration = time.time() - start_time
                    return EvalResult(
                        problem_id=problem.id,
                        title=problem.title,
                        success=False,
                        duration_seconds=duration,
                        turn_count=turn_count,
                        error_message=f"Agent runtime exception: {str(exc)}",
                    )

            # 4. Capture Diff Summary
            diff_proc = subprocess.run(
                ["git", "diff", "HEAD"],
                cwd=str(sandbox_path),
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
                [sys.executable, "-m", "pytest", "-q"],
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
