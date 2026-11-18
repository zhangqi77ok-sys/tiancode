"""Slash command handlers for TianCode REPL."""

from typing import TYPE_CHECKING, Callable, Dict, Optional
from tiancode.cli.formatter import TerminalFormatter
from tiancode.config.settings import get_default_config_path

if TYPE_CHECKING:
    from tiancode.core.engine import TianCodeEngine


class SlashCommandHandler:
    """Dispatches and executes REPL slash commands."""

    def __init__(self, engine: "TianCodeEngine") -> None:
        self.engine = engine
        self._handlers: Dict[str, Callable[[str], None]] = {
            "/diff": lambda _: self._handle_diff(),
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
            "/doctor": lambda _: self._handle_doctor(),
            "/cost": lambda _: self._handle_cost(),
            "/usage": lambda _: self._handle_cost(),
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
        cmd = parts[0].lower()
        arg = parts[1] if len(parts) > 1 else ""
        handler = self._handlers.get(cmd)
        if handler:
            handler(arg)
            return True
        return False

    def _handle_diff(self) -> None:
        diffs = self.engine.snapshot_store.compute_diffs()
        TerminalFormatter.print_diffs(diffs)

    def _handle_undo(self) -> None:
        reverted = self.engine.snapshot_store.rollback()
        if reverted:
            TerminalFormatter.print_success("已完成时光倒流回滚：")
            for item in reverted:
                print(f"  ⏪ {item}")

            # Clear persisted snapshots from active session on disk
            try:
                active_id = getattr(self.engine, "active_session_id", "tiancode_repl")
                persisted = self.engine.session_store.load(active_id)
                if persisted:
                    self.engine.session_store.save(
                        session_id=active_id,
                        work_dir=persisted.metadata.work_dir,
                        messages=persisted.messages,
                        plan=self.engine.planner.active_plan,
                        snapshots=[],
                    )
            except Exception:
                pass
        else:
            TerminalFormatter.print_info("当前没有可回滚的修改。")

    def _handle_commit(self, custom_msg: str = "") -> None:
        diffs = self.engine.snapshot_store.compute_diffs()
        non_empty = [d for d in diffs if not d.is_empty]
        if not non_empty:
            TerminalFormatter.print_info("当前工作区干净，无待提交的修改。")
            return

        if not hasattr(self.engine.snapshot_store, "commit"):
            TerminalFormatter.print_warning("当前目录非 Git 仓库，无法执行 Git 提交。")
            return

        commit_msg = custom_msg.strip()
        if not commit_msg:
            TerminalFormatter.print_info("正在分析代码差异生成 Conventional Commit 消息...")
            combined_diff = "\n".join(d.diff_text for d in non_empty)
            commit_msg = self.engine.generate_commit_message("提交代码修改", combined_diff)

        commit_hash = self.engine.snapshot_store.commit(commit_msg)
        if commit_hash:
            TerminalFormatter.print_success(f"Git 提交成功！[{commit_hash}] {commit_msg}")
        else:
            TerminalFormatter.print_warning("Git 提交未成功，请检查工作区状态。")

    def _apply_config_set(self, prop: str, val: str) -> None:
        """Validate and persist a single /config set property update uniformly."""
        s = self.engine.settings
        str_map = {"key": "api_key", "api_key": "api_key", "url": "base_url", "base_url": "base_url", "model": "model", "model_name": "model"}
        if prop in str_map:
            setattr(s, str_map[prop], val)
            s.persist()
            self.engine.reload_llm()
            TerminalFormatter.print_success(f"已更新 {str_map[prop]} 并持久化保存。")
            return

        int_limits = {
            "max_chars": ("max_context_chars", 500),
            "max_context_chars": ("max_context_chars", 500),
            "max_msgs": ("max_history_messages", 2),
            "max_history_messages": ("max_history_messages", 2),
            "timeout": ("timeout_seconds", 1),
            "timeout_seconds": ("timeout_seconds", 1),
        }
        if prop in int_limits:
            attr, min_val = int_limits[prop]
            try:
                num = int(val)
            except ValueError:
                TerminalFormatter.print_warning(f"参数值 '{val}' 必须为正整数。")
                return
            if num < min_val:
                TerminalFormatter.print_warning(f"参数 {prop} 不能小于 {min_val}: {num}")
                return
            setattr(s, attr, num)
            s.persist()
            if attr == "timeout_seconds":
                self.engine.reload_llm()
            TerminalFormatter.print_success(f"已更新 {attr} = {num} 并持久化保存。")
            return

        if prop in ("auto_heal", "heal"):
            low = val.lower()
            if low not in ("true", "false", "1", "0", "on", "off", "yes", "no"):
                TerminalFormatter.print_warning("布尔值仅支持 true/false/on/off。")
                return
            s.auto_heal = low in ("true", "1", "on", "yes")
            s.persist()
            self.engine.reload_llm()
            TerminalFormatter.print_success(f"已更新 auto_heal = {s.auto_heal} 并持久化保存。")
            return

        TerminalFormatter.print_warning(f"未知属性 '{prop}'。支持: key, url, model, max_chars, max_msgs, timeout, auto_heal")

    def _handle_config(self, arg: str) -> None:
        parts = arg.strip().split()
        subcmd = parts[0].lower() if parts else "show"

        if subcmd == "show":
            s = self.engine.settings
            masked_key = f"****{s.api_key[-4:]}" if len(s.api_key) >= 4 else "(未配置)"
            print("\n⚙️ ====== [TianCode 配置信息] ======")
            print(f"  API Key  : {masked_key}")
            print(f"  Base URL : {s.base_url}")
            print(f"  Model    : {s.model}")
            print(f"  预算阈值 : {getattr(s, 'max_history_messages', 12)} 条 / {getattr(s, 'max_context_chars', 40000):,} 字符")
            print(f"  自愈开关 : {getattr(s, 'auto_heal', True)} | 超时: {getattr(s, 'timeout_seconds', 15)}s")
            guard = getattr(self.engine, "permission_guard", None)
            if guard is not None:
                print(f"  权限模式 : {guard.mode.value}")
            print(f"  配置文件 : {get_default_config_path()}")
            print("====================================\n")
            return

        if subcmd == "set" and len(parts) >= 3:
            self._apply_config_set(parts[1].lower(), parts[2])
        else:
            TerminalFormatter.print_info("用法: /config show 或 /config set <key|url|model|max_chars|max_msgs|timeout|auto_heal> <值>")

    def _handle_map(self) -> None:
        from tiancode.core.repo_map import generate_repo_map
        map_text = generate_repo_map(self.engine.settings.work_dir)
        print(map_text)

    def _handle_plan(self) -> None:
        if not hasattr(self.engine, "planner") or not self.engine.planner.active_plan:
            TerminalFormatter.print_info("当前没有进行中的任务计划看板。遇到复杂任务时 AI 将自动创建。")
            return
        TerminalFormatter.print_task_plan(self.engine.planner.active_plan)

    def _handle_skills(self) -> None:
        skills = self.engine.skill_loader.load_all_skills()
        if not skills:
            TerminalFormatter.print_info("当前环境未安装任何扩展技能 (Skills)。")
            print("💡 提示：可在当前工程创建 '.tiancode/skills/<name>/SKILL.md' 进行零代码扩展！\n")
            return

        TerminalFormatter.print_info(f"✨ 当前已加载 {len(skills)} 个扩展技能：")
        for s in skills:
            scope = "全局" if s.is_global else "工作区"
            print(f"  • {TerminalFormatter.BOLD}{s.name}{TerminalFormatter.RESET} [{scope}]: {s.description}")
            print(f"    路径: {s.file_path}")
        print()

    def _handle_eval(self, problem_arg: str = "") -> None:
        from tiancode.eval.runner import BenchmarkRunner
        runner = BenchmarkRunner(engine=self.engine)
        pids = [problem_arg.strip()] if problem_arg.strip() else None
        TerminalFormatter.print_info("⚡ 正在启动 TianCode 自动化本地微型基准评测 (Micro Benchmark)...")
        report = runner.run_suite(problem_ids=pids)
        print(report.format_table())

    def _handle_clear(self) -> None:
        self.engine.reset_session()
        TerminalFormatter.print_success("已清空对话记忆并重置所有文件快照。")

    def _handle_sessions(self) -> None:
        sessions = self.engine.session_store.list_sessions()
        if not sessions:
            TerminalFormatter.print_info("当前暂无历史持久化会话记录。")
            return
        print(f"\n{TerminalFormatter.CYAN}{TerminalFormatter.BOLD}📜 ====== [TianCode 历史会话列表] ======{TerminalFormatter.RESET}")
        for s in sessions:
            progress = f"(进度: {s.steps_completed}/{s.steps_total} 步)" if s.steps_total else ""
            title = f": {s.task_title}" if s.task_title else ""
            print(f"  • {TerminalFormatter.BOLD}[{s.session_id}]{TerminalFormatter.RESET}{title} {progress}")
            print(f"    工作目录: {s.work_dir}")
            cost_info = f" | 消耗: {s.total_tokens:,} Tokens (¥{s.estimated_cost:.4f})" if getattr(s, "total_tokens", 0) > 0 else ""
            print(f"    消息条数: {s.message_count} 条{cost_info} | 更新时间: {s.updated_at[:19].replace('T', ' ')}")
        print(f"{TerminalFormatter.CYAN}========================================{TerminalFormatter.RESET}\n")
        print("💡 快捷切换会话: /switch <session_id>\n")

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
        """Read active thread state messages and validated budget limits."""
        settings = getattr(self.engine, "settings", None)
        max_msgs = getattr(settings, "max_history_messages", 12)
        if not isinstance(max_msgs, int):
            max_msgs = 12
        max_chars = getattr(settings, "max_context_chars", 40000)
        if not isinstance(max_chars, int):
            max_chars = 40000

        if not hasattr(self.engine, "graph") or not self.engine.graph:
            return None, [], max_msgs, max_chars

        session_id = getattr(self.engine, "active_session_id", "tiancode_repl")
        config = {"configurable": {"thread_id": session_id}}
        try:
            state = self.engine.graph.get_state(config)
            messages = state.values.get("messages", []) if state and hasattr(state, "values") and state.values else []
        except Exception:
            messages = []
        return config, list(messages), max_msgs, max_chars

    def _handle_compact(self) -> None:
        config, messages, max_msgs, max_chars = self._get_active_context_state()
        if config is None:
            TerminalFormatter.print_info("当前引擎尚未初始化执行图，无需压缩。")
            return
        if not messages:
            TerminalFormatter.print_info("当前上下文无历史交互消息，无需压缩。")
            return

        from tiancode.core.compactor import ContextCompactor
        compactor = ContextCompactor(max_retained_messages=max_msgs, max_context_chars=max_chars)
        result = compactor.compact_with_stats(messages)
        if result.compacted_chars < result.original_chars or len(result.messages) < len(messages):
            self.engine.graph.update_state(config, {"messages": result.messages})
            saved_chars = max(0, result.original_chars - result.compacted_chars)
            TerminalFormatter.print_success(
                f"上下文压缩完成！已折叠 {result.folded_count} 条消息，释放约 {saved_chars} 字符 (当前共 {len(result.messages)} 条消息)。"
            )
        else:
            TerminalFormatter.print_info("当前上下文未超出预算阈值，无需压缩。")

    def _handle_context(self) -> None:
        _, messages, max_msgs, max_chars = self._get_active_context_state()
        from tiancode.core.doctor import analyze_context_usage, format_context_report
        stats = analyze_context_usage(messages, max_retained_messages=max_msgs, max_context_chars=max_chars)
        print(format_context_report(stats))

    def _handle_mode(self, arg: str = "") -> None:
        """View or switch runtime HITL permission mode (ask / auto / read_only)."""
        guard = getattr(self.engine, "permission_guard", None)
        if guard is None or not hasattr(self.engine, "set_permission_mode"):
            TerminalFormatter.print_warning("当前引擎实例不支持动态切换权限模式。")
            return

        target = arg.strip()
        if not target:
            print(f"\n🔐 当前权限模式: {guard.mode.value} (可选: ask | auto | read_only)\n")
            return

        try:
            new_mode = self.engine.set_permission_mode(target)
            TerminalFormatter.print_success(f"权限模式已切换为: {new_mode.value}")
        except ValueError as exc:
            TerminalFormatter.print_warning(str(exc))

    def _handle_doctor(self) -> None:
        from tiancode.core.doctor import format_diagnostics_report, run_environment_diagnostics
        settings = getattr(self.engine, "settings", None)
        work_dir = getattr(settings, "work_dir", ".")
        sess_dir = getattr(getattr(self.engine, "session_store", None), "storage_dir", None)
        checks = run_environment_diagnostics(work_dir, settings, sessions_dir=sess_dir)
        print(format_diagnostics_report(checks))

    def _handle_cost(self) -> None:
        tracker = getattr(self.engine, "usage_tracker", None)
        if not tracker:
            TerminalFormatter.print_info("当前引擎未启用 Token 计量器。")
            return

        tokens, cost = tracker.get_session_stats()
        model = getattr(getattr(self.engine, "settings", None), "model", "unknown")
        if not isinstance(model, str):
            model = "unknown"

        from tiancode.core.usage_meter import get_model_pricing
        pricing = get_model_pricing(model)
        pricing_label = pricing.display_name if pricing else "本地模型/未配置费率"

        print(f"\n📊 ====== [TianCode 会话 Token 与成本账单] ======")
        print(f"  • 当前模型: {model} ({pricing_label})")
        print(f"  • 推理轮次: {tokens.turn_count} 轮")
        print(
            f"  • 输入 Tokens: {tokens.total_input_tokens:,} "
            f"(其中缓存命中: {tokens.cache_read_tokens:,}, 命中率: {tokens.cache_hit_rate:.1f}%)"
        )
        print(f"  • 输出 Tokens: {tokens.output_tokens:,}")
        print(f"  • 累计消耗: {tokens.total_tokens:,} Tokens")
        if cost.is_known_pricing:
            saved_str = f" (命中缓存节省约: ¥{cost.saved_by_cache:.4f})" if cost.saved_by_cache > 0 else ""
            print(f"  • 预估总费用: ¥{cost.total_cost:.4f}{saved_str}")
        else:
            print("  • 预估总费用: ¥0.0000 (本地/自定义模型无计费)")
        print("================================================\n")

    def _handle_status(self) -> None:
        snapshots = self.engine.snapshot_store.get_all_snapshots()
        TerminalFormatter.print_info(f"当前已追踪修改文件数: {len(snapshots)}")
        for fname in snapshots:
            print(f"  • {fname}")

        tracker = getattr(self.engine, "usage_tracker", None)
        if tracker:
            tokens, cost = tracker.get_session_stats()
            if tokens.total_tokens > 0:
                TerminalFormatter.print_info(
                    f"会话累计消耗: {tokens.total_tokens:,} Tokens | 预估费用: ¥{cost.total_cost:.4f}"
                )

    def _handle_help(self) -> None:
        print("""
【TianCode 快捷斜杠指令】
  /config   - 查看或修改模型连接配置 (/config show, /config set key xxx)
  /map      - 查看当前项目的 AST 代码拓扑全景图 (Repo Map)
  /plan     - 查看当前活跃的 Planner-Worker 任务执行看板
  /skills   - 查看已安装的扩展技能列表 (Skills)
  /sessions - 查看磁盘持久化的历史会话列表
  /switch   - 快速切换恢复至指定历史会话 (/switch <session_id>)
  /compact  - 立即执行上下文智能压缩与 Token 预算折叠，释放上下文窗口
  /context  - 可视化查看当前上下文窗口占用与角色字符占比体检
  /mode     - 查看或切换安全权限模式 (/mode [ask|auto|read_only])
  /doctor   - 一键执行环境配置、目录权限、Git 与 pytest 健康自检
  /cost     - 查看当前会话 Token 消耗、缓存命中率与预估费用账单 (别名 /usage)
  /diff    - 查看当前工作区代码变更补丁对比
  /undo    - 一键回滚并撤销当前所有未提交改动 (安全无损快照回滚)
  /commit  - 智能生成 Conventional Commit 并提交 (或指定: /commit feat: 自定义信息)
  /eval    - 运行自动化基准评测机 (可指定: /eval p1_lru_cache)
  /clear   - 清空上下文记忆并重置会话
  /status  - 查看当前修改的文件列表
  /help    - 打印此帮助菜单
  exit/:q  - 退出当前 CLI 会话
""")
