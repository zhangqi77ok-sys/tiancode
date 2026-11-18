"""Core LangGraph-powered orchestration engine for TianCode."""

from pathlib import Path
from typing import Any, Dict, Generator, List, Optional, Union
from langchain_core.messages import HumanMessage, SystemMessage
from langgraph.checkpoint.memory import InMemorySaver
from langgraph.graph import START, MessagesState, StateGraph
from langgraph.prebuilt import tools_condition

from tiancode.config.settings import Settings
from tiancode.core.git_store import GitSnapshotStore
from tiancode.core.history import InMemorySnapshotStore
from tiancode.core.llm_adapter import ReasonerChatOpenAI, ThinkTagStreamSplitter
from tiancode.core.permissions import ApprovalPromptFn, PermissionGuard, PermissionMode
from tiancode.core.prompts import TIANCODE_SYSTEM_PROMPT
from tiancode.core.state import prune_messages
from tiancode.core.stream_events import iter_message_events, persist_engine_session, resume_engine_session
from tiancode.core.tool_dispatcher import SafeToolDispatcher
from tiancode.domain.interfaces import SnapshotStore
from tiancode.tools.execution import create_execution_tools
from tiancode.tools.file_ops import create_file_tools


class TianCodeEngine:
    """Orchestrates LLM, Tools, and LangGraph state machine."""

    def __init__(
        self,
        settings: Settings,
        snapshot_store: Optional[SnapshotStore] = None,
        permission_mode: Optional[Union[PermissionMode, str]] = None,
        permission_prompt_fn: Optional[ApprovalPromptFn] = None,
    ) -> None:
        self.settings = settings
        self._custom_snapshot_store = snapshot_store is not None
        initial_mode = (
            PermissionMode.from_value(permission_mode)
            if permission_mode is not None
            else (PermissionMode.ASK if permission_prompt_fn is not None else PermissionMode.AUTO)
        )
        self.permission_guard = PermissionGuard(mode=initial_mode, prompt_fn=permission_prompt_fn)

        # Adaptive store selection: Use GitSnapshotStore if inside git repo, else fallback to InMemorySnapshotStore
        self.snapshot_store = snapshot_store if snapshot_store is not None else self._select_snapshot_store(settings.work_dir)

        # 2. Session Store, Cognitive Memory, Usage Tracker & Subagent Runner
        from tiancode.core.cognitive_memory import CognitiveMemoryStore
        from tiancode.core.planner import TaskPlanner
        from tiancode.core.session_store import SessionStore
        from tiancode.core.subagent import SubagentRunner
        from tiancode.core.usage_meter import TokenUsageTracker

        self.session_store = SessionStore()
        self.memory_store = CognitiveMemoryStore()
        self.usage_tracker = TokenUsageTracker()
        self.active_session_id: str = "tiancode_repl"

        self.planner = TaskPlanner()
        self.subagent_runner = SubagentRunner(
            work_dir=settings.work_dir,
            llm_client=None,
            usage_tracker=self.usage_tracker,
            model_name=settings.model,
        )

        # 3. Dynamic Skills & Project Rules Extension
        from tiancode.core.project_rules import ProjectRulesLoader
        from tiancode.skills.loader import SkillLoader
        self.skill_loader = SkillLoader(workspace_dir=settings.work_dir)
        self.rules_loader = ProjectRulesLoader()

        # 4. Assemble LangGraph Workflow
        self.memory = InMemorySaver()
        self.mcp_manager = None
        self.reload_llm()

        # Track config file modification time for JIT dynamic sync
        self._last_config_mtime: float = 0.0
        self._init_config_mtime()

    def _select_snapshot_store(self, work_dir: Path) -> SnapshotStore:
        git_store = GitSnapshotStore(work_dir=work_dir)
        return git_store if git_store.is_git_repo() else InMemorySnapshotStore(work_dir=work_dir)

    def switch_workspace(self, target_dir: Union[Path, str]) -> Path:
        """Validate and atomically re-anchor sandbox, snapshot store, subagent, skills, and tools."""
        target_path = Path(target_dir).expanduser().resolve()
        if not target_path.exists() or not target_path.is_dir():
            raise ValueError(f"目标工作区目录不存在或不是有效目录: {target_dir}")

        self.settings.work_dir = target_path
        if not getattr(self, "_custom_snapshot_store", False):
            self.snapshot_store = self._select_snapshot_store(target_path)
        self.subagent_runner.work_dir = target_path
        self.skill_loader.workspace_dir = target_path
        self.reload_llm()
        if hasattr(self, "memory_store"):
            self.memory_store.touch_workspace(target_path, session_id=self.active_session_id)
        return target_path

    def _get_config_path(self, config_path: Optional[Path] = None) -> Path:
        from tiancode.config.settings import get_default_config_path
        return config_path or get_default_config_path()

    def _init_config_mtime(self, config_path: Optional[Path] = None) -> None:
        cfg = self._get_config_path(config_path)
        if cfg.exists():
            try:
                self._last_config_mtime = cfg.stat().st_mtime
            except Exception:
                self._last_config_mtime = 0.0

    def _register_tools(self) -> None:
        """Rebuild all engine tool closures from current settings."""
        from tiancode.tools.mcp_ops import sync_engine_mcp
        from tiancode.tools.memory_ops import create_memory_tools
        from tiancode.tools.plan_ops import create_plan_tools
        from tiancode.tools.subagent_ops import create_subagent_tools
        from tiancode.tools.web_ops import create_web_tools

        self.tools = [
            *create_file_tools(self.snapshot_store, self.settings.work_dir, auto_heal=self.settings.auto_heal),
            *create_execution_tools(self.settings.work_dir, self.settings.timeout_seconds),
            *create_plan_tools(self.planner),
            *create_subagent_tools(self.subagent_runner),
            *create_memory_tools(self),
            *create_web_tools(),
            *sync_engine_mcp(self),
        ]

    def sync_config_if_changed(self, config_path: Optional[Path] = None) -> bool:
        """Inspect config file on disk; if modified, hot-reload settings and LLM immediately."""
        cfg_file = self._get_config_path(config_path)
        if not cfg_file.exists():
            return False

        try:
            current_mtime = cfg_file.stat().st_mtime
        except Exception:
            return False

        if current_mtime <= self._last_config_mtime:
            return False

        from tiancode.config.settings import load_user_config
        new_cfg = load_user_config(cfg_file)
        candidate: Dict[str, Any] = {
            "api_key": new_cfg.get("api_key", ""),
            "base_url": new_cfg.get("base_url", "https://api.deepseek.com"),
            "model": new_cfg.get("model", "deepseek-chat"),
        }
        for key in ("timeout_seconds", "max_history_messages", "max_context_chars"):
            val = new_cfg.get(key)
            candidate[key] = val if (isinstance(val, int) and not isinstance(val, bool) and val > 0) else getattr(self.settings, key)
        auto_heal_val = new_cfg.get("auto_heal")
        candidate["auto_heal"] = auto_heal_val if isinstance(auto_heal_val, bool) else self.settings.auto_heal

        differs = any(getattr(self.settings, k) != v for k, v in candidate.items())
        self._last_config_mtime = current_mtime

        if differs:
            for k, v in candidate.items():
                setattr(self.settings, k, v)
            self.reload_llm()
            return True

        return False

    def reload_llm(self) -> None:
        """Re-initialize LLM client, tool closures, and state machine when settings update."""
        self._register_tools()
        llm_timeout = max(30.0, float(self.settings.timeout_seconds) * 2)
        self.llm = ReasonerChatOpenAI(
            model=self.settings.model,
            api_key=self.settings.api_key or "sk-dummy",
            base_url=self.settings.base_url,
            temperature=self.settings.temperature,
            timeout=llm_timeout,
            max_retries=2,
            stream_usage=True,
        )
        self.subagent_runner.llm = self.llm
        self.subagent_runner.model_name = self.settings.model
        self.llm_with_tools = self.llm.bind_tools(self.tools)
        self.graph = self._build_graph()

    def _build_graph(self):
        def brain_node(state: MessagesState) -> Dict[str, Any]:
            max_chars = getattr(self.settings, "max_context_chars", 40000)
            if not isinstance(max_chars, int):
                max_chars = 40000
            trimmed = prune_messages(state["messages"], max_retained=self.settings.max_history_messages, max_context_chars=max_chars)
            skills_catalog = self.skill_loader.format_prompt_catalog()
            rules_context = self.rules_loader.format_prompt_rules(self.settings.work_dir) if hasattr(self, "rules_loader") else ""
            memory_context = self.memory_store.format_priming_context(self.settings.work_dir, max_chars=1200) if hasattr(self, "memory_store") else ""
            plan_context = ""
            if self.planner.active_plan and not self.planner.active_plan.is_finished:
                plan_context = "\n" + self.planner.format_worker_prompt_context(self.planner.active_plan)

            full_prompt = (
                TIANCODE_SYSTEM_PROMPT
                + (f"\n{rules_context}" if rules_context else "")
                + (f"\n{skills_catalog}" if skills_catalog else "")
                + (f"\n{memory_context}" if memory_context else "")
                + plan_context
            )
            sys_msg = SystemMessage(content=full_prompt)
            response = self.llm_with_tools.invoke([sys_msg] + trimmed)
            if hasattr(self, "usage_tracker") and getattr(response, "usage_metadata", None):
                self.usage_tracker.record_turn(model=self.settings.model, usage_metadata=response.usage_metadata)
            return {"messages": [response]}

        self.tool_dispatcher = SafeToolDispatcher(self.tools, permission_guard=self.permission_guard)
        builder = StateGraph(MessagesState)
        builder.add_node("brain", brain_node)
        builder.add_node("tools", lambda s: self.tool_dispatcher(s))

        builder.add_edge(START, "brain")
        builder.add_conditional_edges("brain", tools_condition)
        builder.add_edge("tools", "brain")

        return builder.compile(checkpointer=self.memory)

    def _persist_session_state(self, thread_id: str, config: Dict[str, Any]) -> None:
        persist_engine_session(self, thread_id, config)

    def _iter_message_events(self, chunk: Any, meta: Any, seen_tools: set, splitter: ThinkTagStreamSplitter) -> Generator[Dict[str, Any], None, None]:
        yield from iter_message_events(chunk, meta, seen_tools, splitter)

    def stream_task(self, prompt: str, thread_id: str = "tiancode_default") -> Generator[Dict[str, Any], None, None]:
        """Stream real-time thinking, tokens, tool lifecycle events, and turn usage telemetry."""
        if self.sync_config_if_changed():
            from tiancode.cli.formatter import TerminalFormatter
            TerminalFormatter.print_info(f"检测到外部配置文件已更新，已自动动态重载连接 ({self.settings.model})！")

        config = {"configurable": {"thread_id": thread_id}, "recursion_limit": 50}
        seen_tools: set = set()
        splitter = ThinkTagStreamSplitter()
        before_tokens = self.usage_tracker.get_session_stats()[0].total_tokens if hasattr(self, "usage_tracker") else 0

        try:
            for item in self.graph.stream({"messages": [HumanMessage(content=prompt)]}, config=config, stream_mode="messages"):
                if isinstance(item, tuple) and len(item) == 2:
                    yield from self._iter_message_events(item[0], item[1], seen_tools, splitter)
                elif isinstance(item, dict):
                    yield item
            for kind, piece in splitter.flush():
                yield {"type": kind, "content": piece}

            if hasattr(self, "usage_tracker"):
                tokens, cost = self.usage_tracker.get_session_stats()
                turn_tokens = max(0, tokens.total_tokens - before_tokens)
                if turn_tokens > 0:
                    yield {"type": "usage_summary", "turn_tokens": turn_tokens, "session_tokens": tokens.total_tokens, "session_cost": cost.total_cost}
        finally:
            self._persist_session_state(thread_id, config)

    def resume_session(self, session_id: str) -> bool:
        """Resume a saved session from disk and restore messages, TaskPlan, and file snapshots."""
        return resume_engine_session(self, session_id)

    def set_permission_mode(self, mode: Union[PermissionMode, str], prompt_fn: Optional[ApprovalPromptFn] = None) -> PermissionMode:
        """Dynamically switch execution permission mode (ask / auto / read_only)."""
        return self.permission_guard.set_mode(mode, prompt_fn=prompt_fn)

    def reset_session(self) -> None:
        """Clear conversation memory, recorded file snapshots, active plan, and usage metrics."""
        self.memory.storage.clear()
        self.snapshot_store.clear()
        self.planner.active_plan = None
        self.permission_guard.clear_session_approvals()
        if hasattr(self, "usage_tracker"):
            self.usage_tracker.reset()
        if hasattr(self, "tool_dispatcher"):
            self.tool_dispatcher.reset_loop_guard()

    def close(self) -> None:
        """Release external transports and managed child processes."""
        from tiancode.tools.mcp_ops import close_engine_mcp
        close_engine_mcp(self)

    def generate_commit_message(self, task_description: str, diff_text: str) -> str:
        """Use LLM to generate a conventional commit message based on task and diff."""
        prompt = (
            "请根据以下任务说明与代码变更，生成一条符合 Conventional Commits 规范的简短单行 Git Commit 消息（例如：feat: add login）。"
            f"直接输出单行 Commit 文本，严禁包含引号或 markdown。\n\n任务说明：{task_description}\n代码差异：\n{diff_text[:2000]}"
        )
        try:
            return self.llm.invoke(prompt).content.strip().splitlines()[0].strip("`'\" ")
        except Exception:
            return f"feat: {task_description[:40]}"

