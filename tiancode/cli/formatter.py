"""Terminal formatting, ANSI styling, and animated tool execution spinners for TianCode CLI."""

import sys
import threading
import time
from typing import List, Optional, TextIO
from tiancode.domain.models import FileDiff


class TerminalFormatter:
    """Provides consistent ANSI coloring and UI panels."""

    RESET = "\033[0m"
    BOLD = "\033[1m"
    GREEN = "\033[92m"
    RED = "\033[91m"
    YELLOW = "\033[93m"
    BLUE = "\033[94m"
    CYAN = "\033[96m"

    @classmethod
    def print_banner(cls, work_dir: str, model: str) -> None:
        """Display the welcome banner."""
        line = "━" * 60
        print(f"\n{cls.CYAN}{cls.BOLD}⚡ TianCode AI 编程智能体就绪{cls.RESET}")
        print(f"{cls.BLUE}{line}{cls.RESET}")
        print(f" 工作目录 : {work_dir}")
        print(f" 底层模型 : {model}")
        print(f" 快捷指令 : [/diff 变动对比] [/map 全景图] [/eval 评测] [/undo 撤销] [/commit 提交] [/config 配置] [exit 退出]")
        print(f"{cls.BLUE}{line}{cls.RESET}\n")

    @classmethod
    def print_diff(cls, diff: FileDiff) -> None:
        """Render a unified diff with green additions and red deletions."""
        if diff.is_empty:
            return
        header = f"📄 变更对比: {diff.filename} (+{diff.additions} / -{diff.deletions})"
        print(f"\n{cls.BOLD}{header}{cls.RESET}")
        print(f"{cls.YELLOW}{'─' * 50}{cls.RESET}")
        for line in diff.diff_text.splitlines():
            if line.startswith("-") and not line.startswith("---"):
                print(f"{cls.RED}{line}{cls.RESET}")
            elif line.startswith("+") and not line.startswith("+++"):
                print(f"{cls.GREEN}{line}{cls.RESET}")
            else:
                print(line)
        print(f"{cls.YELLOW}{'─' * 50}{cls.RESET}\n")

    @classmethod
    def print_diffs(cls, diffs: List[FileDiff]) -> None:
        """Render multiple file diffs."""
        non_empty = [d for d in diffs if not d.is_empty]
        if not non_empty:
            print(f"{cls.YELLOW}💡 当前没有检测到任何代码文件修改。{cls.RESET}")
            return
        for d in non_empty:
            cls.print_diff(d)

    @classmethod
    def print_agent_message(cls, content: str) -> None:
        """Print assistant message."""
        print(f"\n{cls.CYAN}{cls.BOLD}🤖 TianCode:{cls.RESET} {content}")

    @classmethod
    def print_info(cls, message: str) -> None:
        """Print informational status."""
        print(f"{cls.BLUE}ℹ️ {message}{cls.RESET}")

    @classmethod
    def print_success(cls, message: str) -> None:
        """Print success status."""
        print(f"{cls.GREEN}✅ {message}{cls.RESET}")

    @classmethod
    def print_warning(cls, message: str) -> None:
        """Print warning status."""
        print(f"{cls.YELLOW}⚠️ {message}{cls.RESET}")

    @classmethod
    def print_task_plan(cls, plan) -> None:
        """Render the dynamic task execution checklist."""
        from tiancode.domain.models import StepStatus

        line = "═" * 55
        print(f"\n{cls.CYAN}{cls.BOLD}📋 {line}{cls.RESET}")
        print(f"{cls.BOLD}  TianCode 任务执行看板: {plan.task_title}{cls.RESET}")
        print(f"{cls.CYAN}{line}{cls.RESET}")
        for s in plan.steps:
            if s.status == StepStatus.COMPLETED:
                icon = f"{cls.GREEN}[✓]{cls.RESET}"
                suffix = f" {cls.GREEN}(已完成){cls.RESET}" if s.result_summary else ""
            elif s.status == StepStatus.IN_PROGRESS:
                icon = f"{cls.YELLOW}{cls.BOLD}[▶]{cls.RESET}"
                suffix = f" {cls.YELLOW}(进行中...){cls.RESET}"
            elif s.status == StepStatus.FAILED:
                icon = f"{cls.RED}[✗]{cls.RESET}"
                suffix = f" {cls.RED}(失败: {s.result_summary}){cls.RESET}" if s.result_summary else f" {cls.RED}(失败){cls.RESET}"
            else:
                icon = "[ ]"
                suffix = ""
            print(f"  {icon} {s.step_id}. {s.title}{suffix}")
        print(f"{cls.CYAN}{line}{cls.RESET}\n")


class LiveToolSpinner:
    """Animated Braille spinner with elapsed seconds and in-place terminal updates."""

    SPINNER_FRAMES = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"]

    def __init__(
        self,
        tool_name: str,
        args: str = "",
        stream: Optional[TextIO] = None,
    ) -> None:
        self.tool_name = tool_name
        self.args = str(args)[:60].replace("\n", " ") if args else ""
        self.stream = stream or sys.stdout
        self.is_running = False
        self._thread: Optional[threading.Thread] = None
        self._start_time: float = 0.0

    def start(self) -> None:
        """Start the spinner animation on a background daemon thread."""
        if self.is_running:
            return
        self.is_running = True
        self._start_time = time.time()
        self._thread = threading.Thread(target=self._spin_loop, daemon=True)
        self._thread.start()

    def _spin_loop(self) -> None:
        idx = 0
        arg_disp = f"({self.args})" if self.args else ""
        while self.is_running:
            elapsed = time.time() - self._start_time
            frame = self.SPINNER_FRAMES[idx % len(self.SPINNER_FRAMES)]
            msg = f"\r\033[K\033[96m{frame}\033[0m ⚙️  正在执行工具: \033[1m{self.tool_name}\033[0m{arg_disp} ... \033[93m[{elapsed:.1f}s]\033[0m"
            try:
                self.stream.write(msg)
                self.stream.flush()
            except Exception:
                pass
            idx += 1
            time.sleep(0.08)

    def stop(self, status_text: str = "") -> None:
        """Stop the spinner, clear line, and write final state."""
        if not self.is_running:
            return
        self.is_running = False
        if self._thread and self._thread.is_alive():
            self._thread.join(timeout=0.3)

        elapsed = time.time() - self._start_time
        final_msg = status_text or f"✔ ⚙️  工具执行完成: {self.tool_name} [{elapsed:.1f}s]"
        try:
            self.stream.write(f"\r\033[K\033[92m{final_msg}\033[0m\n")
            self.stream.flush()
        except Exception:
            pass
