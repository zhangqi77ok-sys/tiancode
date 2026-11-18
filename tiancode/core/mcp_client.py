"""MCP configuration layering and server lifecycle manager for TianCode."""

from __future__ import annotations

import atexit
import json
import os
import re
import tempfile
import threading
import weakref
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple

from tiancode.core.mcp_transports import (
    _DEFAULT_TIMEOUT_SECONDS,
    HttpMcpTransport,
    StdioMcpTransport,
)

__all__ = [
    "HttpMcpTransport",
    "McpManager",
    "McpServerConfig",
    "McpServerStatus",
    "McpToolSpec",
    "StdioMcpTransport",
    "get_default_global_mcp_path",
    "load_mcp_configs",
]

_SAFE_NAME_RE = re.compile(r"[^a-zA-Z0-9_-]")


@dataclass
class McpServerConfig:
    """Configuration for one external MCP server."""

    name: str
    transport: str
    command: str = ""
    args: List[str] = field(default_factory=list)
    env: Dict[str, str] = field(default_factory=dict)
    cwd: str = ""
    url: str = ""
    headers: Dict[str, str] = field(default_factory=dict)
    timeout_seconds: int = _DEFAULT_TIMEOUT_SECONDS
    disabled: bool = False
    source: str = ""


@dataclass
class McpToolSpec:
    """Discovered MCP tool mapped into TianCode's tool namespace."""

    full_name: str
    server_name: str
    tool_name: str
    description: str
    input_schema: Dict[str, Any] = field(default_factory=dict)


@dataclass
class McpServerStatus:
    """Runtime connection and tool discovery state for one MCP server."""

    name: str
    transport: str
    source: str
    enabled: bool = True
    connected: bool = False
    tool_count: int = 0
    error: str = ""


def get_default_global_mcp_path() -> Path:
    """Resolve the default global mcp.json path, isolated during pytest runs."""
    override = os.environ.get("TIANCODE_MCP_CONFIG", "").strip()
    if override:
        return Path(override).expanduser().resolve()
    if os.environ.get("PYTEST_CURRENT_TEST"):
        return (Path(tempfile.gettempdir()) / "tiancode_pytest_global_mcp.json").resolve()
    return (Path.home() / ".tiancode" / "mcp.json").resolve()


def _sanitize_segment(raw: str) -> str:
    return _SAFE_NAME_RE.sub("_", (raw or "").strip()).strip("_") or "item"


def _load_single_mcp_file(path: Path, source: str) -> Tuple[Dict[str, McpServerConfig], List[str]]:
    """Read one mcp.json file and return parsed configs plus explicit error notes."""
    if not path.is_file():
        return {}, []
    try:
        payload = json.loads(path.read_text(encoding="utf-8-sig"))
    except (OSError, json.JSONDecodeError) as exc:
        return {}, [f"读取 MCP 配置失败 ({path}): {exc}"]
    if not isinstance(payload, dict) or not isinstance(payload.get("mcpServers", {}), dict):
        return {}, [f"MCP 配置格式无效 ({path}): 缺少 mcpServers 对象"]

    configs: Dict[str, McpServerConfig] = {}
    errors: List[str] = []
    base_dir = path.parent.parent if path.parent.name == ".tiancode" else path.parent
    for name, entry in payload.get("mcpServers", {}).items():
        if not isinstance(entry, dict):
            errors.append(f"MCP 服务 '{name}' 配置不是对象 ({path})")
            continue
        t_raw = entry.get("timeoutSeconds", entry.get("timeout_seconds", _DEFAULT_TIMEOUT_SECONDS))
        timeout_s = max(1, int(t_raw)) if str(t_raw).isdigit() else _DEFAULT_TIMEOUT_SECONDS
        url = str(entry.get("url") or "").strip()
        cmd = str(entry.get("command") or "").strip()
        disabled = bool(entry.get("disabled", False))
        if not url and not cmd and not disabled:
            errors.append(f"MCP 服务 '{name}' 缺少 command 或 url ({path})")
            continue
        raw_cwd = str(entry.get("cwd") or "").strip()
        resolved_cwd = ""
        if raw_cwd:
            p = Path(raw_cwd).expanduser()
            resolved_cwd = str((p if p.is_absolute() else (base_dir / p)).resolve())
        configs[str(name)] = McpServerConfig(
            name=str(name),
            transport="http" if url else "stdio",
            command=cmd,
            args=[str(a) for a in (entry.get("args") or [])],
            env={str(k): str(v) for k, v in (entry.get("env") or {}).items()},
            cwd=resolved_cwd,
            url=url,
            headers={str(k): str(v) for k, v in (entry.get("headers") or {}).items()},
            timeout_seconds=timeout_s,
            disabled=disabled,
            source=source,
        )
    return configs, errors


def load_mcp_configs(
    work_dir: Path | str, global_config_path: Optional[Path] = None
) -> Tuple[Dict[str, McpServerConfig], List[str]]:
    """Load global and workspace mcp.json with workspace overriding global by name."""
    g_path = (global_config_path or get_default_global_mcp_path()).resolve()
    w_path = (Path(work_dir).resolve() / ".tiancode" / "mcp.json").resolve()
    merged, errors = _load_single_mcp_file(g_path, f"global:{g_path}")
    ws_configs, ws_errors = _load_single_mcp_file(w_path, f"workspace:{w_path}")
    merged.update(ws_configs)
    return merged, errors + ws_errors


def _cleanup_manager_ref(ref: weakref.ReferenceType["McpManager"]) -> None:
    mgr = ref()
    if mgr is not None:
        mgr.close_all()


class McpManager:
    """Lifecycle and tool registry manager across configured MCP servers."""

    def __init__(self, work_dir: Path | str, global_config_path: Optional[Path] = None) -> None:
        self.work_dir = Path(work_dir).resolve()
        self.global_config_path = global_config_path
        self._lock = threading.RLock()
        self._loaded = False
        self._configs: Dict[str, McpServerConfig] = {}
        self._transports: Dict[str, Any] = {}
        self._statuses: Dict[str, McpServerStatus] = {}
        self._tools: Dict[str, McpToolSpec] = {}
        self._config_errors: List[str] = []
        atexit.register(_cleanup_manager_ref, weakref.ref(self))

    def _ensure_loaded(self) -> None:
        with self._lock:
            if not self._loaded:
                self.reload()

    def reload(self, work_dir: Optional[Path | str] = None) -> List[McpServerStatus]:
        """Close active transports, reload mcp.json, connect enabled servers, and discover tools."""
        with self._lock:
            self.close_all()
            if work_dir is not None:
                self.work_dir = Path(work_dir).resolve()
            self._configs, self._config_errors = load_mcp_configs(self.work_dir, self.global_config_path)
            self._statuses.clear()
            self._tools.clear()
            for name, cfg in self._configs.items():
                self._connect_one(name, cfg)
            self._loaded = True
            return list(self._statuses.values())

    def _connect_one(self, name: str, cfg: McpServerConfig) -> None:
        if cfg.disabled:
            self._statuses[name] = McpServerStatus(name=name, transport=cfg.transport, source=cfg.source, enabled=False)
            return
        transport = (
            HttpMcpTransport(cfg.url, headers=cfg.headers, timeout_seconds=cfg.timeout_seconds)
            if cfg.transport == "http"
            else StdioMcpTransport(cfg.command, args=cfg.args, env=cfg.env, timeout_seconds=cfg.timeout_seconds, cwd=cfg.cwd or str(self.work_dir))
        )
        try:
            transport.connect()
            raw_tools = transport.list_tools()
        except Exception as exc:
            transport.close()
            self._statuses[name] = McpServerStatus(
                name=name, transport=cfg.transport, source=cfg.source, enabled=True, connected=False, error=str(exc)
            )
            return
        self._transports[name] = transport
        safe_srv = _sanitize_segment(name)
        count = 0
        for item in raw_tools:
            if not isinstance(item, dict) or not item.get("name"):
                continue
            tool_name = str(item["name"])
            full_name = f"mcp__{safe_srv}__{_sanitize_segment(tool_name)}"
            schema = item.get("inputSchema") if isinstance(item.get("inputSchema"), dict) else {"type": "object", "properties": {}}
            self._tools[full_name] = McpToolSpec(
                full_name=full_name,
                server_name=name,
                tool_name=tool_name,
                description=str(item.get("description") or f"MCP tool {name}/{tool_name}"),
                input_schema=schema,
            )
            count += 1
        self._statuses[name] = McpServerStatus(
            name=name, transport=cfg.transport, source=cfg.source, enabled=True, connected=True, tool_count=count
        )

    def list_tools(self) -> List[McpToolSpec]:
        self._ensure_loaded()
        with self._lock:
            return list(self._tools.values())

    def get_server_statuses(self) -> List[McpServerStatus]:
        self._ensure_loaded()
        with self._lock:
            return list(self._statuses.values())

    def get_config_errors(self) -> List[str]:
        self._ensure_loaded()
        with self._lock:
            return list(self._config_errors)

    def call_tool(self, full_name: str, arguments: Optional[Dict[str, Any]] = None) -> str:
        """Invoke a discovered MCP tool by its namespaced full_name."""
        self._ensure_loaded()
        with self._lock:
            spec = self._tools.get(full_name)
            transport = self._transports.get(spec.server_name) if spec else None
        if not spec or not transport:
            return f"❌ MCP 工具不可用或未连接: {full_name}"
        try:
            return transport.call_tool(spec.tool_name, dict(arguments or {}))
        except TimeoutError as exc:
            transport.close()
            with self._lock:
                self._transports.pop(spec.server_name, None)
                if spec.server_name in self._statuses:
                    self._statuses[spec.server_name].connected = False
                    self._statuses[spec.server_name].error = str(exc)
            return f"❌ {exc}"
        except Exception as exc:
            return f"❌ MCP 工具调用失败 ({full_name}): {exc}"

    def close_all(self) -> None:
        """Close every active MCP transport cleanly."""
        with self._lock:
            transports = list(self._transports.values())
            self._transports.clear()
            for status in self._statuses.values():
                status.connected = False
            self._loaded = False
        for transport in transports:
            try:
                transport.close()
            except Exception:
                pass
