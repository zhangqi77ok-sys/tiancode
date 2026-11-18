"""Main CLI entrypoint for TianCode."""

import argparse
import sys
import time
import uuid
from pathlib import Path
from typing import Optional

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


def _resolve_resumed_session(engine: TianCodeEngine, args: argparse.Namespace) -> Optional[str]:
    """Restore explicit or latest session if requested via CLI flags."""
    if args.continue_session:
        latest_id = engine.session_store.get_latest_session_id()
        if latest_id and engine.resume_session(latest_id):
            TerminalFormatter.print_success(f"已成功自动恢复最近活跃会话 [{latest_id}]！")
            if engine.planner.active_plan:
                TerminalFormatter.print_task_plan(engine.planner.active_plan)
            return latest_id
        TerminalFormatter.print_info("未发现可恢复的历史会话，已开启全新工作会话。")
        return None

    if args.resume_id:
        if engine.resume_session(args.resume_id):
            TerminalFormatter.print_success(f"已成功恢复指定历史会话 [{args.resume_id}]！")
            if engine.planner.active_plan:
                TerminalFormatter.print_task_plan(engine.planner.active_plan)
            return args.resume_id
        TerminalFormatter.print_warning(f"未能找到指定的会话 [{args.resume_id}]。")
    return None


def _run_oneshot_task(engine: TianCodeEngine, task_str: str, thread_id: str) -> None:
    """Execute a single CLI task with streaming output and compact usage telemetry."""
    TerminalFormatter.print_info(f"⚡ [单发任务] 正在处理: '{task_str}' (会话ID: {thread_id}) ...")
    usage_footer: Optional[str] = None
    in_agent_message = False
    try:
        for event in engine.stream_task(task_str, thread_id=thread_id):
            ev_type = event.get("type") if isinstance(event, dict) else None
            if ev_type == "token" and event.get("content"):
                if not in_agent_message:
                    print(f"\n{TerminalFormatter.CYAN}{TerminalFormatter.BOLD}🤖 TianCode:{TerminalFormatter.RESET} ", end="", flush=True)
                    in_agent_message = True
                print(event["content"], end="", flush=True)
            elif ev_type == "tool_start":
                if in_agent_message:
                    print()
                    in_agent_message = False
                TerminalFormatter.print_info(f"⚙️ 正在调用工具: {event.get('name')}...")
            elif ev_type == "tool_end":
                if in_agent_message:
                    print()
                    in_agent_message = False
                lines = str(event.get("content", "")).strip().splitlines()
                if lines:
                    TerminalFormatter.print_info(f"↳ {lines[0][:120]}")
            elif ev_type == "usage_summary":
                usage_footer = (
                    f"⚡ [本次消耗: {event.get('turn_tokens', 0):,} Tokens | "
                    f"会话累计: {event.get('session_tokens', 0):,} Tokens (¥{event.get('session_cost', 0.0):.4f})]"
                )
            elif isinstance(event, dict) and "brain" in event:
                msg = event["brain"]["messages"][-1]
                if msg.content:
                    TerminalFormatter.print_agent_message(msg.content)
        if in_agent_message:
            print()
        if usage_footer:
            TerminalFormatter.print_info(usage_footer)
    except Exception as exc:
        TerminalFormatter.print_warning(f"单发任务执行失败: {str(exc)}")
        sys.exit(1)


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

    if not settings.api_key:
        settings = prompt_setup_wizard(settings)

    engine = TianCodeEngine(settings=settings)

    if args.task and args.task[0] == "eval":
        from tiancode.eval.runner import BenchmarkRunner
        pids = args.task[1:] if len(args.task) > 1 else None
        TerminalFormatter.print_info("⚡ 正在启动 TianCode 自动化基准评测 (SWE-bench 体系)...")
        report = BenchmarkRunner(engine=engine)
        print(report.run_suite(problem_ids=pids).format_table())
        return

    resumed_session_id = _resolve_resumed_session(engine, args)

    if args.task:
        task_str = " ".join(args.task)
        thread_id = resumed_session_id or f"oneshot_{int(time.time())}_{uuid.uuid4().hex[:6]}"
        _run_oneshot_task(engine, task_str, thread_id)
    elif resumed_session_id:
        start_repl(engine, session_id=resumed_session_id)
    else:
        start_repl(engine)


if __name__ == "__main__":
    main()
