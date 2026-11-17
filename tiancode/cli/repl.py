"""Interactive REPL shell for TianCode."""

from tiancode.cli.commands import SlashCommandHandler
from tiancode.cli.formatter import TerminalFormatter
from tiancode.core.engine import TianCodeEngine


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
            in_agent_message = False
            current_thread = getattr(engine, "active_session_id", session_id)
            for event in engine.stream_task(user_input, thread_id=current_thread):
                ev_type = event.get("type") if isinstance(event, dict) else None
                if ev_type == "token":
                    content = event.get("content", "")
                    if content:
                        if not in_agent_message:
                            print(f"\n{TerminalFormatter.CYAN}{TerminalFormatter.BOLD}🤖 TianCode:{TerminalFormatter.RESET} ", end="", flush=True)
                            in_agent_message = True
                        print(content, end="", flush=True)
                elif ev_type == "tool_start":
                    if in_agent_message:
                        print()
                        in_agent_message = False
                    TerminalFormatter.print_info(f"⚙️ 正在调用工具: {event.get('name')}...")
                elif ev_type == "tool_end":
                    if in_agent_message:
                        print()
                        in_agent_message = False
                    summary = str(event.get("content", "")).strip().splitlines()
                    summary_text = summary[0][:120] if summary else ""
                    if summary_text:
                        TerminalFormatter.print_info(f"↳ {summary_text}")
                elif isinstance(event, dict) and "brain" in event:
                    msg = event["brain"]["messages"][-1]
                    if msg.content:
                        TerminalFormatter.print_agent_message(msg.content)

            if in_agent_message:
                print()
        except Exception as exc:
            TerminalFormatter.print_warning(f"任务执行异常: {str(exc)}")
