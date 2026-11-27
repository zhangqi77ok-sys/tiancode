"""Stream event adapter and session state persistence/resume helpers for TianCodeEngine."""

import logging
from typing import TYPE_CHECKING, Any, Dict, Generator, List, Optional
from langchain_core.messages import ToolMessage

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
    elif node == "tools" and isinstance(chunk, ToolMessage):
        for kind, piece in splitter.flush():
            yield {"type": kind, "content": piece}
        yield {"type": "tool_end", "name": getattr(chunk, "name", "tool"), "content": getattr(chunk, "content", "")}


def persist_engine_session(engine: "TianCodeEngine", thread_id: str, config: Dict[str, Any]) -> Optional[str]:
    """Persist graph messages, active plan, snapshots, usage metrics, and episode memory.

    Returns None on success. A failure is logged and also stored on
    ``engine.last_persist_error`` so the CLI can show it; the transcript is
    not discarded just because the disk write failed.
    """
    try:
        graph = getattr(engine, "graph", None)
        get_state = getattr(graph, "get_state", None)
        if not callable(get_state):
            return None
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
        try:
            engine.last_persist_error = None
        except Exception:
            pass
        return None
    except Exception as exc:
        message = f"会话状态持久化失败 [{thread_id}]: {exc}"
        logging.getLogger("tiancode.engine").warning(message)
        try:
            engine.last_persist_error = message
        except Exception:
            pass
        return message


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
    """Make a transcript acceptable to tool-calling providers.

    Providers reject a later user or system message while an assistant tool
    call is open, and they also reject a tool result whose call is missing.
    Cancelling a turn, or folding a system summary between a call and its
    result, leaves exactly those shapes. Existing results are kept; missing
    ids are filled in call order; orphan results and duplicate ids are dropped.
    A system message that landed inside a tool group is moved to after the
    results so the pair stays contiguous.
    """
    from langchain_core.messages import AIMessage, SystemMessage, ToolMessage

    closed: List[Any] = []
    index = 0
    while index < len(messages):
        message = messages[index]
        index += 1
        tool_calls = getattr(message, "tool_calls", None) if isinstance(message, AIMessage) else None
        if isinstance(message, ToolMessage):
            continue
        closed.append(message)
        if not tool_calls:
            continue
        expected = []
        for tool_call in tool_calls:
            identity = _tool_call_identity(tool_call)
            if identity is not None and identity[0] not in {item[0] for item in expected}:
                expected.append(identity)
        expected_ids = {item[0] for item in expected}
        found: Dict[str, ToolMessage] = {}
        unmatched: List[ToolMessage] = []
        deferred: List[Any] = []
        while index < len(messages):
            nxt = messages[index]
            if isinstance(nxt, ToolMessage):
                index += 1
                tool_call_id = getattr(nxt, "tool_call_id", None)
                if not expected_ids:
                    unmatched.append(nxt)
                elif isinstance(tool_call_id, str) and tool_call_id in expected_ids and tool_call_id not in found:
                    found[tool_call_id] = nxt
                continue
            if isinstance(nxt, SystemMessage):
                deferred.append(nxt)
                index += 1
                continue
            break
        if expected_ids:
            for tool_call_id, tool_name in expected:
                existing = found.get(tool_call_id)
                if existing is not None:
                    closed.append(existing)
                    continue
                closed.append(
                    ToolMessage(
                        content=INTERRUPTED_TOOL_NOTE,
                        tool_call_id=tool_call_id,
                        name=tool_name,
                    )
                )
        else:
            closed.extend(unmatched)
        closed.extend(deferred)
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


def install_thread_messages(graph: Any, config: Dict[str, Any], messages: List[Any]) -> List[Any]:
    """Replace the thread transcript with a tool-call-safe copy of `messages`."""
    sealed = close_pending_tool_calls(list(messages))
    _replace_checkpoint_messages(graph, config, sealed)
    return sealed


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


def _warn_resume_workspace(message: str) -> None:
    """Surface a non-fatal resume warning without hiding the restored transcript."""
    try:
        from tiancode.cli.formatter import TerminalFormatter

        TerminalFormatter.print_warning(message)
    except Exception:
        pass


def resume_engine_session(engine: "TianCodeEngine", session_id: str) -> bool:
    """Replace graph, plan, undo snapshots, usage, and workspace with one session.

    Validation runs before any live field is written. A missing or corrupt
    session returns False and leaves the engine unchanged. When the saved
    workspace still exists, the engine re-anchors there before the transcript
    is installed, so later tool calls and undo snapshots hit that tree. A
    missing workspace keeps the current directory and leaves foreign snapshots
    unloaded. Snapshot import replaces the previous undo set.
    """
    from pathlib import Path

    persisted = engine.session_store.load(session_id)
    if not persisted:
        err = getattr(engine.session_store, "last_load_error", None)
        if err is not None and getattr(err, "kind", "") != "not_found":
            _warn_resume_cancelled(err)
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

    saved_work = str(getattr(getattr(persisted, "metadata", None), "work_dir", "") or "").strip()
    previous_work = getattr(getattr(engine, "settings", None), "work_dir", "")
    switched = False
    workspace_note = ""
    if saved_work and not _same_workspace(saved_work, previous_work):
        target = Path(saved_work).expanduser()
        try:
            if target.is_dir():
                engine.switch_workspace(target, discard_snapshots=False, session_id=session_id)
                switched = True
            else:
                workspace_note = "该会话记录的工作区已不存在，仍停留在当前目录。"
        except Exception as exc:
            workspace_note = f"未能切回会话工作区 ({exc})，仍停留在当前目录。"

    config = {"configurable": {"thread_id": session_id}}
    try:
        _replace_checkpoint_messages(engine.graph, config, rehydrated_msgs)
    except Exception as exc:
        if switched:
            try:
                engine.switch_workspace(previous_work, discard_snapshots=False)
            except Exception:
                workspace_note = "会话恢复失败，且工作区切回也失败，请检查当前目录。"
        _warn_resume_cancelled(exc)
        return False

    engine.snapshot_store.clear()
    incoming_snapshots = list(persisted.snapshots or [])
    current_work = getattr(getattr(engine, "settings", None), "work_dir", "")
    anchored = bool(saved_work) and _same_workspace(saved_work, current_work)
    dropped_foreign = bool(incoming_snapshots) and not anchored
    if dropped_foreign:
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
    notes = []
    if dropped_foreign:
        notes.append("该会话的撤销快照属于其他工作区，本次未载入，当前目录文件保持不变。")
    if workspace_note:
        notes.append(workspace_note)
    if notes:
        _warn_resume_workspace(" ".join(notes))
    return True


def report_persist_error(engine: Any) -> None:
    """Print and clear a session-save failure recorded during the last turn."""
    message = getattr(engine, "last_persist_error", None)
    if not message:
        return
    try:
        engine.last_persist_error = None
    except Exception:
        pass
    from tiancode.cli.formatter import TerminalFormatter

    TerminalFormatter.print_warning(str(message))
