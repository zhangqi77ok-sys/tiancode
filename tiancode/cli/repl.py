"""Interactive REPL shell for TianCode."""

from tiancode.cli.commands import SlashCommandHandler
from tiancode.cli.formatter import TerminalFormatter
from tiancode.core.engine import TianCodeEngine


def start_repl(engine: TianCodeEngine, session_id: str = "tiancode_repl") -> None:
    """Launch the interactive terminal REPL."""
    TerminalFormatter.print_banner(
        work_dir=str(engine.settings.work_dir),
        model=engine.settings.model,
    )
    cmd_handler = SlashCommandHandler(engine)

    while True:
        try:
            prompt_str = f"{TerminalFormatter.BOLD}tiancode > {TerminalFormatter.RESET}"
            user_input = input(prompt_str).strip()
        except (EOFError, KeyboardInterrupt):
            print("\n👋 已退出 TianCode。")
            break

        if not user_input:
            continue

        if user_input.lower() in ("exit", "quit", ":q"):
            print("👋 已安全退出 TianCode。")
            break

        # Check and handle slash command
        if cmd_handler.is_slash_command(user_input):
            cmd_handler.execute(user_input)
            continue

        # Execute normal coding request
        try:
            for step in engine.stream_task(user_input, thread_id=session_id):
                if "brain" in step:
                    msg = step["brain"]["messages"][-1]
                    if msg.content:
                        TerminalFormatter.print_agent_message(msg.content)
                    if getattr(msg, "tool_calls", None):
                        for tc in msg.tool_calls:
                            TerminalFormatter.print_info(f"⚙️ 调用工具: {tc.get('name')}")
                elif "tools" in step:
                    for tmsg in step["tools"]["messages"]:
                        content_str = str(tmsg.content).strip()
                        if content_str:
                            first_line = content_str.splitlines()[0]
                            TerminalFormatter.print_info(f"↳ {first_line[:120]}")
        except Exception as exc:
            TerminalFormatter.print_warning(f"任务执行异常: {str(exc)}")
