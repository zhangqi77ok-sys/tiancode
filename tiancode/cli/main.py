"""Main CLI entrypoint for TianCode."""

import argparse
import sys
from pathlib import Path

from tiancode import __version__
from tiancode.cli.formatter import TerminalFormatter
from tiancode.cli.repl import start_repl
from tiancode.config.settings import Settings, prompt_setup_wizard
from tiancode.core.engine import TianCodeEngine


def build_parser() -> argparse.ArgumentParser:
    """Construct argument parser for CLI."""
    parser = argparse.ArgumentParser(
        prog="tiancode",
        description="TianCode - 本地终端 AI 编程智能体",
    )
    parser.add_argument(
        "task",
        nargs="*",
        help="单发编程指令 (若省略则进入交互式工作台)",
    )
    parser.add_argument(
        "--dir", "-d",
        dest="work_dir",
        default=".",
        help="指定工作区目录 (默认为当前目录)",
    )
    parser.add_argument(
        "--model", "-m",
        dest="model",
        default=None,
        help="指定大模型名称 (默认 deepseek-chat)",
    )
    parser.add_argument(
        "--key", "-k",
        dest="api_key",
        default=None,
        help="指定 API Key (默认读取 TIANCODE_API_KEY 或 DEEPSEEK_API_KEY)",
    )
    parser.add_argument(
        "--base-url", "-u",
        dest="base_url",
        default=None,
        help="指定 OpenAI 兼容协议 Base URL",
    )
    parser.add_argument(
        "--continue", "-c",
        dest="continue_session",
        action="store_true",
        help="无缝断点恢复最近一次活跃的会话",
    )
    parser.add_argument(
        "--resume",
        dest="resume_id",
        type=str,
        default=None,
        help="恢复指定的历史会话 ID",
    )
    parser.add_argument(
        "--version", "-v",
        action="version",
        version=f"%(prog)s {__version__}",
    )
    return parser


def main() -> None:
    """CLI execution entrypoint."""
    parser = build_parser()
    args = parser.parse_args()

    work_dir = Path(args.work_dir).resolve()
    settings = Settings.load(
        api_key=args.api_key,
        base_url=args.base_url,
        model=args.model,
        work_dir=work_dir,
    )

    # 首次使用或未配置 Key 时，主动触发交互式配置向导并持久化到 ~/.tiancode/config.json
    if not settings.api_key:
        settings = prompt_setup_wizard(settings)

    engine = TianCodeEngine(settings=settings)

    # 快捷运行自动化基准评测：tc eval [problem_id]
    if args.task and args.task[0] == "eval":
        from tiancode.eval.runner import BenchmarkRunner
        pids = args.task[1:] if len(args.task) > 1 else None
        TerminalFormatter.print_info("⚡ 正在启动 TianCode 自动化基准评测 (SWE-bench 体系)...")
        runner = BenchmarkRunner(engine=engine)
        report = runner.run_suite(problem_ids=pids)
        print(report.format_table())
        return

    session_id = "tiancode_repl"
    if args.continue_session:
        latest_id = engine.session_store.get_latest_session_id()
        if latest_id and engine.resume_session(latest_id):
            session_id = latest_id
            TerminalFormatter.print_success(f"已成功自动恢复最近活跃会话 [{latest_id}]！")
            if engine.planner.active_plan:
                TerminalFormatter.print_task_plan(engine.planner.active_plan)
        else:
            TerminalFormatter.print_info("未发现可恢复的历史会话，已开启全新工作会话。")
    elif args.resume_id:
        if engine.resume_session(args.resume_id):
            session_id = args.resume_id
            TerminalFormatter.print_success(f"已成功恢复指定历史会话 [{args.resume_id}]！")
            if engine.planner.active_plan:
                TerminalFormatter.print_task_plan(engine.planner.active_plan)
        else:
            TerminalFormatter.print_warning(f"未能找到指定的会话 [{args.resume_id}]。")

    if args.task:
        task_str = " ".join(args.task)
        if session_id:
            active_task_session_id = session_id
        else:
            import time
            import uuid
            active_task_session_id = f"oneshot_{int(time.time())}_{uuid.uuid4().hex[:6]}"

        TerminalFormatter.print_info(f"⚡ [单发任务] 正在处理: '{task_str}' (会话ID: {active_task_session_id}) ...")
        try:
            in_agent_message = False
            for event in engine.stream_task(task_str, thread_id=active_task_session_id):
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
            TerminalFormatter.print_warning(f"单发任务执行失败: {str(exc)}")
            sys.exit(1)
    else:
        if session_id != "tiancode_repl":
            start_repl(engine, session_id=session_id)
        else:
            start_repl(engine)


if __name__ == "__main__":
    main()
