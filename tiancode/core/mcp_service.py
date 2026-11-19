"""Unified MCP application facade — single entry for Engine / CLI / REPL."""

from __future__ import annotations

from pathlib import Path
from typing import TYPE_CHECKING, Any, List, Optional, Union

from tiancode.core.mcp_client import McpManager, McpServerStatus, McpToolSpec

if TYPE_CHECKING:
    from langchain_core.tools import StructuredTool

__all__ = ["McpService"]


class McpService:
    """应用层门面：配置加载、连接、发现、工具绑定、状态查询与关闭均经此入口。

    调用方（Engine / CLI / REPL）不应直接操作 McpManager 或 transport。
    """

    def __init__(
        self,
        work_dir: Union[Path, str],
        global_config_path: Optional[Path] = None,
    ) -> None:
        self._manager = McpManager(work_dir=work_dir, global_config_path=global_config_path)

    @property
    def manager(self) -> McpManager:
        """底层管理器逃生舱；新代码优先用门面方法。"""
        return self._manager

    @property
    def work_dir(self) -> Path:
        return self._manager.work_dir

    def ensure(
        self,
        work_dir: Optional[Union[Path, str]] = None,
        *,
        force_reload: bool = False,
    ) -> None:
        """确保已按 work_dir 加载；目录变化或 force_reload 时重连。"""
        target = Path(work_dir).resolve() if work_dir is not None else self._manager.work_dir
        if force_reload or self._manager.work_dir != target:
            self._manager.reload(work_dir=target)
            return
        # 触发懒加载，不重复建连
        self._manager.get_server_statuses()

    def as_langchain_tools(
        self,
        work_dir: Optional[Union[Path, str]] = None,
        *,
        force_reload: bool = False,
    ) -> List["StructuredTool"]:
        """发现 MCP 工具并投影为 LangChain StructuredTool 列表。"""
        from tiancode.tools.mcp_ops import create_mcp_tools

        self.ensure(work_dir, force_reload=force_reload)
        return create_mcp_tools(self._manager)

    def reload(self, work_dir: Optional[Union[Path, str]] = None) -> List[McpServerStatus]:
        """强制重载 mcp.json、重连服务并刷新工具目录。"""
        return self._manager.reload(work_dir=work_dir)

    def close(self) -> None:
        """关闭全部 MCP 传输与子进程。"""
        self._manager.close_all()

    def statuses(self) -> List[McpServerStatus]:
        self.ensure()
        return self._manager.get_server_statuses()

    def list_tools(self) -> List[McpToolSpec]:
        self.ensure()
        return self._manager.list_tools()

    def config_errors(self) -> List[str]:
        self.ensure()
        return self._manager.get_config_errors()

    def call_tool(self, full_name: str, arguments: Optional[dict[str, Any]] = None) -> str:
        """按命名空间全名调用已发现的 MCP 工具。"""
        self.ensure()
        return self._manager.call_tool(full_name, arguments)

    def status_summary(self) -> Optional[str]:
        """REPL 启动用一行摘要；无服务时返回 None。"""
        statuses = self.statuses()
        if not statuses:
            return None
        parts = [
            f"{s.name}:{'在线' if s.connected else ('停用' if not s.enabled else '离线')}({s.tool_count})"
            for s in statuses
        ]
        return ", ".join(parts)
