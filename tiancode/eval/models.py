"""Domain models for agentic evaluation benchmarks."""

from dataclasses import dataclass, field
from typing import Dict, List, Optional


@dataclass
class BenchmarkProblem:
    """Represents a standardized coding benchmark problem."""

    id: str
    title: str
    description: str
    starter_files: Dict[str, str]
    test_files: Dict[str, str]
    timeout_seconds: int = 60
    max_turns: int = 5


@dataclass
class EvalResult:
    """Outcome of running an evaluation against a single problem."""

    problem_id: str
    title: str
    success: bool
    duration_seconds: float = 0.0
    turn_count: int = 0
    diff_summary: str = ""
    error_message: Optional[str] = None


@dataclass
class EvalReport:
    """Aggregated scorecard of a benchmark evaluation run."""

    results: List[EvalResult] = field(default_factory=list)

    @property
    def total(self) -> int:
        """Total number of evaluated problems."""
        return len(self.results)

    @property
    def passed(self) -> int:
        """Total number of problems successfully solved."""
        return sum(1 for r in self.results if r.success)

    @property
    def pass_rate(self) -> float:
        """Pass@1 percentage."""
        return (self.passed / self.total * 100.0) if self.total > 0 else 0.0

    def format_table(self) -> str:
        """Render a clean terminal scorecard table."""
        header = f"{'ID':<22} | {'标题':<24} | {'状态':<10} | {'轮次':<6} | {'耗时':<8}"
        divider = "-" * len(header)
        lines = [
            "",
            "📊 ====== [TianCode 本地微型基准评测战报 (Micro Benchmark)] ======",
            divider,
            header,
            divider,
        ]

        for res in self.results:
            status = "✔ PASSED" if res.success else "✘ FAILED"
            lines.append(
                f"{res.problem_id:<22} | {res.title:<24} | {status:<10} | {res.turn_count:<6} | {res.duration_seconds:>5.2f}s"
            )

        lines.extend([
            divider,
            f"📈 评测汇总: 总计 {self.total} 题 | 通过 {self.passed} 题 | Pass@1: {self.pass_rate:.1f}%",
            "==================================================",
            "",
        ])
        return "\n".join(lines)
