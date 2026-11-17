"""Core LangGraph-powered orchestration engine for TianCode."""

from pathlib import Path
from typing import Any, Dict, Generator, List, Optional
from langchain_core.messages import HumanMessage, SystemMessage
from langchain_openai import ChatOpenAI
from langgraph.checkpoint.memory import InMemorySaver
from langgraph.graph import START, MessagesState, StateGraph
from langgraph.prebuilt import ToolNode, tools_condition

from tiancode.config.settings import Settings
from tiancode.core.git_store import GitSnapshotStore
from tiancode.core.history import InMemorySnapshotStore
from tiancode.core.prompts import TIANCODE_SYSTEM_PROMPT
from tiancode.core.state import prune_messages
from tiancode.domain.interfaces import SnapshotStore
from tiancode.tools.execution import create_execution_tools
from tiancode.tools.file_ops import create_file_tools


class TianCodeEngine:
    """Orchestrates LLM, Tools, and LangGraph state machine."""

    def __init__(
        self,
        settings: Settings,
        snapshot_store: Optional[SnapshotStore] = None,
    ) -> None:
        self.settings = settings

        # Adaptive store selection: Use GitSnapshotStore if inside git repo, else fallback to InMemorySnapshotStore
        if snapshot_store is not None:
            self.snapshot_store = snapshot_store
        else:
            git_store = GitSnapshotStore(work_dir=settings.work_dir)
            self.snapshot_store = git_store if git_store.is_git_repo() else InMemorySnapshotStore(work_dir=settings.work_dir)

        # 2. Build Tools, Planner & Subagent Runner
        from tiancode.core.planner import TaskPlanner
        from tiancode.core.subagent import SubagentRunner
        from tiancode.tools.plan_ops import create_plan_tools
        from tiancode.tools.subagent_ops import create_subagent_tools

        self.planner = TaskPlanner()
        self.subagent_runner = SubagentRunner(work_dir=settings.work_dir, llm_client=None)

        self.tools = []
        self.tools.extend(create_file_tools(self.snapshot_store, settings.work_dir, auto_heal=settings.auto_heal))
        self.tools.extend(create_execution_tools(settings.work_dir, settings.timeout_seconds))
        self.tools.extend(create_plan_tools(self.planner))
        self.tools.extend(create_subagent_tools(self.subagent_runner))

        # 3. Dynamic Skills Extension
        from tiancode.skills.loader import SkillLoader
        self.skill_loader = SkillLoader(workspace_dir=settings.work_dir)

        # 4. Session Persistence Store
        from tiancode.core.session_store import SessionStore
        self.session_store = SessionStore()
        self.active_session_id: str = "tiancode_repl"

        # 5. Assemble LangGraph Workflow
        self.memory = InMemorySaver()
        self.reload_llm()

        # Track config file modification time for JIT dynamic sync
        self._last_config_mtime: float = 0.0
        self._init_config_mtime()

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
        new_key = new_cfg.get("api_key", "")
        new_url = new_cfg.get("base_url", "https://api.deepseek.com")
        new_model = new_cfg.get("model", "deepseek-chat")

        differs = (
            new_key != self.settings.api_key
            or new_url != self.settings.base_url
            or new_model != self.settings.model
        )

        self._last_config_mtime = current_mtime

        if differs:
            self.settings.api_key = new_key
            self.settings.base_url = new_url
            self.settings.model = new_model
            self.reload_llm()
            return True

        return False

    def reload_llm(self) -> None:
        """Re-initialize LLM client and rebuild state machine when settings are updated dynamically."""
        self.llm = ChatOpenAI(
            model=self.settings.model,
            api_key=self.settings.api_key or "sk-dummy",
            base_url=self.settings.base_url,
            temperature=self.settings.temperature,
        )
        self.subagent_runner.llm = self.llm
        self.llm_with_tools = self.llm.bind_tools(self.tools)
        self.graph = self._build_graph()

    def _build_graph(self):
        def brain_node(state: MessagesState) -> Dict[str, Any]:
            trimmed = prune_messages(state["messages"], max_retained=self.settings.max_history_messages)
            skills_catalog = self.skill_loader.format_prompt_catalog()
            plan_context = ""
            if self.planner.active_plan and not self.planner.active_plan.is_finished:
                plan_context = "\n" + self.planner.format_worker_prompt_context(self.planner.active_plan)

            full_prompt = TIANCODE_SYSTEM_PROMPT + (f"\n{skills_catalog}" if skills_catalog else "") + plan_context
            sys_msg = SystemMessage(content=full_prompt)
            response = self.llm_with_tools.invoke([sys_msg] + trimmed)
            return {"messages": [response]}

        builder = StateGraph(MessagesState)
        builder.add_node("brain", brain_node)
        builder.add_node("tools", ToolNode(self.tools))

        builder.add_edge(START, "brain")
        builder.add_conditional_edges("brain", tools_condition)
        builder.add_edge("tools", "brain")

        return builder.compile(checkpointer=self.memory)

    def stream_task(
        self, prompt: str, thread_id: str = "tiancode_default"
    ) -> Generator[Dict[str, Any], None, None]:
        """Stream real-time tokens and tool lifecycle events from the underlying graph."""
        if self.sync_config_if_changed():
            from tiancode.cli.formatter import TerminalFormatter
            TerminalFormatter.print_info(f"检测到外部配置文件已更新，已自动动态重载连接 ({self.settings.model})！")

        config = {"configurable": {"thread_id": thread_id}}
        seen_tools = set()

        try:
            for item in self.graph.stream(
                {"messages": [HumanMessage(content=prompt)]},
                config=config,
                stream_mode="messages",
            ):
                if isinstance(item, tuple) and len(item) == 2:
                    chunk, meta = item
                    node = meta.get("langgraph_node") if isinstance(meta, dict) else None

                    if node == "brain":
                        if getattr(chunk, "content", None):
                            yield {"type": "token", "content": chunk.content}

                        if getattr(chunk, "tool_call_chunks", None):
                            for tc in chunk.tool_call_chunks:
                                name = tc.get("name")
                                call_id = tc.get("id") or tc.get("index")
                                if name and call_id not in seen_tools:
                                    seen_tools.add(call_id)
                                    yield {"type": "tool_start", "name": name, "args": tc.get("args", "")}
                        elif getattr(chunk, "tool_calls", None):
                            for tc in chunk.tool_calls:
                                call_id = tc.get("id") or tc.get("name")
                                if call_id not in seen_tools:
                                    seen_tools.add(call_id)
                                    yield {"type": "tool_start", "name": tc.get("name"), "args": tc.get("args", {})}

                    elif node == "tools":
                        name = getattr(chunk, "name", "tool")
                        content = getattr(chunk, "content", "")
                        yield {"type": "tool_end", "name": name, "content": content}

                elif isinstance(item, dict):
                    yield item
        finally:
            try:
                state = self.graph.get_state(config)
                all_messages = state.values.get("messages", [])
                self.session_store.save(
                    session_id=thread_id,
                    work_dir=str(self.settings.work_dir),
                    messages=all_messages,
                    plan=self.planner.active_plan,
                )
            except Exception:
                pass

    def resume_session(self, session_id: str) -> bool:
        """Resume a saved session from disk and restore messages and TaskPlan."""
        persisted = self.session_store.load(session_id)
        if not persisted:
            return False

        from langchain_core.messages import messages_from_dict

        if persisted.plan:
            self.planner.active_plan = self.session_store.rehydrate_plan(persisted.plan)

        if persisted.messages:
            rehydrated_msgs = messages_from_dict(persisted.messages)
            config = {"configurable": {"thread_id": session_id}}
            try:
                self.graph.update_state(config, {"messages": rehydrated_msgs})
            except Exception:
                pass
        self.active_session_id = session_id
        return True

    def reset_session(self) -> None:
        """Clear conversation memory, recorded file snapshots, and active plan."""
        self.memory.storage.clear()
        self.snapshot_store.clear()
        self.planner.active_plan = None

    def generate_commit_message(self, task_description: str, diff_text: str) -> str:
        """Use LLM to generate a conventional commit message based on task and diff."""
        prompt = (
            "请根据以下任务说明与代码变更，生成一条符合 Conventional Commits 规范的简短单行 Git Commit 消息（例如：feat: add login 或 fix(calc): resolve float precision）。"
            "直接输出单行 Commit 文本，严禁包含多余解释、引号或 markdown 标记。\n\n"
            f"任务说明：{task_description}\n"
            f"代码差异：\n{diff_text[:2000]}"
        )
        try:
            raw = self.llm.invoke(prompt).content.strip()
            return raw.splitlines()[0].strip("`'\" ")
        except Exception:
            return f"feat: {task_description[:40]}"

