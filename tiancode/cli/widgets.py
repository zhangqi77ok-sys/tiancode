"""Interactive CLI widgets: Braille live spinner, command completer, and mouse approval bar."""

import re
import sys
import threading
import time
from pathlib import Path
from typing import Any, Callable, List, Optional, TextIO, Tuple, Union

from prompt_toolkit import PromptSession
from prompt_toolkit.application import Application
from prompt_toolkit.completion import Completer, Completion
from prompt_toolkit.formatted_text import FormattedText
from prompt_toolkit.key_binding import KeyBindings
from prompt_toolkit.layout import Layout, Window
from prompt_toolkit.layout.controls import FormattedTextControl
from prompt_toolkit.mouse_events import MouseButton, MouseEvent, MouseEventType
from prompt_toolkit.styles import Style
from rich.text import Text

from tiancode.cli.theme import BRAND_COLOR, SUCCESS_COLOR, TEXT_SUBTLE, WARNING_COLOR, get_console
from tiancode.core.permissions import ApprovalResponse, PermissionDecision

SLASH_COMMAND_HINTS: Tuple[Tuple[str, str], ...] = (
    ("/mode", "切换安全权限模式 (ask / auto / read_only)"),
    ("/init", "扫描项目生成 TIANCODE.md (--force 覆盖)"),
    ("/rules", "查看已自动加载的工程规范 (AGENTS.md/TIANCODE.md)"),
    ("/mcp", "查看或重载外部 MCP 服务状态与工具 (/mcp [reload|tools])"),
    ("/cd", "切换当前工作区项目目录 (/cd <path>)"),
    ("/memory", "查看或清空跨会话认知记忆"),
    ("/remember", "固化一条长期事实/规范进语义记忆"),
    ("/config", "查看或热更新配置"),
    ("/doctor", "一键体检环境配置、Git 与 pytest"),
    ("/context", "查看上下文窗口字符占比"),
    ("/compact", "智能折叠上下文历史释放 Token 预算"),
    ("/cost", "查看当前会话 Token 消耗与费用账单"),
    ("/usage", "Token 账单快捷别名 (/cost)"),
    ("/map", "生成项目 AST 代码拓扑全景图"),
    ("/plan", "查看任务计划执行看板"),
    ("/skills", "查看已加载扩展技能"),
    ("/sessions", "列出磁盘持久化的历史会话"),
    ("/switch", "切换并恢复指定会话 (/switch <id>)"),
    ("/diff", "查看当前未提交代码变更补丁"),
    ("/undo", "无损回滚本轮修改"),
    ("/commit", "自动生成 Conventional Commit 并提交"),
    ("/eval", "运行本地自动化基准评测"),
    ("/clear", "清空记忆、快照与任务状态"),
    ("/status", "查看修改文件与消耗状态"),
    ("/help", "显示斜杠命令帮助卡片"),
)

_APPROVAL_BUTTONS: Tuple[Tuple[str, str, str], ...] = (
    ("allow_once", " ✓ 允许本次 (y) ", "bg:#059669 #ffffff bold"),
    ("allow_session", " ⚡ 本会话允许 (a) ", "bg:#0284c7 #ffffff bold"),
    ("edit", " ✎ 人工纠正 (e) ", "bg:#d97706 #ffffff bold"),
    ("deny", " ✕ 拒绝 (n) ", "bg:#e11d48 #ffffff bold"),
)

PTK_STYLE = Style.from_dict({
    "completion-menu.completion": "bg:#1e293b #cbd5e1",
    "completion-menu.completion.current": "bg:#0284c7 #ffffff bold",
    "completion-menu.meta.completion": "bg:#1e293b #64748b",
    "completion-menu.meta.completion.current": "bg:#0369a1 #e2e8f0",
    "scrollbar.background": "bg:#0f172a",
    "scrollbar.button": "bg:#475569",
    "bottom-toolbar": "bg:#0f172a #94a3b8",
})


_AT_TRIGGER_RE = re.compile(r"(?:^|(?<=[\s(\[\"'，、。：；（]))@([^\s@#]*)$")


class CompositeInputCompleter(Completer):
    """Unified interactive completer supporting '/' slash commands and '@' fuzzy workspace files."""

    def __init__(self, get_work_dir: Optional[Callable[[], Any]] = None) -> None:
        self._get_work_dir = get_work_dir
        self._cached_root: Optional[str] = None
        self._cached_at: float = 0.0
        self._cached_files: List[str] = []

    def _get_files(self) -> List[str]:
        if self._get_work_dir is None:
            return []
        raw_dir = self._get_work_dir()
        if not isinstance(raw_dir, (str, Path)):
            return []
        root_key = str(Path(raw_dir).resolve())
        now = time.monotonic()
        if self._cached_root != root_key or (now - self._cached_at) > 3.0:
            from tiancode.core.context_mentions import list_workspace_files
            self._cached_files = list_workspace_files(root_key)
            self._cached_root = root_key
            self._cached_at = now
        return self._cached_files

    def get_completions(self, document, complete_event):
        raw_before = document.text_before_cursor
        stripped = raw_before.lstrip()
        if stripped.startswith("/") and " " not in stripped:
            prefix = stripped.lower()
            for cmd, desc in SLASH_COMMAND_HINTS:
                if cmd.startswith(prefix):
                    yield Completion(cmd, start_position=-len(stripped), display_meta=desc)
            return

        if match := _AT_TRIGGER_RE.search(raw_before):
            from tiancode.core.context_mentions import fuzzy_match_files
            fragment = match.group(1)
            for rel in fuzzy_match_files(fragment, self._get_files(), limit=10):
                yield Completion(rel, start_position=-len(fragment), display_meta="📄 文件引用 (支持 #L起始-结束)")


_SlashCommandCompleter = CompositeInputCompleter


class LiveToolSpinner:
    """Animated Braille spinner with class-level pause lock to prevent approval dialog collisions."""

    SPINNER_FRAMES = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"]
    _active_lock = threading.Lock()
    _active_instance: Optional["LiveToolSpinner"] = None

    def __init__(self, tool_name: str, args: str = "", stream: Optional[TextIO] = None) -> None:
        self.tool_name = tool_name
        self.args = str(args)[:60].replace("\n", " ") if args else ""
        self.stream = stream or sys.stdout
        self.is_running = False
        self._thread: Optional[threading.Thread] = None
        self._start_time: float = 0.0

    @classmethod
    def pause_active(cls) -> bool:
        """Immediately halt and clear any active spinner before interactive stdin prompts."""
        with cls._active_lock:
            inst = cls._active_instance
        if inst is not None and inst.is_running:
            inst.stop(clear_line_only=True)
            return True
        return False

    def start(self) -> None:
        if self.is_running:
            return
        self.is_running = True
        self._start_time = time.time()
        with self._active_lock:
            LiveToolSpinner._active_instance = self
        self._thread = threading.Thread(target=self._spin_loop, daemon=True)
        self._thread.start()

    def _spin_loop(self) -> None:
        idx = 0
        arg_disp = f" \033[2m({self.args})\033[0m" if self.args else ""
        while self.is_running:
            elapsed = time.time() - self._start_time
            frame = self.SPINNER_FRAMES[idx % len(self.SPINNER_FRAMES)]
            msg = f"\r\033[K  \033[38;2;56;189;248m{frame}\033[0m ▸ \033[1m{self.tool_name}\033[0m{arg_disp} \033[2m· {elapsed:.1f}s\033[0m"
            try:
                self.stream.write(msg)
                self.stream.flush()
            except Exception:
                pass
            idx += 1
            time.sleep(0.08)

    def stop(self, status_text: str = "", *, clear_line_only: bool = False) -> None:
        if not self.is_running:
            return
        self.is_running = False
        with self._active_lock:
            if LiveToolSpinner._active_instance is self:
                LiveToolSpinner._active_instance = None
        if self._thread and self._thread.is_alive():
            self._thread.join(timeout=0.3)

        try:
            if clear_line_only:
                self.stream.write("\r\033[K")
            else:
                elapsed = time.time() - self._start_time
                final_msg = status_text or f"  ✓ {self.tool_name} · {elapsed:.1f}s"
                self.stream.write(f"\r\033[K\033[38;2;16;185;129m{final_msg}\033[0m\n")
            self.stream.flush()
        except Exception:
            pass


class _ApprovalBarController:
    """Mouse-and-keyboard interactive pill button bar for tool approval and human steering."""

    def __init__(self, on_select: Callable[[str], None]) -> None:
        self.selected_idx: int = 0
        self._on_select = on_select

    @property
    def current_action(self) -> str:
        return _APPROVAL_BUTTONS[self.selected_idx][0]

    def move(self, delta: int) -> None:
        self.selected_idx = (self.selected_idx + delta) % len(_APPROVAL_BUTTONS)

    def get_formatted_text(self) -> FormattedText:
        fragments: List[Tuple] = [("", "  ")]
        for idx, (action, label, active_style) in enumerate(_APPROVAL_BUTTONS):
            if idx > 0:
                fragments.append(("", "  "))
            style = active_style if idx == self.selected_idx else "bg:#1e293b #cbd5e1"

            def _make_handler(i: int, act: str):
                def _handle_mouse(mouse_event: MouseEvent):
                    if getattr(mouse_event, "button", MouseButton.LEFT) != MouseButton.LEFT:
                        return NotImplemented
                    if mouse_event.event_type == MouseEventType.MOUSE_DOWN:
                        self.selected_idx = i
                    elif mouse_event.event_type == MouseEventType.MOUSE_UP:
                        self.selected_idx = i
                        self._on_select(act)
                return _handle_mouse

            fragments.append((style, label, _make_handler(idx, action)))
        fragments.append(("#64748b", "\n  🖱 支持鼠标直接点击按钮 · ◀/▶ 或 Tab 切换 · Enter 确认 · e 输入纠正指令"))
        return FormattedText(fragments)


def run_mouse_approval_dialog(tool_name: str) -> Union[PermissionDecision, ApprovalResponse]:
    """Run inline mouse-enabled pill selector and optional human-correction prompt."""
    chosen: List[str] = []
    ctrl = _ApprovalBarController(on_select=lambda act: chosen.append(act))
    kb = KeyBindings()
    app: Application = Application(
        layout=Layout(Window(FormattedTextControl(ctrl.get_formatted_text, focusable=True), height=2)),
        key_bindings=kb, mouse_support=True, full_screen=False,
    )

    def _finish(act: str) -> None:
        chosen.append(act)
        app.exit(result=act)

    ctrl._on_select = _finish
    for key, fn in (
        ("left", lambda _: ctrl.move(-1)), ("s-tab", lambda _: ctrl.move(-1)),
        ("right", lambda _: ctrl.move(1)), ("tab", lambda _: ctrl.move(1)),
        ("enter", lambda _: _finish(ctrl.current_action)), (" ", lambda _: _finish(ctrl.current_action)),
        ("y", lambda _: _finish("allow_once")), ("a", lambda _: _finish("allow_session")),
        ("e", lambda _: _finish("edit")), ("n", lambda _: _finish("deny")),
        ("escape", lambda _: _finish("deny")), ("c-c", lambda _: _finish("deny")),
    ):
        kb.add(key)(fn)

    try:
        action = app.run() or (chosen[0] if chosen else "deny")
    except (EOFError, KeyboardInterrupt):
        action = "deny"

    console = get_console()
    if action == "allow_once":
        console.print(Text(f"  ✓ 已授权本次执行 '{tool_name}'", style=SUCCESS_COLOR))
        return PermissionDecision.ALLOW_ONCE
    if action == "allow_session":
        console.print(Text(f"  ⚡ 已授权本会话始终允许 '{tool_name}'", style=BRAND_COLOR))
        return PermissionDecision.ALLOW_SESSION
    if action == "edit":
        try:
            feedback = PromptSession().prompt(
                FormattedText([(f"bold {WARNING_COLOR}", "  ✎ 请输入人工纠正指令 (回车发送给 AI，留空取消): ❯ ")])
            ).strip()
        except (EOFError, KeyboardInterrupt):
            feedback = ""
        if feedback:
            console.print(Text(f"  ✎ 已拦截并注入人工纠正指导: {feedback}", style=WARNING_COLOR))
            return ApprovalResponse(decision=PermissionDecision.DENY, feedback=feedback)

    console.print(Text(f"  ✕ 已拒绝执行 '{tool_name}'", style=TEXT_SUBTLE))
    return PermissionDecision.DENY
