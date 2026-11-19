"""LangChain adapter for MCP tools; engine lifecycle goes through McpService."""

from __future__ import annotations

from typing import Any, Dict, List

from langchain_core.tools import StructuredTool

from tiancode.core.mcp_client import McpManager, McpServerStatus, McpToolSpec
from tiancode.core.mcp_service import McpService

__all__ = [
    "create_mcp_tools",
    "get_engine_mcp",
    "sync_engine_mcp",
    "reload_engine_mcp",
    "close_engine_mcp",
]


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


def get_engine_mcp(engine: Any) -> McpService:
    """Resolve or create the engine-owned McpService (unified MCP entry)."""
    mcp = getattr(engine, "mcp", None)
    if isinstance(mcp, McpService):
        return mcp
    from pathlib import Path

    work_dir = Path(engine.settings.work_dir).resolve()
    mcp = McpService(work_dir=work_dir)
    engine.mcp = mcp
    # 兼容旧字段：部分代码仍读 engine.mcp_manager
    engine.mcp_manager = mcp.manager
    return mcp


def sync_engine_mcp(engine: Any, *, force_reload: bool = False) -> List[StructuredTool]:
    """Bind MCP tools for current work_dir via McpService."""
    mcp = get_engine_mcp(engine)
    tools = mcp.as_langchain_tools(engine.settings.work_dir, force_reload=force_reload)
    engine.mcp_manager = mcp.manager
    return tools


def reload_engine_mcp(engine: Any) -> List[McpServerStatus]:
    """Force reload MCP configs/connections and rebind tools onto LLM/graph."""
    mcp = get_engine_mcp(engine)
    statuses = mcp.reload(work_dir=engine.settings.work_dir)
    engine.mcp_manager = mcp.manager
    engine.reload_llm()
    return statuses


def close_engine_mcp(engine: Any) -> None:
    """Close any active MCP transports owned by engine."""
    mcp = getattr(engine, "mcp", None)
    if isinstance(mcp, McpService):
        mcp.close()
        return
    manager = getattr(engine, "mcp_manager", None)
    if manager is not None:
        manager.close_all()
