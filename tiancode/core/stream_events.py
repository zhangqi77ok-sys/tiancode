"""Stream event adapter and session state persistence/resume helpers for TianCodeEngine."""

import logging
from typing import TYPE_CHECKING, Any, Dict, Generator

from tiancode.core.llm_adapter import ThinkTagStreamSplitter

if TYPE_CHECKING:
    from tiancode.core.engine import TianCodeEngine


def iter_message_events(
    chunk: Any,
    meta: Any,
    seen_tools: set,
    splitter: ThinkTagStreamSplitter,
) -> Generator[Dict[str, Any], None, None]:
    """Translate a LangGraph stream tuple into structured thinking/token/tool events."""
    node = meta.get("langgraph_node") if isinstance(meta, dict) else None
    if node == "brain":
        add_kw = getattr(chunk, "additional_kwargs", None) or {}
        reasoning = add_kw.get("reasoning_content")
        if isinstance(reasoning, str) and reasoning:
            yield {"type": "thinking", "content": reasoning}

        raw_content = getattr(chunk, "content", None)
        if isinstance(raw_content, str) and raw_content:
            for kind, piece in splitter.feed(raw_content):
                yield {"type": kind, "content": piece}

        tool_chunks = getattr(chunk, "tool_call_chunks", None)
        if tool_chunks:
            for kind, piece in splitter.flush():
                yield {"type": kind, "content": piece}
            for tc in tool_chunks:
                name = tc.get("name")
                call_id = tc.get("id") or tc.get("index")
                if name and call_id not in seen_tools:
                    seen_tools.add(call_id)
                    yield {"type": "tool_start", "name": name, "args": tc.get("args", "")}
        elif getattr(chunk, "tool_calls", None):
            for kind, piece in splitter.flush():
                yield {"type": kind, "content": piece}
            for tc in chunk.tool_calls:
                call_id = tc.get("id") or tc.get("name")
                if call_id not in seen_tools:
                    seen_tools.add(call_id)
                    yield {"type": "tool_start", "name": tc.get("name"), "args": tc.get("args", {})}
    elif node == "tools":
        for kind, piece in splitter.flush():
            yield {"type": kind, "content": piece}
        yield {"type": "tool_end", "name": getattr(chunk, "name", "tool"), "content": getattr(chunk, "content", "")}


def persist_engine_session(engine: "TianCodeEngine", thread_id: str, config: Dict[str, Any]) -> None:
    """Persist graph messages, active plan, snapshots, usage metrics, and episode memory."""
    try:
        state = engine.graph.get_state(config)
        all_messages = state.values.get("messages", [])
        exported_snapshots = (
            engine.snapshot_store.export_snapshots()
            if hasattr(engine.snapshot_store, "export_snapshots")
            else []
        )
        if hasattr(engine, "usage_tracker"):
            tokens, cost = engine.usage_tracker.get_session_stats()
            total_tokens, estimated_cost = tokens.total_tokens, cost.total_cost
            usage_dict = engine.usage_tracker.to_dict()
        else:
            total_tokens, estimated_cost, usage_dict = 0, 0.0, None

        engine.session_store.save(
            session_id=thread_id,
            work_dir=str(engine.settings.work_dir),
            messages=all_messages,
            plan=engine.planner.active_plan,
            snapshots=exported_snapshots,
            total_tokens=total_tokens,
            estimated_cost=estimated_cost,
            usage=usage_dict,
        )
        if all_messages and hasattr(engine, "memory_store"):
            modified = (
                list(engine.snapshot_store.get_all_snapshots().keys())
                if hasattr(engine.snapshot_store, "get_all_snapshots")
                else []
            )
            engine.memory_store.consolidate_session(
                session_id=thread_id,
                work_dir=engine.settings.work_dir,
                messages=all_messages,
                plan=engine.planner.active_plan,
                modified_files=modified,
            )
    except Exception as exc:
        logging.getLogger("tiancode.engine").warning(f"会话状态持久化失败 [{thread_id}]: {exc}")


def resume_engine_session(engine: "TianCodeEngine", session_id: str) -> bool:
    """Resume a persisted session into graph state, planner, snapshots, and usage tracker."""
    persisted = engine.session_store.load(session_id)
    if not persisted:
        return False

    from langchain_core.messages import messages_from_dict

    if persisted.plan:
        engine.planner.active_plan = engine.session_store.rehydrate_plan(persisted.plan)
    if getattr(persisted, "snapshots", None) and hasattr(engine.snapshot_store, "import_snapshots"):
        engine.snapshot_store.import_snapshots(persisted.snapshots)
    if hasattr(engine, "usage_tracker"):
        if getattr(persisted, "usage", None):
            engine.usage_tracker.load_from_dict(persisted.usage)
        else:
            engine.usage_tracker.reset()

    if persisted.messages:
        rehydrated_msgs = messages_from_dict(persisted.messages)
        config = {"configurable": {"thread_id": session_id}}
        try:
            engine.graph.update_state(config, {"messages": rehydrated_msgs})
        except Exception as exc:
            from tiancode.cli.formatter import TerminalFormatter
            TerminalFormatter.print_warning(f"会话状态恢复失败: {exc}，取消切换以防覆盖历史！")
            return False

    engine.active_session_id = session_id
    engine.permission_guard.clear_session_approvals()
    if hasattr(engine, "tool_dispatcher"):
        engine.tool_dispatcher.reset_loop_guard()
    return True
