"""Main CLI entrypoint for TianCode."""

import argparse
import sys
import time
import uuid
from pathlib import Path
from typing import Any, Dict, Optional, Union

from tiancode import __version__
from tiancode.cli.formatter import LiveToolSpinner, TerminalFormatter
from tiancode.cli.repl import can_use_interactive_tty, prepare_task_prompt, run_mouse_approval_dialog, start_repl
from tiancode.config.settings import Settings, prompt_setup_wizard
from tiancode.core.engine import TianCodeEngine
from tiancode.core.permissions import ApprovalResponse, PermissionDecision, PermissionMode


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
        "--no-heal",
        dest="no_heal",
        action="store_true",
        help="禁用写后自动运行项目测试与自愈循环",
    )
    parser.add_argument(
        "--max-chars",
        dest="max_chars",
        type=int,
        default=None,
        help="指定上下文最大字符预算阈值（超出将触发智能折叠压缩）",
    )
    mode_group = parser.add_mutually_exclusive_group()
    mode_group.add_argument(
        "--yes", "-y",
        dest="auto_yes",
        action="store_true",
        help="免确认自动批准所有文件变更与终端命令执行",
    )
    mode_group.add_argument(
        "--read-only",
        dest="read_only",
        action="store_true",
        help="只读安全模式，禁止所有写入、修改、删除与命令执行",
    )
    parser.add_argument(
        "--version", "-v",
        action="version",
        version=f"%(prog)s {__version__}",
    )
    return parser


def _resolve_permission_mode(args: argparse.Namespace) -> PermissionMode:
    """Resolve initial PermissionMode from mutually exclusive CLI flags."""
    if getattr(args, "read_only", False):
        return PermissionMode.READ_ONLY
    if getattr(args, "auto_yes", False):
        return PermissionMode.AUTO
    return PermissionMode.ASK


def _format_tool_preview(args_dict: Dict[str, Any]) -> str:
    """Format concise single-line preview of tool arguments for interactive approval."""
    if not isinstance(args_dict, dict) or not args_dict:
        return "{}"
    parts = []
    for k, v in args_dict.items():
        text = str(v).replace("\n", "\\n")
        if len(text) > 80:
            text = text[:77] + "..."
        parts.append(f"{k}={text}")
    return ", ".join(parts)


def _terminal_approval_prompt(
    tool_name: str, args_dict: Dict[str, Any]
) -> Union[PermissionDecision, ApprovalResponse]:
    """Pause spinner, display rounded approval card, and run mouse/steering prompt (fail-closed)."""
    from rich import box
    from rich.panel import Panel
    from rich.table import Table
    from rich.text import Text
    from tiancode.cli.theme import TEXT_MUTED, TEXT_PRIMARY, WARNING_COLOR, get_console

    LiveToolSpinner.pause_active()

    preview = _format_tool_preview(args_dict)
    grid = Table.grid(padding=(0, 2))
    grid.add_column(style=TEXT_MUTED, no_wrap=True)
    grid.add_column(style=TEXT_PRIMARY)
    grid.add_row("目标工具", Text(tool_name, style=f"bold {WARNING_COLOR}"))
    grid.add_row("参数摘要", Text(preview, style=TEXT_PRIMARY))
    if not can_use_interactive_tty():
        grid.add_row(
            "快捷操作",
            Text("[y] 允许本次  [a] 本会话允许  [e] 人工纠正  [n] 拒绝 (或直接输入纠正指令)", style=TEXT_MUTED),
        )

    console = get_console()
    console.print()
    console.print(
        Panel(
            grid,
            title=Text("⚠️ 工具执行权限与人工干预", style=f"bold {WARNING_COLOR}"),
            title_align="left",
            box=box.ROUNDED,
            border_style=WARNING_COLOR,
            padding=(0, 2),
        )
    )

    if can_use_interactive_tty():
        return run_mouse_approval_dialog(tool_name)

    try:
        raw_answer = input(f"   ❯ 操作选择 [y/a/e/n 或直接输入纠正指令]: ").strip()
    except (EOFError, KeyboardInterrupt):
        return PermissionDecision.DENY

    low = raw_answer.lower()
    if low in ("y", "yes"):
        return PermissionDecision.ALLOW_ONCE
    if low in ("a", "always"):
        return PermissionDecision.ALLOW_SESSION
    if low in ("", "n", "no"):
        return PermissionDecision.DENY
    if low in ("e", "edit"):
        try:
            correction = input("   ✎ 请输入人工纠正指令: ").strip()
        except (EOFError, KeyboardInterrupt):
            return PermissionDecision.DENY
        return ApprovalResponse(decision=PermissionDecision.DENY, feedback=correction) if correction else PermissionDecision.DENY

    return ApprovalResponse(decision=PermissionDecision.DENY, feedback=raw_answer)


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
    """Execute a single CLI task with clean header and unified Rich stream output."""
    TerminalFormatter.print_user_turn(task_str)
    TerminalFormatter.print_info(f"单发会话 {thread_id}")
    work_dir = getattr(getattr(engine, "settings", None), "work_dir", None)
    effective_prompt = prepare_task_prompt(task_str, work_dir)
    try:
        TerminalFormatter.render_task_stream(
            engine.stream_task(effective_prompt, thread_id=thread_id),
            use_spinner=False,
        )
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
    if getattr(args, "no_heal", False):
        settings.auto_heal = False
    if getattr(args, "max_chars", None) and args.max_chars > 0:
        settings.max_context_chars = args.max_chars

    if not settings.api_key:
        settings = prompt_setup_wizard(settings)

    engine = TianCodeEngine(settings=settings)
    perm_mode = _resolve_permission_mode(args)
    if hasattr(engine, "set_permission_mode"):
        engine.set_permission_mode(perm_mode, prompt_fn=_terminal_approval_prompt)

    if args.task and args.task[0] == "eval":
        if perm_mode is not PermissionMode.READ_ONLY:
            engine.set_permission_mode(PermissionMode.AUTO)
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
