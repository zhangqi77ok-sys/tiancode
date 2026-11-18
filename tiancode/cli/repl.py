"""Interactive REPL shell for TianCode with unified stream rendering and dynamic prompt."""

from tiancode.cli.commands import SlashCommandHandler
from tiancode.cli.formatter import TerminalFormatter
from tiancode.core.engine import TianCodeEngine


def _build_prompt(engine: TianCodeEngine) -> str:
    """Build dynamic interactive prompt displaying active model."""
    model_name = getattr(getattr(engine, "settings", None), "model", "")
    model_tag = f" [{model_name}]" if isinstance(model_name, str) and model_name else ""
    return f"{TerminalFormatter.BOLD}tiancode{model_tag} > {TerminalFormatter.RESET}"


def start_repl(engine: TianCodeEngine, session_id: str = "tiancode_repl") -> None:
    """Launch the interactive terminal REPL."""
    engine.active_session_id = session_id
    TerminalFormatter.print_banner(
        work_dir=str(engine.settings.work_dir),
        model=engine.settings.model,
    )
    cmd_handler = SlashCommandHandler(engine)

    while True:
        try:
            user_input = input(_build_prompt(engine)).strip()
        except (EOFError, KeyboardInterrupt):
            print("\n👋 已退出 TianCode。")
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
