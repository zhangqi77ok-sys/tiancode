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
        TerminalFormatter.print_info("⚡ 正在启动 TianCode 自动化基准评测 (SWE-bench 体系)...")
        report = runner.run_suite(problem_ids=pids)
        print(report.format_table())

    def _handle_clear(self) -> None:
        self.engine.reset_session()
        TerminalFormatter.print_success("已清空对话记忆并重置所有文件快照。")

    def _handle_status(self) -> None:
        snapshots = self.engine.snapshot_store.get_all_snapshots()
        TerminalFormatter.print_info(f"当前已追踪修改文件数: {len(snapshots)}")
        for fname in snapshots:
            print(f"  • {fname}")

    def _handle_help(self) -> None:
        print("""
【TianCode 快捷斜杠指令】
  /config  - 查看或修改模型连接配置 (/config show, /config set key xxx)
  /map     - 查看当前项目的 AST 代码拓扑全景图 (Repo Map)
  /plan    - 查看当前活跃的 Planner-Worker 任务执行看板
  /skills  - 查看已安装的扩展技能列表 (Skills)
  /diff    - 查看当前工作区代码变更补丁对比
  /undo    - 一键回滚并撤销当前所有未提交改动 (Git Restore)
  /commit  - 智能生成 Conventional Commit 并提交 (或指定: /commit feat: 自定义信息)
  /eval    - 运行自动化基准评测机 (可指定: /eval p1_lru_cache)
  /clear   - 清空上下文记忆并重置会话
  /status  - 查看当前修改的文件列表
  /help    - 打印此帮助菜单
  exit/:q  - 退出当前 CLI 会话
""")
