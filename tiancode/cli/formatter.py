"""Terminal UI formatting, Rich streaming Markdown renderer, and live tool spinners."""

import sys
import threading
import time
from typing import Any, Dict, Iterable, List, Optional, TextIO

from rich.live import Live
from rich.padding import Padding
from rich.rule import Rule
from rich.text import Text

from tiancode.cli.theme import (
    BORDER_MUTED,
    BRAND_COLOR,
    SUCCESS_COLOR,
    TEXT_MUTED,
    TEXT_PRIMARY,
    TEXT_SUBTLE,
    WARNING_COLOR,
    build_banner_panel,
    build_diff_panel,
    build_plan_panel,
    get_console,
    render_markdown,
)
from tiancode.domain.models import FileDiff


class TerminalFormatter:
    """Modern dark-mode terminal UI kit with graceful plain fallback under capture/pipes."""

    RESET = "\033[0m"
    BOLD = "\033[1m"
    GREEN = "\033[92m"
    RED = "\033[91m"
    YELLOW = "\033[93m"
    BLUE = "\033[94m"
    CYAN = "\033[96m"

    @classmethod
    def print_banner(
        cls,
        work_dir: str,
        model: str,
        permission_mode: str = "ask",
        auto_heal: bool = True,
        timeout_seconds: int = 15,
    ) -> None:
        """Display rounded Bento welcome card with environment and mode badges."""
        console = get_console()
        console.print()
        console.print(build_banner_panel(work_dir, model, permission_mode, auto_heal, timeout_seconds))
        console.print()

    @classmethod
    def print_user_turn(cls, prompt_text: str) -> None:
        """Print clear visual divider for a user turn."""
        get_console().print(Text.assemble(("\n❯ ", f"bold {BRAND_COLOR}"), (prompt_text, f"bold {TEXT_PRIMARY}")))

    @classmethod
    def print_diff(cls, diff: FileDiff) -> None:
        """Render a unified diff card with color-coded lines and change badges."""
        if diff.is_empty:
            return
        console = get_console()
        console.print()
        console.print(build_diff_panel(diff))
        console.print()

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
        """Print low-noise informational status line."""
        get_console().print(Text.assemble(("● ", BRAND_COLOR), (message, TEXT_MUTED)))

    @classmethod
    def print_success(cls, message: str) -> None:
        """Print success status line."""
        get_console().print(Text.assemble(("✔ ", f"bold {SUCCESS_COLOR}"), (message, TEXT_PRIMARY)))

    @classmethod
    def print_warning(cls, message: str) -> None:
        """Print warning status line."""
        get_console().print(Text.assemble(("▲ ", f"bold {WARNING_COLOR}"), (message, WARNING_COLOR)))

    @classmethod
    def print_task_plan(cls, plan) -> None:
        """Render the dynamic task execution checklist inside a rounded card."""
        console = get_console()
        console.print()
        console.print(build_plan_panel(plan))
        console.print()

    @classmethod
    def _dispatch_stream_event(cls, state: "_StreamRenderState", event: Dict[str, Any]) -> None:
        """Route a single LangGraph stream event to Rich Live Markdown or tool timeline."""
        ev_type = event.get("type")
        if ev_type == "token" and event.get("content"):
            state.stop_spinner()
            state.append_token(str(event["content"]))
            return

        if ev_type == "tool_start":
            state.flush_markdown()
            state.stop_spinner()
            name = str(event.get("name", "tool"))
            if state.use_spinner:
                state.spinner = LiveToolSpinner(tool_name=name, args=str(event.get("args", "")))
                state.spinner.start()
            else:
                state.console.print(Text.assemble(("  ⚙ ", BRAND_COLOR), (f"调用工具: {name}", TEXT_MUTED)))
            return

        if ev_type == "tool_end":
            state.stop_spinner()
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
    def render_task_stream(
        cls,
        events: Iterable[Dict[str, Any]],
        *,
        use_spinner: bool = False,
    ) -> None:
        """Stream live Markdown tokens, tool timeline, and low-noise usage footer."""
        t0 = time.perf_counter()
        state = _StreamRenderState(use_spinner=use_spinner, console=get_console())
        try:
            for event in events:
                if isinstance(event, dict):
                    cls._dispatch_stream_event(state, event)
        finally:
            state.stop_spinner()
            state.flush_markdown()

        if state.usage_ev:
            elapsed = time.perf_counter() - t0
            ev = state.usage_ev
            footer = (
                f"  ┄ ⏱ 耗时 {elapsed:.1f}s · 本轮: {ev.get('turn_tokens', 0):,} Tokens · "
                f"累计: {ev.get('session_tokens', 0):,} Tokens (¥{ev.get('session_cost', 0.0):.4f}) ┄"
            )
            state.console.print(Text(footer, style=TEXT_SUBTLE))


class _StreamRenderState:
    """Mutable render state buffering Markdown tokens with Rich Live on interactive TTYs."""

    def __init__(self, *, use_spinner: bool, console) -> None:
        self.use_spinner = use_spinner
        self.console = console
        self.spinner: Optional["LiveToolSpinner"] = None
        self.md_chunks: List[str] = []
        self.live: Optional[Live] = None
        self.header_printed = False
        self.usage_ev: Optional[Dict[str, Any]] = None

    def append_token(self, chunk: str) -> None:
        if not self.header_printed:
            self.console.print()
            self.console.print(Rule(f"[bold {BRAND_COLOR}]◆ TianCode[/]", align="left", style=BORDER_MUTED))
            self.header_printed = True

        self.md_chunks.append(chunk)
        full_md = "".join(self.md_chunks)
        if self.console.is_terminal:
            if self.live is None:
                self.live = Live(
                    Padding(render_markdown(full_md), (0, 2)),
                    console=self.console,
                    refresh_per_second=12,
                    vertical_overflow="visible",
                )
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


class LiveToolSpinner:
    """Animated Braille spinner with elapsed seconds and clean single-line status."""

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
        """Start the spinner animation on a daemon thread."""
        if self.is_running:
            return
        self.is_running = True
        self._start_time = time.time()
        self._thread = threading.Thread(target=self._spin_loop, daemon=True)
        self._thread.start()

    def _spin_loop(self) -> None:
        idx = 0
        arg_disp = f"\033[2m({self.args})\033[0m" if self.args else ""
        while self.is_running:
            elapsed = time.time() - self._start_time
            frame = self.SPINNER_FRAMES[idx % len(self.SPINNER_FRAMES)]
            msg = f"\r\033[K  \033[38;2;56;189;248m{frame}\033[0m ⚙ \033[1m{self.tool_name}\033[0m{arg_disp} \033[2m· {elapsed:.1f}s\033[0m"
            try:
                self.stream.write(msg)
                self.stream.flush()
            except Exception:
                pass
            idx += 1
            time.sleep(0.08)

    def stop(self, status_text: str = "") -> None:
        """Stop spinner and write calm summary line."""
        if not self.is_running:
            return
        self.is_running = False
        if self._thread and self._thread.is_alive():
            self._thread.join(timeout=0.3)

        elapsed = time.time() - self._start_time
        final_msg = status_text or f"  ✓ {self.tool_name} · {elapsed:.1f}s"
        try:
            self.stream.write(f"\r\033[K\033[38;2;16;185;129m{final_msg}\033[0m\n")
            self.stream.flush()
        except Exception:
            pass
