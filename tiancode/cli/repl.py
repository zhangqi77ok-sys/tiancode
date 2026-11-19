"""Interactive REPL shell with clickable toolbar, startup rule/memory hints, and fallback input."""

import builtins
import sys
import time
import uuid
from pathlib import Path
from typing import Any, Optional, Tuple

from prompt_toolkit import PromptSession
from prompt_toolkit.formatted_text import FormattedText
from prompt_toolkit.key_binding import KeyBindings
from prompt_toolkit.mouse_events import MouseButton, MouseEvent, MouseEventType

from tiancode.cli.commands import SlashCommandHandler
from tiancode.cli.formatter import TerminalFormatter
from tiancode.cli.theme import BRAND_COLOR, PERMISSION_BADGES, TEXT_SUBTLE
from tiancode.cli.widgets import (
    PTK_STYLE,
    SLASH_COMMAND_HINTS,
    CompositeInputCompleter,
    _ApprovalBarController,
    _SlashCommandCompleter,
    run_mouse_approval_dialog,
)
from tiancode.core.context_mentions import resolve_file_mentions
from tiancode.core.engine import TianCodeEngine
from tiancode.core.permissions import PermissionMode

__all__ = [
    "CompositeInputCompleter",
    "SLASH_COMMAND_HINTS",
    "_ApprovalBarController",
    "_SlashCommandCompleter",
    "_build_bottom_toolbar",
    "_build_prompt",
    "_cycle_engine_permission_mode",
    "can_use_interactive_tty",
    "prepare_task_prompt",
    "run_mouse_approval_dialog",
    "start_repl",
]


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
        ("#94a3b8", "  |  🖱 点击切换 · Shift+Tab 切模式 · / 命令 @ 文件 · Shift+拖拽复制"),
    ])


def prepare_task_prompt(raw_prompt: str, work_dir: Any) -> str:
    """Resolve safe @file mentions into prompt context while surfacing concise UI notices."""
    if not isinstance(work_dir, (str, Path)) or "@" not in raw_prompt:
        return raw_prompt
    res = resolve_file_mentions(raw_prompt, work_dir)
    for w in res.warnings:
        TerminalFormatter.print_warning(w)
    if res.attachments:
        tags = ", ".join(f"{a.rel_path}#L{a.start_line}-{a.end_line}" for a in res.attachments)
        TerminalFormatter.print_info(f"📎 已直注 {len(res.attachments)} 份源码上下文: {tags}")
    return res.expanded_prompt


def can_use_interactive_tty() -> bool:
    """Enable interactive TUI widgets only on real TTYs where builtins.input is unpatched."""
    return sys.stdin.isatty() and sys.stdout.isatty() and getattr(builtins.input, "__module__", "") == "builtins"


def _print_startup_hints(engine: TianCodeEngine) -> None:
    """Display subtle startup hints for detected project rules and recent other workspaces."""
    rules_loader = getattr(engine, "rules_loader", None)
    if rules_loader is not None and hasattr(engine, "settings"):
        docs = rules_loader.load_rules(engine.settings.work_dir)
        if isinstance(docs, list) and docs:
            TerminalFormatter.print_info(f"📜 已加载工程规约: {', '.join(d.filename for d in docs)}")

    workspaces = getattr(getattr(engine, "memory_store", None), "workspaces", None)
    if isinstance(workspaces, dict) and workspaces:
        curr = str(engine.settings.work_dir.resolve()) if hasattr(engine, "settings") else ""
        recent = sorted(workspaces.values(), key=lambda w: getattr(w, "last_active_at", ""), reverse=True)
        others = [w for w in recent if getattr(w, "path", "") != curr]
        if others:
            TerminalFormatter.print_info(f"🧠 记忆已唤醒: 最近活跃项目 [{others[0].name}] ({others[0].path}) · 可直接聊起或 /cd 切换")

    mcp = getattr(engine, "mcp", None)
    if mcp is not None:
        for err in mcp.config_errors():
            TerminalFormatter.print_warning(str(err))
        summary = mcp.status_summary()
        if summary:
            TerminalFormatter.print_info(f"🔌 MCP 服务: {summary} (/mcp 查看详情)")


def start_repl(engine: TianCodeEngine, session_id: Optional[str] = None) -> None:
    """Launch interactive REPL with isolated session ID so prior history is never overwritten."""
    resolved_id = session_id or f"repl_{int(time.time())}_{uuid.uuid4().hex[:6]}"
    engine.active_session_id = resolved_id
    model, mode, auto_heal, timeout = _extract_engine_meta(engine)
    TerminalFormatter.print_banner(
        work_dir=str(engine.settings.work_dir), model=model or "deepseek-chat",
        permission_mode=mode, auto_heal=auto_heal, timeout_seconds=timeout,
    )
    _print_startup_hints(engine)
    cmd_handler = SlashCommandHandler(engine)
    repl_kb = KeyBindings()
    repl_kb.add("s-tab")(lambda event: (_cycle_engine_permission_mode(engine), event.app.invalidate()))
    ptk_session = (
        PromptSession(
            completer=CompositeInputCompleter(get_work_dir=lambda: engine.settings.work_dir),
            style=PTK_STYLE, key_bindings=repl_kb, complete_while_typing=True, mouse_support=True,
        )
        if can_use_interactive_tty() else None
    )

    while True:
        try:
            raw_line = (
                ptk_session.prompt(lambda: _build_ptk_prompt(engine), bottom_toolbar=lambda: _build_bottom_toolbar(engine))
                if ptk_session is not None else input(_build_prompt(engine))
            )
            user_input = raw_line.lstrip("\ufeff").strip()
        except (EOFError, KeyboardInterrupt):
            print("\n👋 已安全退出 TianCode。")
            break

        if not user_input:
            continue
        if user_input.lower() in ("exit", "quit", ":q", "/exit", "/quit"):
            print("👋 已安全退出 TianCode。")
            break
        if cmd_handler.is_slash_command(user_input):
            cmd_handler.execute(user_input)
            continue

        try:
            current_thread = getattr(engine, "active_session_id", resolved_id)
            work_dir = getattr(getattr(engine, "settings", None), "work_dir", None)
            effective_prompt = prepare_task_prompt(user_input, work_dir)
            TerminalFormatter.render_task_stream(engine.stream_task(effective_prompt, thread_id=current_thread), use_spinner=True)
        except Exception as exc:
            TerminalFormatter.print_warning(f"任务执行异常: {str(exc)}")
