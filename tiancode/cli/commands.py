"""Slash command handlers for TianCode REPL."""

from typing import TYPE_CHECKING, Callable, Dict
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

    def _handle_config(self, arg: str) -> None:
        parts = arg.strip().split()
        subcmd = parts[0].lower() if parts else "show"

        if subcmd == "show":
            key_str = self.engine.settings.api_key
            masked_key = f"****{key_str[-4:]}" if len(key_str) >= 4 else "(未配置)"
            print("\n⚙️ ====== [TianCode 配置信息] ======")
            print(f"  API Key  : {masked_key}")
            print(f"  Base URL : {self.engine.settings.base_url}")
            print(f"  Model    : {self.engine.settings.model}")
            print(f"  配置文件 : {get_default_config_path()}")
            print("====================================\n")
            print("💡 快捷修改命令示例：")
            print("   /config set key sk-xxxx")
            print("   /config set url https://api.deepseek.com")
            print("   /config set model deepseek-chat\n")
            return

        if subcmd == "set" and len(parts) >= 3:
            prop = parts[1].lower()
            val = parts[2]
            if prop in ("key", "api_key"):
                self.engine.settings.api_key = val
                self.engine.settings.persist()
                self.engine.reload_llm()
                TerminalFormatter.print_success(f"已更新 API Key 并永久保存至 {get_default_config_path()}")
            elif prop in ("url", "base_url"):
                self.engine.settings.base_url = val
                self.engine.settings.persist()
                self.engine.reload_llm()
                TerminalFormatter.print_success(f"已更新 Base URL 为: {val}")
            elif prop in ("model", "model_name"):
                self.engine.settings.model = val
                self.engine.settings.persist()
                self.engine.reload_llm()
                TerminalFormatter.print_success(f"已更新 Model 为: {val}")
            else:
                TerminalFormatter.print_warning(f"未知属性 '{prop}'。支持: key, url, model")
        else:
            TerminalFormatter.print_info("用法: /config show 或 /config set <key|url|model> <值>")

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
            print(f"    消息条数: {s.message_count} 条 | 更新时间: {s.updated_at[:19].replace('T', ' ')}")
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

    def _handle_compact(self) -> None:
        if not hasattr(self.engine, "graph") or not self.engine.graph:
            TerminalFormatter.print_info("当前引擎尚未初始化执行图，无需压缩。")
            return

        session_id = getattr(self.engine, "active_session_id", "tiancode_repl")
        config = {"configurable": {"thread_id": session_id}}

        try:
            state = self.engine.graph.get_state(config)
            messages = state.values.get("messages", []) if state and hasattr(state, "values") and state.values else []
        except Exception:
            messages = []

        if not messages:
            TerminalFormatter.print_info("当前上下文无历史交互消息，无需压缩。")
            return

        from tiancode.core.compactor import ContextCompactor

        settings = getattr(self.engine, "settings", None)
        max_msgs = getattr(settings, "max_history_messages", 12)
        if not isinstance(max_msgs, int):
            max_msgs = 12
        max_chars = getattr(settings, "max_context_chars", 40000)
        if not isinstance(max_chars, int):
            max_chars = 40000

        compactor = ContextCompactor(
            max_retained_messages=max_msgs,
            max_context_chars=max_chars,
        )

        result = compactor.compact_with_stats(messages)
        if result.compacted_chars < result.original_chars or len(result.messages) < len(messages):
            self.engine.graph.update_state(config, {"messages": result.messages})
            saved_chars = max(0, result.original_chars - result.compacted_chars)
            TerminalFormatter.print_success(
                f"上下文压缩完成！已折叠 {result.folded_count} 条消息，释放约 {saved_chars} 字符 (当前共 {len(result.messages)} 条消息)。"
            )
        else:
            TerminalFormatter.print_info("当前上下文未超出预算阈值，无需压缩。")

    def _handle_status(self) -> None:
        snapshots = self.engine.snapshot_store.get_all_snapshots()
        TerminalFormatter.print_info(f"当前已追踪修改文件数: {len(snapshots)}")
        for fname in snapshots:
            print(f"  • {fname}")

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
  /diff    - 查看当前工作区代码变更补丁对比
  /undo    - 一键回滚并撤销当前所有未提交改动 (安全无损快照回滚)
  /commit  - 智能生成 Conventional Commit 并提交 (或指定: /commit feat: 自定义信息)
  /eval    - 运行自动化基准评测机 (可指定: /eval p1_lru_cache)
  /clear   - 清空上下文记忆并重置会话
  /status  - 查看当前修改的文件列表
  /help    - 打印此帮助菜单
  exit/:q  - 退出当前 CLI 会话
""")
