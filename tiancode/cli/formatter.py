"""Terminal formatting and ANSI styling for TianCode CLI."""

import sys
from typing import List
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
