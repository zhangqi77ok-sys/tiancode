"""Adapters exposing external MCP tools to LangGraph and managing engine MCP lifecycle."""

from __future__ import annotations

from pathlib import Path
from typing import Any, Dict, List

from langchain_core.tools import StructuredTool

from tiancode.core.mcp_client import McpManager, McpServerStatus, McpToolSpec


def _normalize_input_schema(raw: Dict[str, Any]) -> Dict[str, Any]:
    """Ensure tool schema conforms to JSON Schema object conventions expected by LLM APIs."""
    schema = dict(raw or {})
    if schema.get("type") != "object":
        schema["type"] = "object"
    if not isinstance(schema.get("properties"), dict):
        schema["properties"] = {}
    return schema


def _make_mcp_runner(mcp_manager: McpManager, spec: McpToolSpec):
    def _run_mcp_tool(**kwargs: Any) -> str:
        return mcp_manager.call_tool(spec.full_name, kwargs)

    _run_mcp_tool.__name__ = spec.full_name
    return _run_mcp_tool


def create_mcp_tools(mcp_manager: McpManager) -> List[StructuredTool]:
    """Convert all discovered MCP tool specs into LangChain StructuredTool instances."""
    tools: List[StructuredTool] = []
    for spec in mcp_manager.list_tools():
        tools.append(
            StructuredTool.from_function(
                func=_make_mcp_runner(mcp_manager, spec),
                name=spec.full_name,
                description=f"[MCP:{spec.server_name}] {spec.description}",
                args_schema=_normalize_input_schema(spec.input_schema),
            )
        )
    return tools


def sync_engine_mcp(engine: Any, *, force_reload: bool = False) -> List[StructuredTool]:
    """Ensure engine.mcp_manager matches engine.settings.work_dir and build StructuredTools."""
    work_dir = Path(engine.settings.work_dir).resolve()
    manager = getattr(engine, "mcp_manager", None)
    if manager is None:
        manager = McpManager(work_dir=work_dir)
        engine.mcp_manager = manager
    elif force_reload or manager.work_dir != work_dir:
        manager.reload(work_dir=work_dir)
    return create_mcp_tools(manager)


def reload_engine_mcp(engine: Any) -> List[McpServerStatus]:
    """Force reload MCP configs/connections for engine and rebind tools onto LLM/graph."""
    work_dir = Path(engine.settings.work_dir).resolve()
    manager = getattr(engine, "mcp_manager", None)
    if manager is None:
        manager = McpManager(work_dir=work_dir)
        engine.mcp_manager = manager
    statuses = manager.reload(work_dir=work_dir)
    engine.reload_llm()
    return statuses


def close_engine_mcp(engine: Any) -> None:
    """Close any active MCP transports owned by engine."""
    manager = getattr(engine, "mcp_manager", None)
    if manager is not None:
        manager.close_all()
