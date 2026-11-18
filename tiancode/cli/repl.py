"""Interactive REPL shell, mouse-clickable HITL approval bar, and human correction dialog."""

import builtins
import sys
import time
import uuid
from typing import Callable, List, Optional, Tuple, Union

from prompt_toolkit import PromptSession
from prompt_toolkit.application import Application
from prompt_toolkit.completion import Completer, Completion
from prompt_toolkit.formatted_text import FormattedText
from prompt_toolkit.key_binding import KeyBindings
from prompt_toolkit.layout import Layout, Window
from prompt_toolkit.layout.controls import FormattedTextControl
from prompt_toolkit.mouse_events import MouseButton, MouseEvent, MouseEventType
from prompt_toolkit.styles import Style

from tiancode.cli.commands import SlashCommandHandler
from tiancode.cli.formatter import TerminalFormatter
from tiancode.cli.theme import BRAND_COLOR, PERMISSION_BADGES, SUCCESS_COLOR, TEXT_SUBTLE, WARNING_COLOR, get_console
from tiancode.core.engine import TianCodeEngine
from tiancode.core.permissions import ApprovalResponse, PermissionDecision, PermissionMode
from rich.text import Text

SLASH_COMMAND_HINTS: Tuple[Tuple[str, str], ...] = (
    ("/mode", "切换安全权限模式 (ask / auto / read_only)"), ("/cd", "切换当前工作区项目目录 (/cd <path>)"),
    ("/memory", "查看或清空跨会话认知记忆"), ("/remember", "固化一条长期事实/规范进语义记忆"),
    ("/config", "查看或热更新配置"), ("/doctor", "一键体检环境配置、Git 与 pytest"),
    ("/context", "查看上下文窗口字符占比"), ("/compact", "智能折叠上下文历史释放 Token 预算"),
    ("/cost", "查看当前会话 Token 消耗与费用账单"), ("/usage", "Token 账单快捷别名 (/cost)"),
    ("/map", "生成项目 AST 代码拓扑全景图"), ("/plan", "查看任务计划执行看板"),
    ("/skills", "查看已加载扩展技能"), ("/sessions", "列出磁盘持久化的历史会话"),
    ("/switch", "切换并恢复指定会话 (/switch <id>)"), ("/diff", "查看当前未提交代码变更补丁"),
    ("/undo", "无损回滚本轮修改"), ("/commit", "自动生成 Conventional Commit 并提交"),
    ("/eval", "运行本地自动化基准评测"), ("/clear", "清空记忆、快照与任务状态"),
    ("/status", "查看修改文件与消耗状态"), ("/help", "显示斜杠命令帮助卡片"),
)

_APPROVAL_BUTTONS: Tuple[Tuple[str, str, str], ...] = (
    ("allow_once", " ✓ 允许本次 (y) ", "bg:#059669 #ffffff bold"),
    ("allow_session", " ⚡ 本会话允许 (a) ", "bg:#0284c7 #ffffff bold"),
    ("edit", " ✎ 人工纠正 (e) ", "bg:#d97706 #ffffff bold"),
    ("deny", " ✕ 拒绝 (n) ", "bg:#e11d48 #ffffff bold"),
)

_PTK_STYLE = Style.from_dict({
    "completion-menu.completion": "bg:#1e293b #cbd5e1",
    "completion-menu.completion.current": "bg:#0284c7 #ffffff bold",
    "completion-menu.meta.completion": "bg:#1e293b #64748b",
    "completion-menu.meta.completion.current": "bg:#0369a1 #e2e8f0",
    "scrollbar.background": "bg:#0f172a",
    "scrollbar.button": "bg:#475569",
    "bottom-toolbar": "bg:#0f172a #94a3b8",
})


class _SlashCommandCompleter(Completer):
    """Offer interactive dropdown completions only when typing a slash command."""

    def get_completions(self, document, complete_event):
        text = document.text_before_cursor.lstrip()
        if not text.startswith("/") or " " in text:
            return
        prefix = text.lower()
        for cmd, desc in SLASH_COMMAND_HINTS:
            if cmd.startswith(prefix):
                yield Completion(cmd, start_position=-len(text), display_meta=desc)


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
        key_bindings=kb,
        mouse_support=True,
        full_screen=False,
    )

    def _finish(act: str) -> None:
        chosen.append(act)
        app.exit(result=act)

    ctrl._on_select = _finish
    kb.add("left")(lambda _: ctrl.move(-1))
    kb.add("s-tab")(lambda _: ctrl.move(-1))
    kb.add("right")(lambda _: ctrl.move(1))
    kb.add("tab")(lambda _: ctrl.move(1))
    kb.add("enter")(lambda _: _finish(ctrl.current_action))
    kb.add(" ")(lambda _: _finish(ctrl.current_action))
    kb.add("y")(lambda _: _finish("allow_once"))
    kb.add("a")(lambda _: _finish("allow_session"))
    kb.add("e")(lambda _: _finish("edit"))
    kb.add("n")(lambda _: _finish("deny"))
    kb.add("escape")(lambda _: _finish("deny"))
    kb.add("c-c")(lambda _: _finish("deny"))

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


def _cycle_engine_permission_mode(engine: TianCodeEngine) -> Optional[PermissionMode]:
    """Cycle permission mode round-robin: ask -> auto -> read_only -> ask."""
    guard = getattr(engine, "permission_guard", None)
    if guard is None or not hasattr(engine, "set_permission_mode"):
        return None
    order = (PermissionMode.ASK, PermissionMode.AUTO, PermissionMode.READ_ONLY)
    curr = getattr(guard, "mode", PermissionMode.ASK)
    next_idx = (order.index(curr) + 1) % len(order) if curr in order else 0
    return engine.set_permission_mode(order[next_idx])


def _extract_engine_meta(engine: TianCodeEngine) -> Tuple[str, str, bool, int]:
    """Safely extract (model, permission_mode, auto_heal, timeout) tolerating test mocks."""
    settings = getattr(engine, "settings", None)
    raw_model = getattr(settings, "model", "")
    model = raw_model if isinstance(raw_model, str) and raw_model else ""
    raw_mode = getattr(getattr(getattr(engine, "permission_guard", None), "mode", None), "value", "ask")
    mode = raw_mode if isinstance(raw_mode, str) else "ask"
    raw_heal = getattr(settings, "auto_heal", True)
    auto_heal = raw_heal if isinstance(raw_heal, bool) else True
    raw_timeout = getattr(settings, "timeout_seconds", 15)
    timeout = raw_timeout if isinstance(raw_timeout, int) and not isinstance(raw_timeout, bool) else 15
    return model, mode, auto_heal, timeout


def _build_prompt(engine: TianCodeEngine) -> str:
    """Build ANSI-styled fallback prompt displaying active model and permission mode."""
    model, mode, _, _ = _extract_engine_meta(engine)
    tag_parts = [p for p in (model, mode) if p]
    badge = f" [{' · '.join(tag_parts)}]" if tag_parts else ""
    return f"\033[38;2;56;189;248m\033[1mtiancode\033[0m\033[2m{badge}\033[0m \033[38;2;56;189;248m❯\033[0m "


def _build_ptk_prompt(engine: TianCodeEngine) -> FormattedText:
    """Build interactive prompt_toolkit prompt with semantic badge colors."""
    model, mode, _, _ = _extract_engine_meta(engine)
    _, mode_color, _ = PERMISSION_BADGES.get(mode, ("🔐 ask", "#F59E0B", "交互"))
    fragments = [(f"bold {BRAND_COLOR}", "tiancode")]
    if model:
        fragments.append((TEXT_SUBTLE, f" [{model}]"))
    fragments.append((f"{mode_color}", f" [{mode}]"))
    fragments.append((f"bold {BRAND_COLOR}", " ❯ "))
    return FormattedText(fragments)


def _build_bottom_toolbar(engine: TianCodeEngine) -> FormattedText:
    """Render clickable dark action dock for mouse mode/heal toggling and hotkey hints."""
    model, mode, auto_heal, _ = _extract_engine_meta(engine)
    heal_label = " ● 自愈:ON " if auto_heal else " ○ 自愈:OFF "
    heal_style = "bg:#065f46 #ecfdf5" if auto_heal else "bg:#1e293b #94a3b8"

    def _on_mode_click(mouse_event: MouseEvent):
        if mouse_event.event_type == MouseEventType.MOUSE_UP and getattr(mouse_event, "button", MouseButton.LEFT) == MouseButton.LEFT:
            _cycle_engine_permission_mode(engine)

    def _on_heal_click(mouse_event: MouseEvent):
        if mouse_event.event_type == MouseEventType.MOUSE_UP and getattr(mouse_event, "button", MouseButton.LEFT) == MouseButton.LEFT:
            settings = getattr(engine, "settings", None)
            if settings is not None and isinstance(getattr(settings, "auto_heal", None), bool):
                settings.auto_heal = not settings.auto_heal
                if hasattr(engine, "reload_llm"):
                    engine.reload_llm()

    return FormattedText([
        ("#94a3b8", f" ⚡ {model or 'default'}  "),
        ("bg:#78350f #fef3c7 bold", f" 🔐 模式:{mode} ", _on_mode_click),
        ("", " "),
        (heal_style, heal_label, _on_heal_click),
        ("#94a3b8", "  |  🖱 点击切换 · Shift+Tab 切模式 · / 补全 · Shift+拖拽复制"),
    ])


def can_use_interactive_tty() -> bool:
    """Enable interactive TUI widgets only on real TTYs where builtins.input is unpatched."""
    return sys.stdin.isatty() and sys.stdout.isatty() and getattr(builtins.input, "__module__", "") == "builtins"


def _print_memory_wakeup_hint(engine: TianCodeEngine) -> None:
    """Display concise hint if previous active workspace exists outside current work_dir."""
    store = getattr(engine, "memory_store", None)
    workspaces = getattr(store, "workspaces", None)
    if not isinstance(workspaces, dict) or not workspaces:
        return
    curr = str(engine.settings.work_dir.resolve()) if hasattr(engine.settings, "work_dir") else ""
    recent = sorted(workspaces.values(), key=lambda w: getattr(w, "last_active_at", ""), reverse=True)
    others = [w for w in recent if getattr(w, "path", "") != curr]
    if others:
        w = others[0]
        TerminalFormatter.print_info(f"🧠 记忆已唤醒: 最近活跃项目 [{w.name}] ({w.path}) · 可直接聊起或 /cd 切换")


def start_repl(engine: TianCodeEngine, session_id: Optional[str] = None) -> None:
    """Launch interactive REPL with isolated session ID so prior history is never overwritten."""
    resolved_id = session_id or f"repl_{int(time.time())}_{uuid.uuid4().hex[:6]}"
    engine.active_session_id = resolved_id
    model, mode, auto_heal, timeout = _extract_engine_meta(engine)
    TerminalFormatter.print_banner(
        work_dir=str(engine.settings.work_dir), model=model or "deepseek-chat",
        permission_mode=mode, auto_heal=auto_heal, timeout_seconds=timeout,
    )
    _print_memory_wakeup_hint(engine)
    cmd_handler = SlashCommandHandler(engine)
    repl_kb = KeyBindings()
    repl_kb.add("s-tab")(lambda event: (_cycle_engine_permission_mode(engine), event.app.invalidate()))
    ptk_session = PromptSession(completer=_SlashCommandCompleter(), style=_PTK_STYLE, key_bindings=repl_kb, complete_while_typing=True, mouse_support=True) if can_use_interactive_tty() else None

    while True:
        try:
            user_input = (
                ptk_session.prompt(lambda: _build_ptk_prompt(engine), bottom_toolbar=lambda: _build_bottom_toolbar(engine)).strip()
                if ptk_session is not None else input(_build_prompt(engine)).strip()
            )
        except (EOFError, KeyboardInterrupt):
            print("\n👋 已安全退出 TianCode。")
            break

        if not user_input:
            continue
        if user_input.lower() in ("exit", "quit", ":q"):
            print("👋 已安全退出 TianCode。")
            break
        if cmd_handler.is_slash_command(user_input):
            cmd_handler.execute(user_input)
            continue

        try:
            current_thread = getattr(engine, "active_session_id", resolved_id)
            TerminalFormatter.render_task_stream(engine.stream_task(user_input, thread_id=current_thread), use_spinner=True)
        except Exception as exc:
            TerminalFormatter.print_warning(f"任务执行异常: {str(exc)}")
