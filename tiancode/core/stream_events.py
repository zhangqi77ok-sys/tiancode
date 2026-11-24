"""Stream event adapter and session state persistence/resume helpers for TianCodeEngine."""

import logging
from typing import TYPE_CHECKING, Any, Dict, Generator, List, Optional

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
        graph = getattr(engine, "graph", None)
        get_state = getattr(graph, "get_state", None)
        if not callable(get_state):
            return
        state = get_state(config)
        all_messages = close_pending_tool_calls(list(state.values.get("messages") or []))
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


def _warn_resume_cancelled(exc: Exception) -> None:
    """Tell the operator that resume aborted and live state was kept."""
    from tiancode.cli.formatter import TerminalFormatter

    TerminalFormatter.print_warning(f"会话状态恢复失败: {exc}，取消切换以防覆盖历史！")


INTERRUPTED_TOOL_NOTE = (
    "工具调用未完成：本轮生成被取消或在结果返回前中断，该工具没有执行成功。"
    "不要假设它已经生效；需要的话请重新调用。"
)


def _tool_call_identity(tool_call: Any) -> Optional[tuple]:
    """Return (id, name) for a tool call that can be paired with a tool result."""
    if isinstance(tool_call, dict):
        tool_call_id = tool_call.get("id")
        tool_name = tool_call.get("name") or "tool"
    else:
        tool_call_id = getattr(tool_call, "id", None)
        tool_name = getattr(tool_call, "name", None) or "tool"
    if not isinstance(tool_call_id, str) or not tool_call_id:
        return None
    return tool_call_id, str(tool_name)


def close_pending_tool_calls(messages: List[Any]) -> List[Any]:
    """Append a result for every tool call that never received one.

    Providers reject a later user message while an assistant tool call is open.
    Cancelling a turn after the call is checkpointed leaves exactly that shape.
    Existing results are kept; only the missing ids are filled, in call order,
    before the next non-tool message.
    """
    from langchain_core.messages import AIMessage, ToolMessage

    closed: List[Any] = []
    index = 0
    while index < len(messages):
        message = messages[index]
        closed.append(message)
        index += 1
        tool_calls = getattr(message, "tool_calls", None) if isinstance(message, AIMessage) else None
        if not tool_calls:
            continue
        expected = []
        for tool_call in tool_calls:
            identity = _tool_call_identity(tool_call)
            if identity is not None and identity[0] not in {item[0] for item in expected}:
                expected.append(identity)
        answered = set()
        while index < len(messages) and isinstance(messages[index], ToolMessage):
            closed.append(messages[index])
            answered.add(messages[index].tool_call_id)
            index += 1
        for tool_call_id, tool_name in expected:
            if tool_call_id in answered:
                continue
            closed.append(
                ToolMessage(
                    content=INTERRUPTED_TOOL_NOTE,
                    tool_call_id=tool_call_id,
                    name=tool_name,
                )
            )
            answered.add(tool_call_id)
    return closed


def repair_pending_tool_calls(engine: "TianCodeEngine", config: Dict[str, Any]) -> bool:
    """Close unanswered tool calls already visible on this thread.

    A cancelled brain step can still be a pending task. ``as_node='brain'``
    commits that step together with the synthetic tool results and clears the
    task, so the next user message is not appended after an open tool call.
    """
    graph = getattr(engine, "graph", None)
    get_state = getattr(graph, "get_state", None)
    if not callable(get_state):
        return False
    state = get_state(config)
    values = getattr(state, "values", None) or {}
    messages = list(values.get("messages") or [])
    repaired = close_pending_tool_calls(messages)
    if len(repaired) == len(messages):
        return False
    engine.graph.update_state(config, {"messages": repaired}, as_node="brain")
    return True


def _checkpoint_messages(graph: Any, config: Dict[str, Any]) -> List[Any]:
    """Return messages already stored on a graph thread, or an empty list."""
    state = graph.get_state(config)
    values = getattr(state, "values", None) or {}
    return list(values.get("messages") or [])


def _replace_checkpoint_messages(graph: Any, config: Dict[str, Any], messages: List[Any]) -> None:
    """Install `messages` as the thread transcript.

    MessagesState appends on update_state. Removing the current ids in the same
    write is what makes /switch and a second resume replace history instead of
    duplicating it.
    """
    from langchain_core.messages import RemoveMessage

    existing = _checkpoint_messages(graph, config)
    removals = [RemoveMessage(id=msg.id) for msg in existing if getattr(msg, "id", None)]
    graph.update_state(config, {"messages": [*removals, *messages]})


def _same_workspace(saved: str, current: Any) -> bool:
    """True when the saved session directory is the engine directory now in use."""
    from pathlib import Path

    text = str(saved or "").strip()
    if not text:
        return False
    try:
        return Path(text).expanduser().resolve() == Path(current).expanduser().resolve()
    except OSError:
        return False


def resume_engine_session(engine: "TianCodeEngine", session_id: str) -> bool:
    """Replace graph, plan, undo snapshots, and usage with one persisted session.

    Validation runs before any live field is written. A missing or corrupt
    session returns False and leaves the engine unchanged. Snapshot import
    replaces the previous undo set so a switched session cannot roll back
    files that belong to the session we just left. Snapshots saved for another
    directory are left out, so undo cannot write them into the current tree.
    """
    persisted = engine.session_store.load(session_id)
    if not persisted:
        return False

    from langchain_core.messages import messages_from_dict

    try:
        rehydrated_msgs = close_pending_tool_calls(
            messages_from_dict(persisted.messages) if persisted.messages else []
        )
        plan_obj = engine.session_store.rehydrate_plan(persisted.plan) if persisted.plan else None
    except Exception as exc:
        _warn_resume_cancelled(exc)
        return False

    config = {"configurable": {"thread_id": session_id}}
    try:
        _replace_checkpoint_messages(engine.graph, config, rehydrated_msgs)
    except Exception as exc:
        _warn_resume_cancelled(exc)
        return False

    engine.snapshot_store.clear()
    incoming_snapshots = persisted.snapshots or []
    saved_work = getattr(getattr(persisted, "metadata", None), "work_dir", "")
    current_work = getattr(getattr(engine, "settings", None), "work_dir", "")
    foreign_snapshots = bool(incoming_snapshots) and not _same_workspace(saved_work, current_work)
    if foreign_snapshots:
        incoming_snapshots = []
    if incoming_snapshots and hasattr(engine.snapshot_store, "import_snapshots"):
        engine.snapshot_store.import_snapshots(incoming_snapshots)

    engine.planner.active_plan = plan_obj
    if hasattr(engine, "usage_tracker"):
        if persisted.usage:
            engine.usage_tracker.load_from_dict(persisted.usage)
        else:
            engine.usage_tracker.reset()

    engine.active_session_id = session_id
    engine.permission_guard.clear_session_approvals()
    if hasattr(engine, "tool_dispatcher"):
        engine.tool_dispatcher.reset_loop_guard()
    if foreign_snapshots:
        try:
            from tiancode.cli.formatter import TerminalFormatter

            TerminalFormatter.print_warning(
                "该会话的撤销快照属于其他工作区，本次未载入，当前目录文件保持不变。"
            )
        except Exception:
            pass
    return True
