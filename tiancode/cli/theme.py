"""Design system tokens, Rich theme, and enhanced Markdown renderers for TianCode CLI."""

import shutil
import sys
from typing import Dict, Optional, TextIO, Tuple

from rich import box
from rich.console import Console, ConsoleOptions, RenderResult
from rich.markdown import CodeBlock, Heading, Markdown
from rich.panel import Panel
from rich.rule import Rule
from rich.syntax import Syntax
from rich.table import Table
from rich.text import Text
from rich.theme import Theme

from tiancode import __version__

BRAND_COLOR = "#38BDF8"
ACCENT_COLOR = "#818CF8"
BORDER_MUTED = "#475569"
TEXT_PRIMARY = "#E5E7EB"
TEXT_MUTED = "#94A3B8"
TEXT_SUBTLE = "#64748B"
SUCCESS_COLOR = "#10B981"
WARNING_COLOR = "#F59E0B"
DANGER_COLOR = "#F43F5E"

PERMISSION_BADGES: Dict[str, Tuple[str, str, str]] = {
    "ask": ("🔐 ask", WARNING_COLOR, "交互确认"),
    "auto": ("⚡ auto", "#A855F7", "自动批准"),
    "read_only": ("🛡️ read_only", SUCCESS_COLOR, "只读审计"),
}

TIAN_THEME = Theme({
    "brand": f"bold {BRAND_COLOR}",
    "accent": f"bold {ACCENT_COLOR}",
    "muted": TEXT_MUTED,
    "subtle": TEXT_SUBTLE,
    "info": BRAND_COLOR,
    "success": SUCCESS_COLOR,
    "warning": WARNING_COLOR,
    "danger": f"bold {DANGER_COLOR}",
    "markdown.h1": f"bold {BRAND_COLOR}",
    "markdown.h2": "bold #7DD3FC",
    "markdown.h3": f"bold {TEXT_PRIMARY}",
    "markdown.h4": f"bold {TEXT_MUTED}",
    "markdown.code": "bold #7DD3FC",
    "markdown.item.bullet": BRAND_COLOR,
    "markdown.item.number": BRAND_COLOR,
    "markdown.hr": BORDER_MUTED,
    "markdown.block_quote": TEXT_MUTED,
})


class _LeftHeading(Heading):
    """Render all Markdown headings left-aligned with clean visual hierarchy."""

    LEVEL_ALIGN = {"h1": "left", "h2": "left", "h3": "left", "h4": "left", "h5": "left", "h6": "left"}

    def __rich_console__(self, console: Console, options: ConsoleOptions) -> RenderResult:
        prefix = "▌ " if self.tag in ("h1", "h2") else "• "
        styled = Text(prefix, style=f"bold {BRAND_COLOR}" if self.tag in ("h1", "h2") else TEXT_MUTED)
        styled.append_text(self.text)
        yield styled


class _CardCodeBlock(CodeBlock):
    """Render fenced code blocks inside subtle rounded cards with syntax highlighting."""

    def __rich_console__(self, console: Console, options: ConsoleOptions) -> RenderResult:
        code = str(self.text).rstrip()
        syntax = Syntax(
            code,
            self.lexer_name,
            theme=self.theme,
            word_wrap=True,
            background_color="default",
            padding=(0, 1),
        )
        title = f"[{TEXT_MUTED}]{self.lexer_name}[/]" if self.lexer_name else None
        yield Panel(
            syntax,
            box=box.ROUNDED,
            border_style=BORDER_MUTED,
            title=title,
            title_align="left",
            padding=(0, 0),
        )


class CleanMarkdown(Markdown):
    """Markdown renderer tailored for dark-mode AI terminal output."""

    elements = {
        **Markdown.elements,
        "heading_open": _LeftHeading,
        "fence": _CardCodeBlock,
        "code_block": _CardCodeBlock,
    }


def render_markdown(text: str) -> CleanMarkdown:
    """Create a Rich Markdown renderable with Monokai syntax highlighting."""
    return CleanMarkdown(text, code_theme="monokai", hyperlinks=False)


def get_console(stream: Optional[TextIO] = None) -> Console:
    """Build a Console dynamically bound to active stdout for TTY and pytest capture compatibility."""
    target = stream or sys.stdout
    is_tty = bool(getattr(target, "isatty", lambda: False)())
    width = None if is_tty else max(shutil.get_terminal_size((120, 30)).columns, 110)
    return Console(
        file=target,
        theme=TIAN_THEME,
        highlight=False,
        soft_wrap=True,
        width=width,
        legacy_windows=False,
    )


def build_banner_panel(
    work_dir: str,
    model: str,
    permission_mode: str,
    auto_heal: bool,
    timeout_seconds: int,
) -> Panel:
    """Construct rounded Bento welcome card with model and permission badges."""
    badge_label, badge_color, badge_desc = PERMISSION_BADGES.get(
        permission_mode, ("🔐 ask", WARNING_COLOR, "交互确认")
    )
    heal_line = Text.assemble(
        ("● 开启" if auto_heal else "○ 关闭", SUCCESS_COLOR if auto_heal else TEXT_MUTED),
        (f" ({timeout_seconds}s)" if auto_heal else "", TEXT_SUBTLE),
    )

    grid = Table.grid(expand=True, padding=(0, 2))
    grid.add_column(style=TEXT_MUTED, no_wrap=True)
    grid.add_column(ratio=1)
    grid.add_column(style=TEXT_MUTED, no_wrap=True)
    grid.add_column(justify="right")
    grid.add_row(
        "工作目录",
        Text(work_dir, style=TEXT_PRIMARY),
        "安全模式",
        Text.assemble((badge_label, badge_color), (f" ({badge_desc})", TEXT_SUBTLE)),
    )
    grid.add_row("推理引擎", Text(model, style=f"bold {BRAND_COLOR}"), "写后自愈", heal_line)

    shortcuts = Text(
        "/mode 权限   /doctor 体检   /context 上下文   /diff 变更   /undo 回滚   /cost 费用   /help 帮助",
        style=TEXT_SUBTLE,
    )
    wrapper = Table.grid(expand=True)
    wrapper.add_row(grid)
    wrapper.add_row(Rule(style=BORDER_MUTED))
    wrapper.add_row(shortcuts)

    return Panel(
        wrapper,
        title=f"[bold {BRAND_COLOR}]⚡ TianCode[/] [dim]v{__version__}[/]",
        title_align="left",
        box=box.ROUNDED,
        border_style=BORDER_MUTED,
        padding=(0, 2),
    )


def build_diff_panel(diff) -> Panel:
    """Construct rounded color-coded Diff card with change statistics."""
    body = Text()
    for idx, line in enumerate(diff.diff_text.splitlines()):
        if idx > 0:
            body.append("\n")
        if line.startswith("-") and not line.startswith("---"):
            body.append(f"  {line}", style=DANGER_COLOR)
        elif line.startswith("+") and not line.startswith("+++"):
            body.append(f"  {line}", style=SUCCESS_COLOR)
        elif line.startswith("@@"):
            body.append(f"  {line}", style=ACCENT_COLOR)
        else:
            body.append(f"  {line}", style=TEXT_MUTED)

    title = Text.assemble(
        (f"📄 {diff.filename}  ", f"bold {TEXT_PRIMARY}"),
        (f"+{diff.additions}", SUCCESS_COLOR),
        (" ", ""),
        (f"-{diff.deletions}", DANGER_COLOR),
    )
    return Panel(body, title=title, title_align="left", box=box.ROUNDED, border_style=BORDER_MUTED, padding=(0, 1))


def build_plan_panel(plan) -> Panel:
    """Construct rounded TaskPlan progress panel preserving step status glyphs."""
    from tiancode.domain.models import StepStatus

    lines = Text()
    for idx, s in enumerate(plan.steps):
        if idx > 0:
            lines.append("\n")
        if s.status == StepStatus.COMPLETED:
            lines.append("  [✓] ", style=f"bold {SUCCESS_COLOR}")
            lines.append(f"{s.step_id}. {s.title}", style=TEXT_PRIMARY)
            if s.result_summary:
                lines.append(f" ({s.result_summary})", style=SUCCESS_COLOR)
        elif s.status == StepStatus.IN_PROGRESS:
            lines.append("  [▶] ", style=f"bold {WARNING_COLOR}")
            lines.append(f"{s.step_id}. {s.title}", style=f"bold {TEXT_PRIMARY}")
            lines.append(" (进行中...)", style=WARNING_COLOR)
        elif s.status == StepStatus.FAILED:
            lines.append("  [✗] ", style=f"bold {DANGER_COLOR}")
            lines.append(f"{s.step_id}. {s.title}", style=DANGER_COLOR)
            lines.append(f" (失败: {s.result_summary})" if s.result_summary else " (失败)", style=DANGER_COLOR)
        else:
            lines.append("  [ ] ", style=TEXT_SUBTLE)
            lines.append(f"{s.step_id}. {s.title}", style=TEXT_MUTED)

    return Panel(
        lines,
        title=Text(f"📋 TianCode 任务执行看板: {plan.task_title}", style=f"bold {BRAND_COLOR}"),
        title_align="left",
        box=box.ROUNDED,
        border_style=ACCENT_COLOR,
        padding=(0, 1),
    )

