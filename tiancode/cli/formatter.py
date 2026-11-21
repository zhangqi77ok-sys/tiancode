"""Terminal UI formatting, Rich streaming thinking/Markdown renderer."""

import time
from typing import Any, Dict, Iterable, List, Optional

from rich import box
from rich.live import Live
from rich.padding import Padding
from rich.panel import Panel
from rich.rule import Rule
from rich.text import Text

from tiancode.cli.theme import (
    BORDER_MUTED, BRAND_COLOR, SUCCESS_COLOR, TEXT_MUTED, TEXT_PRIMARY, TEXT_SUBTLE, WARNING_COLOR,
    build_banner_panel, build_diff_panel, build_plan_panel, get_console, render_markdown,
)
from tiancode.cli.widgets import LiveToolSpinner
from tiancode.domain.models import FileDiff

MAX_VISIBLE_THINK_LINES = 6


def _build_thinking_panel(full_thought: str, elapsed: float) -> Panel:
    """Build compact 6-line scrolling preview card for active model reasoning."""
    raw_lines = [ln for ln in full_thought.strip().splitlines() if ln.strip()] or ["..."]
    tail = raw_lines[-MAX_VISIBLE_THINK_LINES:]
    prefix = f"… (已前略 {len(raw_lines) - len(tail)} 行)\n" if len(raw_lines) > len(tail) else ""
    return Panel(
        Text(prefix + "\n".join(tail), style=TEXT_MUTED),
        title=Text(f"💭 深度思考中 · {elapsed:.1f}s", style=TEXT_SUBTLE),
        title_align="left", box=box.ROUNDED, border_style=BORDER_MUTED, padding=(0, 1),
    )


class TerminalFormatter:
    """Modern dark-mode terminal UI kit with graceful plain fallback under capture/pipes."""

    RESET, BOLD = "\033[0m", "\033[1m"
    GREEN, RED, YELLOW, BLUE, CYAN = "\033[92m", "\033[91m", "\033[93m", "\033[94m", "\033[96m"

    @staticmethod
    def _print_spaced(renderable: Any) -> None:
        console = get_console()
        console.print()
        console.print(renderable)
        console.print()

    @classmethod
    def print_banner(cls, work_dir: str, model: str, permission_mode: str = "ask", auto_heal: bool = True, timeout_seconds: int = 15) -> None:
        """Display rounded Bento welcome card with environment and mode badges."""
        cls._print_spaced(build_banner_panel(work_dir, model, permission_mode, auto_heal, timeout_seconds))

    @classmethod
    def print_user_turn(cls, prompt_text: str) -> None:
        """Print clear visual divider for a user turn."""
        get_console().print(Text.assemble(("\n❯ ", f"bold {BRAND_COLOR}"), (prompt_text, f"bold {TEXT_PRIMARY}")))

    @classmethod
    def print_diff(cls, diff: FileDiff) -> None:
        """Render a unified diff card with color-coded lines and change badges."""
        if not diff.is_empty:
            cls._print_spaced(build_diff_panel(diff))

    @classmethod
    def print_diffs(cls, diffs: List[FileDiff]) -> None:
        """Render multiple file diffs cleanly."""
        non_empty = [d for d in diffs if not d.is_empty]
        if not non_empty:
            cls.print_info("当前没有检测到任何代码文件修改。")
            return
        for d in non_empty:
            cls.print_diff(d)

    @classmethod
    def print_agent_message(cls, content: str) -> None:
        """Print assistant Markdown message with header divider."""
        console = get_console()
        console.print()
        console.print(Rule(f"[bold {BRAND_COLOR}]◆ TianCode[/]", align="left", style=BORDER_MUTED))
        console.print(Padding(render_markdown(content), (0, 2)))

    @classmethod
    def print_info(cls, message: str) -> None:
        get_console().print(Text.assemble(("● ", BRAND_COLOR), (message, TEXT_MUTED)))

    @classmethod
    def print_success(cls, message: str) -> None:
        get_console().print(Text.assemble(("✔ ", f"bold {SUCCESS_COLOR}"), (message, TEXT_PRIMARY)))

    @classmethod
    def print_warning(cls, message: str) -> None:
        get_console().print(Text.assemble(("▲ ", f"bold {WARNING_COLOR}"), (message, WARNING_COLOR)))

    @classmethod
    def print_task_plan(cls, plan) -> None:
        """Render the dynamic task execution checklist inside a rounded card."""
        cls._print_spaced(build_plan_panel(plan))

    @classmethod
    def _dispatch_stream_event(cls, state: "_StreamRenderState", event: Dict[str, Any]) -> None:
        """Route a single LangGraph stream event with single-Live mutual exclusion."""
        ev_type = event.get("type")
        if ev_type == "thinking" and event.get("content"):
            state.stop_spinner()
            state.flush_markdown()
            state.append_thinking(str(event["content"]))
            return

        if ev_type == "token" and event.get("content"):
            text = str(event["content"])
            if not state.md_chunks and not text.strip():
                return
            state.stop_spinner()
            state.flush_thinking()
            state.append_token(text)
            return

        if ev_type == "tool_start":
            state.flush_thinking()
            state.flush_markdown()
            state.stop_spinner()
            name = str(event.get("name", "tool"))
            if state.use_spinner:
                state.spinner = LiveToolSpinner(tool_name=name, args=str(event.get("args", "")))
                state.spinner.start()
            else:
                state.console.print(Text.assemble(("  ▸ ", BRAND_COLOR), (f"调用工具: {name}", TEXT_MUTED)))
            return

        if ev_type == "tool_end":
            state.stop_spinner()
            state.flush_thinking()
            state.flush_markdown()
            lines = str(event.get("content", "")).strip().splitlines()
            if lines:
                state.console.print(Text(f"    │ ↳ {lines[0][:120]}", style=TEXT_SUBTLE))
        elif ev_type == "usage_summary":
            state.usage_ev = event
        elif "brain" in event:
            msg = event["brain"]["messages"][-1]
            if getattr(msg, "content", None):
                cls.print_agent_message(msg.content)

    @classmethod
    def render_task_stream(cls, events: Iterable[Dict[str, Any]], *, use_spinner: bool = False) -> None:
        """Stream live thinking, Markdown tokens, tool timeline, and low-noise usage footer."""
        t0 = time.perf_counter()
        state = _StreamRenderState(use_spinner=use_spinner, console=get_console())
        try:
            for event in events:
                if isinstance(event, dict):
                    cls._dispatch_stream_event(state, event)
        finally:
            closer = getattr(events, "close", None)
            if closer is not None:
                closer()
            state.stop_spinner()
            state.flush_thinking()
            state.flush_markdown()

        if state.usage_ev:
            elapsed = time.perf_counter() - t0
            ev = state.usage_ev
            footer = (
                f"  ┄ 耗时 {elapsed:.1f}s · 本轮: {ev.get('turn_tokens', 0):,} Tokens · "
                f"累计: {ev.get('session_tokens', 0):,} Tokens (¥{ev.get('session_cost', 0.0):.4f}) ┄"
            )
            state.console.print(Text(footer, style=TEXT_SUBTLE))


class _StreamRenderState:
    """Mutable render state managing collapsible thinking panel and Live Markdown."""

    def __init__(self, *, use_spinner: bool, console) -> None:
        self.use_spinner = use_spinner
        self.console = console
        self.spinner: Optional["LiveToolSpinner"] = None
        self.think_chunks: List[str] = []
        self.think_live: Optional[Live] = None
        self.think_t0: float = 0.0
        self.md_chunks: List[str] = []
        self.live: Optional[Live] = None
        self.header_printed = False
        self.usage_ev: Optional[Dict[str, Any]] = None

    def _ensure_header(self) -> None:
        if not self.header_printed:
            self.console.print()
            self.console.print(Rule(f"[bold {BRAND_COLOR}]◆ TianCode[/]", align="left", style=BORDER_MUTED))
            self.header_printed = True

    def append_thinking(self, chunk: str) -> None:
        self._ensure_header()
        if not self.think_chunks:
            self.think_t0 = time.perf_counter()
        self.think_chunks.append(chunk)
        if self.console.is_terminal:
            panel = Padding(_build_thinking_panel("".join(self.think_chunks), time.perf_counter() - self.think_t0), (0, 2))
            if self.think_live is None:
                self.think_live = Live(panel, console=self.console, refresh_per_second=10, transient=True)
                self.think_live.start()
            else:
                self.think_live.update(panel)

    def flush_thinking(self) -> None:
        if not self.think_chunks and self.think_live is None:
            return
        elapsed = max(0.1, time.perf_counter() - self.think_t0) if self.think_t0 else 0.1
        char_count = sum(len(c) for c in self.think_chunks)
        if self.think_live is not None:
            try:
                self.think_live.stop()
            finally:
                self.think_live = None
        self.think_chunks.clear()
        self.think_t0 = 0.0
        if char_count > 0:
            self.console.print(Text(f"  💭 已完成深度思考 (耗时 {elapsed:.1f}s · {char_count:,} 字)", style=TEXT_SUBTLE))

    def append_token(self, chunk: str) -> None:
        self._ensure_header()
        self.md_chunks.append(chunk)
        full_md = "".join(self.md_chunks)
        if self.console.is_terminal:
            if self.live is None:
                self.live = Live(Padding(render_markdown(full_md), (0, 2)), console=self.console, refresh_per_second=12, vertical_overflow="visible")
                self.live.start()
            else:
                self.live.update(Padding(render_markdown(full_md), (0, 2)))

    def flush_markdown(self) -> None:
        if self.live is not None:
            try:
                self.live.stop()
            finally:
                self.live = None
            self.md_chunks.clear()
            self.header_printed = False
            return
        if self.md_chunks:
            text = "".join(self.md_chunks)
            self.md_chunks.clear()
            self.header_printed = False
            self.console.print(Padding(render_markdown(text), (0, 2)))

    def stop_spinner(self) -> None:
        if self.spinner:
            self.spinner.stop()
            self.spinner = None
