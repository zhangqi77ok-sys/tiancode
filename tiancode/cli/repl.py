"""Interactive REPL shell with prompt_toolkit slash completion, mode badges, and Rich stream UI."""

import builtins
import sys
from typing import Tuple

from prompt_toolkit import PromptSession
from prompt_toolkit.completion import Completer, Completion
from prompt_toolkit.formatted_text import FormattedText
from prompt_toolkit.styles import Style

from tiancode.cli.commands import SlashCommandHandler
from tiancode.cli.formatter import TerminalFormatter
from tiancode.cli.theme import BRAND_COLOR, PERMISSION_BADGES, TEXT_SUBTLE
from tiancode.core.engine import TianCodeEngine

SLASH_COMMAND_HINTS: Tuple[Tuple[str, str], ...] = (
    ("/mode", "切换安全权限模式 (ask / auto / read_only)"),
    ("/config", "查看或热更新配置 (/config show, /config set ...)"),
    ("/doctor", "一键体检环境配置、Git 与 pytest"),
    ("/context", "查看上下文窗口字符占比"),
    ("/compact", "智能折叠上下文历史，释放 Token 预算"),
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


def _extract_engine_meta(engine: TianCodeEngine) -> Tuple[str, str, bool, int]:
    """Safely extract (model, permission_mode, auto_heal, timeout) tolerating test mocks."""
    settings = getattr(engine, "settings", None)
    raw_model = getattr(settings, "model", "")
    model = raw_model if isinstance(raw_model, str) and raw_model else ""

    guard = getattr(engine, "permission_guard", None)
    raw_mode = getattr(getattr(guard, "mode", None), "value", "ask")
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
    badge = f" [{ ' · '.join(tag_parts) }]" if tag_parts else ""
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
    """Render unobtrusive dark toolbar showing active context and hotkeys."""
    model, mode, auto_heal, _ = _extract_engine_meta(engine)
    heal_flag = "自愈:ON" if auto_heal else "自愈:OFF"
    text = f" ⚡ {model or 'default'}  |  🔐 模式: {mode}  |  {heal_flag}  |  输入 / 触发命令补全  |  Ctrl+C 退出"
    return FormattedText([("#94a3b8", text)])


def _can_use_interactive_prompt() -> bool:
    """Enable prompt_toolkit only on real TTYs where builtins.input is unpatched."""
    return (
        sys.stdin.isatty()
        and sys.stdout.isatty()
        and getattr(builtins.input, "__module__", "") == "builtins"
    )


def start_repl(engine: TianCodeEngine, session_id: str = "tiancode_repl") -> None:
    """Launch the interactive terminal REPL with smart fallback for test runners."""
    engine.active_session_id = session_id
    model, mode, auto_heal, timeout = _extract_engine_meta(engine)
    TerminalFormatter.print_banner(
        work_dir=str(engine.settings.work_dir),
        model=model or "deepseek-chat",
        permission_mode=mode,
        auto_heal=auto_heal,
        timeout_seconds=timeout,
    )
    cmd_handler = SlashCommandHandler(engine)
    ptk_session = (
        PromptSession(completer=_SlashCommandCompleter(), style=_PTK_STYLE, complete_while_typing=True)
        if _can_use_interactive_prompt()
        else None
    )

    while True:
        try:
            if ptk_session is not None:
                user_input = ptk_session.prompt(
                    lambda: _build_ptk_prompt(engine),
                    bottom_toolbar=lambda: _build_bottom_toolbar(engine),
                ).strip()
            else:
                user_input = input(_build_prompt(engine)).strip()
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
            current_thread = getattr(engine, "active_session_id", session_id)
            TerminalFormatter.render_task_stream(
                engine.stream_task(user_input, thread_id=current_thread),
                use_spinner=True,
            )
        except Exception as exc:
            TerminalFormatter.print_warning(f"任务执行异常: {str(exc)}")
