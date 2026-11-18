"""Slash command router and workspace/session/git command handlers for TianCode REPL."""

from typing import TYPE_CHECKING, Callable, Dict, Optional, Tuple

from rich import box
from rich.panel import Panel
from rich.table import Table
from rich.text import Text

from tiancode.cli.command_ops import (
    apply_config_set,
    handle_cd,
    handle_init,
    handle_memory,
    handle_remember,
    handle_rules,
    print_table,
    show_config_panel,
    show_cost_panel,
)
from tiancode.cli.formatter import TerminalFormatter
from tiancode.cli.theme import BORDER_MUTED, BRAND_COLOR, SUCCESS_COLOR, TEXT_MUTED, TEXT_PRIMARY, TEXT_SUBTLE, get_console

if TYPE_CHECKING:
    from tiancode.core.engine import TianCodeEngine

_HELP_GROUPS: Tuple[Tuple[str, str], ...] = (
    ("/mode [ask|auto|read_only]", "查看或动态切换 HITL 安全权限模式"),
    ("/init [--force] | /rules", "扫描技术栈生成 TIANCODE.md 规范或查看已加载工程规约"),
    ("/cd <dir>", "切换当前工作区沙箱并重锚 Git/文件工具"),
    ("/memory [clear] | /remember <事实>", "查看跨会话认知记忆档案或固化长期知识"),
    ("/config show | set <k> <v>", "查看或热更新配置 (key/url/model/max_chars/timeout/auto_heal)"),
    ("/doctor", "一键执行配置、目录权限、Git 与 pytest 健康自检"),
    ("/context | /compact", "可视化上下文窗口占用或立即智能压缩折叠"),
    ("/cost | /usage", "查看当前会话 Token 消耗、缓存命中率与预估费用"),
    ("/map | /plan | /skills", "查看 AST 代码拓扑、任务执行看板与已加载技能"),
    ("/sessions | /switch <id>", "列出持久化历史会话或热切换恢复"),
    ("/diff | /undo | /commit", "代码补丁对比、无损快照回滚或生成 Conventional Commit"),
    ("/eval [problem_id]", "运行本地自动化微型基准评测"),
    ("/clear | /status | /help", "清空对话重置快照、查看变更文件或打印帮助"),
)


class SlashCommandHandler:
    """Dispatches and executes REPL slash commands."""

    def __init__(self, engine: "TianCodeEngine") -> None:
        self.engine = engine
        self._handlers: Dict[str, Callable[[str], None]] = {
            "/diff": lambda _: TerminalFormatter.print_diffs(self.engine.snapshot_store.compute_diffs()),
            "/undo": lambda _: self._handle_undo(),
            "/commit": self._handle_commit,
            "/config": self._handle_config,
            "/map": lambda _: self._handle_map(),
            "/plan": lambda _: self._handle_plan(),
            "/skills": lambda _: self._handle_skills(),
            "/sessions": lambda _: self._handle_sessions(),
            "/switch": self._handle_switch,
            "/compact": lambda _: self._handle_compact(),
            "/context": lambda _: self._handle_context(),
            "/mode": self._handle_mode,
            "/init": lambda arg: handle_init(self.engine, arg),
            "/rules": lambda _: handle_rules(self.engine),
            "/cd": lambda arg: handle_cd(self.engine, arg),
            "/memory": lambda arg: handle_memory(self.engine, arg),
            "/remember": lambda arg: handle_remember(self.engine, arg),
            "/doctor": lambda _: self._handle_doctor(),
            "/cost": lambda _: show_cost_panel(self.engine),
            "/usage": lambda _: show_cost_panel(self.engine),
            "/eval": self._handle_eval,
            "/clear": lambda _: self._handle_clear(),
            "/help": lambda _: self._handle_help(),
            "/status": lambda _: self._handle_status(),
        }

    def is_slash_command(self, text: str) -> bool:
        """Check if input text is a recognized slash command."""
        return text.strip().split()[0].lower() in self._handlers if text.strip() else False

    def execute(self, text: str) -> bool:
        """Execute command with optional argument. Returns True if handled, False otherwise."""
        parts = text.strip().split(maxsplit=1)
        if not parts:
            return False
        handler = self._handlers.get(parts[0].lower())
        if handler:
            handler(parts[1] if len(parts) > 1 else "")
            return True
        return False

    def _apply_config_set(self, prop: str, val: str) -> None:
        apply_config_set(self.engine, prop, val)

    def _handle_undo(self) -> None:
        reverted = self.engine.snapshot_store.rollback()
        if not reverted:
            TerminalFormatter.print_info("当前没有可回滚的修改。")
            return
        TerminalFormatter.print_success("已完成时光倒流回滚：")
        for item in reverted:
            get_console().print(Text(f"  ⏪ {item}", style=TEXT_MUTED))
        try:
            active_id = getattr(self.engine, "active_session_id", "tiancode_repl")
            persisted = self.engine.session_store.load(active_id)
            if persisted:
                self.engine.session_store.save(
                    session_id=active_id, work_dir=persisted.metadata.work_dir,
                    messages=persisted.messages, plan=self.engine.planner.active_plan, snapshots=[],
                )
        except Exception:
            pass

    def _handle_commit(self, custom_msg: str = "") -> None:
        non_empty = [d for d in self.engine.snapshot_store.compute_diffs() if not d.is_empty]
        if not non_empty:
            TerminalFormatter.print_info("当前工作区干净，无待提交的修改。")
            return
        if not hasattr(self.engine.snapshot_store, "commit"):
            TerminalFormatter.print_warning("当前目录非 Git 仓库，无法执行 Git 提交。")
            return
        commit_msg = custom_msg.strip()
        if not commit_msg:
            TerminalFormatter.print_info("正在分析代码差异生成 Conventional Commit 消息...")
            commit_msg = self.engine.generate_commit_message("提交代码修改", "\n".join(d.diff_text for d in non_empty))
        commit_hash = self.engine.snapshot_store.commit(commit_msg)
        if commit_hash:
            TerminalFormatter.print_success(f"Git 提交成功！[{commit_hash}] {commit_msg}")
        else:
            TerminalFormatter.print_warning("Git 提交未成功，请检查工作区状态。")

    def _handle_config(self, arg: str) -> None:
        parts = arg.strip().split()
        subcmd = parts[0].lower() if parts else "show"
        if subcmd == "show":
            show_config_panel(self.engine)
        elif subcmd == "set" and len(parts) >= 3:
            apply_config_set(self.engine, parts[1].lower(), parts[2])
        else:
            TerminalFormatter.print_info("用法: /config show 或 /config set <key|url|model|max_chars|max_msgs|timeout|auto_heal> <值>")

    def _handle_map(self) -> None:
        from tiancode.core.repo_map import generate_repo_map
        print(generate_repo_map(self.engine.settings.work_dir))

    def _handle_plan(self) -> None:
        if not getattr(getattr(self.engine, "planner", None), "active_plan", None):
            TerminalFormatter.print_info("当前没有进行中的任务计划看板。遇到复杂任务时 AI 将自动创建。")
            return
        TerminalFormatter.print_task_plan(self.engine.planner.active_plan)

    def _handle_skills(self) -> None:
        skills = self.engine.skill_loader.load_all_skills()
        if not skills:
            TerminalFormatter.print_info("当前环境未安装任何扩展技能 (Skills)，可在 '.tiancode/skills/<name>/SKILL.md' 创建。")
            return
        headers = [("名称", {"style": f"bold {TEXT_PRIMARY}", "no_wrap": True}), ("作用域", {"style": SUCCESS_COLOR, "no_wrap": True}), ("描述", {"style": TEXT_MUTED})]
        rows = [(Text(s.name), "全局" if s.is_global else "工作区", Text(s.description)) for s in skills]
        print_table("✨ 已加载扩展技能", headers, rows)

    def _handle_eval(self, problem_arg: str = "") -> None:
        from tiancode.eval.runner import BenchmarkRunner
        pids = [problem_arg.strip()] if problem_arg.strip() else None
        TerminalFormatter.print_info("⚡ 正在启动 TianCode 自动化本地微型基准评测 (Micro Benchmark)...")
        print(BenchmarkRunner(engine=self.engine).run_suite(problem_ids=pids).format_table())

    def _handle_clear(self) -> None:
        self.engine.reset_session()
        TerminalFormatter.print_success("已清空对话记忆并重置所有文件快照。")

    def _handle_sessions(self) -> None:
        sessions = self.engine.session_store.list_sessions()
        if not sessions:
            TerminalFormatter.print_info("当前暂无历史持久化会话记录。")
            return
        headers = [
            ("会话 ID", {"style": f"bold {BRAND_COLOR}", "no_wrap": True}),
            ("任务标题 / 进度", {"style": TEXT_PRIMARY}),
            ("消耗统计", {"style": TEXT_MUTED, "justify": "right"}),
            ("更新时间", {"style": TEXT_SUBTLE, "no_wrap": True}),
        ]
        rows = [
            (
                Text(s.session_id),
                Text(f"{s.task_title or '(无标题)'}{f' ({s.steps_completed}/{s.steps_total})' if s.steps_total else ''}"),
                f"{s.total_tokens:,} tok (¥{s.estimated_cost:.4f})" if getattr(s, "total_tokens", 0) > 0 else f"{s.message_count} 条",
                s.updated_at[:19].replace("T", " "),
            )
            for s in sessions
        ]
        print_table("📜 TianCode 历史会话列表", headers, rows)
        TerminalFormatter.print_info("快捷切换会话: /switch <session_id>")

    def _handle_switch(self, target_id: str) -> None:
        target_id = target_id.strip()
        if not target_id:
            TerminalFormatter.print_info("用法: /switch <session_id>")
            return
        if self.engine.resume_session(target_id):
            TerminalFormatter.print_success(f"已成功切换并恢复至会话 [{target_id}]！")
            if self.engine.planner.active_plan:
                TerminalFormatter.print_task_plan(self.engine.planner.active_plan)
        else:
            TerminalFormatter.print_warning(f"未能找到或恢复会话 [{target_id}]，请先运行 /sessions 查看可用列表。")

    def _get_active_context_state(self) -> tuple[Optional[dict], list, int, int]:
        settings = getattr(self.engine, "settings", None)
        max_msgs = getattr(settings, "max_history_messages", 12)
        max_msgs = max_msgs if isinstance(max_msgs, int) else 12
        max_chars = getattr(settings, "max_context_chars", 40000)
        max_chars = max_chars if isinstance(max_chars, int) else 40000
        if not getattr(self.engine, "graph", None):
            return None, [], max_msgs, max_chars
        config = {"configurable": {"thread_id": getattr(self.engine, "active_session_id", "tiancode_repl")}}
        try:
            state = self.engine.graph.get_state(config)
            messages = state.values.get("messages", []) if state and getattr(state, "values", None) else []
        except Exception:
            messages = []
        return config, list(messages), max_msgs, max_chars

    def _handle_compact(self) -> None:
        config, messages, max_msgs, max_chars = self._get_active_context_state()
        if config is None or not messages:
            TerminalFormatter.print_info("当前上下文无历史交互消息，无需压缩。")
            return
        from tiancode.core.compactor import ContextCompactor
        result = ContextCompactor(max_retained_messages=max_msgs, max_context_chars=max_chars).compact_with_stats(messages)
        if result.compacted_chars < result.original_chars or len(result.messages) < len(messages):
            self.engine.graph.update_state(config, {"messages": result.messages})
            saved = max(0, result.original_chars - result.compacted_chars)
            TerminalFormatter.print_success(f"上下文压缩完成！已折叠 {result.folded_count} 条消息，释放约 {saved} 字符 (当前共 {len(result.messages)} 条)。")
        else:
            TerminalFormatter.print_info("当前上下文未超出预算阈值，无需压缩。")

    def _handle_context(self) -> None:
        _, messages, max_msgs, max_chars = self._get_active_context_state()
        from tiancode.core.doctor import analyze_context_usage, format_context_report
        print(format_context_report(analyze_context_usage(messages, max_retained_messages=max_msgs, max_context_chars=max_chars)))

    def _handle_mode(self, arg: str = "") -> None:
        guard = getattr(self.engine, "permission_guard", None)
        if guard is None or not hasattr(self.engine, "set_permission_mode"):
            TerminalFormatter.print_warning("当前引擎实例不支持动态切换权限模式。")
            return
        target = arg.strip()
        if not target:
            TerminalFormatter.print_info(f"🔐 当前权限模式: {guard.mode.value} (可选: ask | auto | read_only)")
            return
        try:
            new_mode = self.engine.set_permission_mode(target)
            TerminalFormatter.print_success(f"权限模式已切换为: {new_mode.value}")
        except ValueError as exc:
            TerminalFormatter.print_warning(str(exc))

    def _handle_doctor(self) -> None:
        from tiancode.core.doctor import format_diagnostics_report, run_environment_diagnostics
        settings = getattr(self.engine, "settings", None)
        checks = run_environment_diagnostics(getattr(settings, "work_dir", "."), settings, sessions_dir=getattr(getattr(self.engine, "session_store", None), "storage_dir", None))
        print(format_diagnostics_report(checks))

    def _handle_status(self) -> None:
        snapshots = self.engine.snapshot_store.get_all_snapshots()
        TerminalFormatter.print_info(f"当前已追踪修改文件数: {len(snapshots)}")
        for fname in snapshots:
            get_console().print(Text(f"  • {fname}", style=TEXT_MUTED))
        tracker = getattr(self.engine, "usage_tracker", None)
        if tracker:
            tokens, cost = tracker.get_session_stats()
            if tokens.total_tokens > 0:
                TerminalFormatter.print_info(f"会话累计消耗: {tokens.total_tokens:,} Tokens | 预估费用: ¥{cost.total_cost:.4f}")

    def _handle_help(self) -> None:
        table = Table(box=None, padding=(0, 2), show_header=False)
        table.add_column(style=f"bold {BRAND_COLOR}", no_wrap=True)
        table.add_column(style=TEXT_MUTED)
        for cmd, desc in _HELP_GROUPS:
            table.add_row(cmd, desc)
        table.add_row("exit / quit / :q", "安全退出 TianCode CLI 会话")
        console = get_console()
        console.print()
        console.print(Panel(table, title=Text("📖 TianCode 快捷斜杠指令", style=f"bold {BRAND_COLOR}"), title_align="left", box=box.ROUNDED, border_style=BORDER_MUTED, padding=(0, 1)))
        console.print()
