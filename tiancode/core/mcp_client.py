"""MCP configuration layering and server lifecycle manager for TianCode."""

from __future__ import annotations

import atexit
import json
import os
import re
import stat
import tempfile
import threading
import weakref
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple

from tiancode.core.mcp_auth import McpAuthError, expand_env_vars, referenced_sensitive_env_names
from tiancode.core.path_safety import WorkspacePathError, workspace_directory_entry
from tiancode.core.mcp_transports import (
    _DEFAULT_TIMEOUT_SECONDS,
    HttpMcpTransport,
    StdioMcpTransport,
)
from tiancode.core.mcp_discovery import (
    discover_tianshell_mcp,
    resolve_tianshell_runtime_path,
)

__all__ = [
    "HttpMcpTransport",
    "McpManager",
    "McpServerConfig",
    "McpServerStatus",
    "McpToolSpec",
    "StdioMcpTransport",
    "discover_tianshell_mcp",
    "get_default_global_mcp_path",
    "load_mcp_configs",
    "resolve_tianshell_runtime_path",
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
    auth: Dict[str, Any] = field(default_factory=dict)
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
    protocol_version: str = ""
    error: str = ""


def _path_keeping_final_component(raw: Path) -> Path:
    """Absolute path that does not resolve the last component."""
    if not raw.is_absolute():
        raw = Path.cwd() / raw
    try:
        parent = raw.parent.resolve()
    except OSError:
        parent = raw.parent.absolute()
    return parent / raw.name


def get_default_global_mcp_path() -> Path:
    """Return the global mcp.json path. The last component is not followed."""
    override = os.environ.get("TIANCODE_MCP_CONFIG", "").strip()
    if override:
        return _path_keeping_final_component(Path(override).expanduser())
    if os.environ.get("PYTEST_CURRENT_TEST"):
        return Path(tempfile.gettempdir()) / "tiancode_pytest_global_mcp.json"
    return Path.home() / ".tiancode" / "mcp.json"


def _sanitize_segment(raw: str) -> str:
    return _SAFE_NAME_RE.sub("_", (raw or "").strip()).strip("_") or "item"


def _is_inside(path: Path, root: Path) -> bool:
    """True when path resolves to root or a directory under it."""
    try:
        path.resolve().relative_to(root.resolve())
    except (OSError, ValueError):
        return False
    return True


def _expand_config_value(value: Any) -> str:
    """Expand one mcp.json string. Unset placeholders fail closed."""
    return expand_env_vars(str(value), strict=True)


def _config_strings(value: Any) -> List[str]:
    """String leaves of one config field. Keys are included; values are not expanded."""
    if isinstance(value, str):
        return [value]
    if isinstance(value, dict):
        texts: List[str] = []
        for key, item in value.items():
            texts.append(str(key))
            texts.extend(_config_strings(item))
        return texts
    if isinstance(value, list):
        texts = []
        for item in value:
            texts.extend(_config_strings(item))
        return texts
    return []


def _workspace_secret_names(entry: Dict[str, Any]) -> List[str]:
    """Sensitive env names a workspace server would copy out of the parent process."""
    texts: List[str] = []
    for key in ("command", "url", "cwd", "args", "env", "headers", "auth"):
        texts.extend(_config_strings(entry.get(key)))
    found: List[str] = []
    for text in texts:
        for name in referenced_sensitive_env_names(text):
            if name not in found:
                found.append(name)
    return found


def _workspace_url_error(url: str) -> Optional[str]:
    """Reject a workspace MCP URL that is not a public http(s) endpoint.

    Global config may still point at localhost. A repository's mcp.json may not:
    connecting to it would reach loopback, link-local, or cloud metadata.
    """
    if not url:
        return None
    from tiancode.tools.web_ops import validate_public_http_url

    try:
        validate_public_http_url(url)
    except ValueError as exc:
        return str(exc)
    return None


def _workspace_command_error(command: str, args: List[str]) -> Optional[str]:
    """Reject a workspace stdio command the shell guard would refuse."""
    if not command:
        return None
    from tiancode.tools.execution import is_dangerous_command

    if is_dangerous_command(" ".join([command, *args])):
        return "工作区 MCP 的 command 含高危命令，已拒绝启动"
    return None


def _load_single_mcp_file(
    path: Path,
    source: str,
    *,
    sandbox_root: Optional[Path] = None,
) -> Tuple[Dict[str, McpServerConfig], List[str]]:
    """Read one regular mcp.json. A symlink or hard link is not opened."""
    label = path.name
    if path.is_symlink():
        return {}, [f"已拒绝读取 MCP 配置 {label}：它是符号链接，未读取目标。"]
    try:
        info = path.lstat()
    except OSError:
        return {}, []
    if not stat.S_ISREG(info.st_mode):
        return {}, []
    if info.st_nlink > 1:
        return {}, [f"已拒绝读取 MCP 配置 {label}：它是硬链接，未读取内容。"]
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
    try:
        fd = os.open(path, flags)
    except OSError as exc:
        return {}, [f"读取 MCP 配置失败 ({label}): {type(exc).__name__}"]
    try:
        with os.fdopen(fd, "r", encoding="utf-8-sig") as handle:
            payload = json.load(handle)
    except json.JSONDecodeError as exc:
        return {}, [f"读取 MCP 配置失败 ({label}): {exc.msg}"]
    except OSError as exc:
        return {}, [f"读取 MCP 配置失败 ({label}): {type(exc).__name__}"]
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
        disabled = bool(entry.get("disabled", False))
        # Cursor/VS Code style "type": "http"|"sse"|"stdio" — url 仍优先判定传输
        type_hint = str(entry.get("type") or entry.get("transport") or "").strip().lower()
        if sandbox_root is not None:
            secret_names = _workspace_secret_names(entry)
            if secret_names:
                errors.append(
                    f"MCP 服务 '{name}' 未加载: 工作区配置不能引用敏感环境变量 {', '.join(secret_names)}"
                )
                continue
        try:
            url = _expand_config_value(entry.get("url") or "").strip()
            cmd = _expand_config_value(entry.get("command") or "").strip()
            args = [_expand_config_value(item) for item in (entry.get("args") or [])]
            env = {str(key): _expand_config_value(value) for key, value in (entry.get("env") or {}).items()}
            headers = {
                str(key): _expand_config_value(value) for key, value in (entry.get("headers") or {}).items()
            }
            raw_cwd = _expand_config_value(entry.get("cwd") or "").strip()
        except McpAuthError as exc:
            errors.append(f"MCP 服务 '{name}' 未加载: {exc}")
            continue
        if sandbox_root is not None:
            command_error = _workspace_command_error(cmd, args)
            if command_error:
                errors.append(f"MCP 服务 '{name}' 未加载: {command_error}")
                continue
            url_error = _workspace_url_error(url)
            if url_error:
                errors.append(f"MCP 服务 '{name}' 未加载: {url_error}")
                continue
        if not url and not cmd and not disabled:
            errors.append(f"MCP 服务 '{name}' 缺少 command 或 url ({path})")
            continue
        resolved_cwd = ""
        if raw_cwd:
            cwd_path = Path(raw_cwd).expanduser()
            resolved = (cwd_path if cwd_path.is_absolute() else (base_dir / cwd_path)).resolve()
            if sandbox_root is not None and not _is_inside(resolved, sandbox_root):
                errors.append(f"MCP 服务 '{name}' 的 cwd 越出工作区，已拒绝加载: {resolved}")
                continue
            resolved_cwd = str(resolved)
        auth_raw = entry.get("auth") if isinstance(entry.get("auth"), dict) else {}
        transport = "http" if url or type_hint in ("http", "sse", "streamable-http", "streamable_http") else "stdio"
        configs[str(name)] = McpServerConfig(
            name=str(name),
            transport=transport,
            command=cmd,
            args=args,
            env=env,
            cwd=resolved_cwd,
            url=url,
            headers=headers,
            auth=dict(auth_raw),
            timeout_seconds=timeout_s,
            disabled=disabled,
            source=source,
        )
    return configs, errors


def load_mcp_configs(
    work_dir: Path | str, global_config_path: Optional[Path] = None
) -> Tuple[Dict[str, McpServerConfig], List[str]]:
    """Load global and workspace mcp.json. Do not follow a link to another file."""
    g_path = _path_keeping_final_component(Path(global_config_path or get_default_global_mcp_path()))
    merged, errors = _load_single_mcp_file(g_path, f"global:{g_path}")
    try:
        w_path = workspace_directory_entry(Path(work_dir), ".tiancode/mcp.json")
    except WorkspacePathError:
        errors.append("安全拦截：工作区 MCP 配置路径越界，未读取。")
        w_path = None
    ws_configs: Dict[str, McpServerConfig] = {}
    ws_errors: List[str] = []
    if w_path is not None:
        ws_configs, ws_errors = _load_single_mcp_file(
            w_path,
            f"workspace:{w_path}",
            sandbox_root=Path(work_dir).resolve(),
        )
    merged.update(ws_configs)
    auto_shell = discover_tianshell_mcp(merged)
    if auto_shell is not None:
        merged["tianshell"] = auto_shell
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
            HttpMcpTransport(
                cfg.url,
                headers=cfg.headers,
                timeout_seconds=cfg.timeout_seconds,
                auth=cfg.auth,
            )
            if cfg.transport == "http"
            else StdioMcpTransport(
                cfg.command,
                args=cfg.args,
                env=cfg.env,
                timeout_seconds=cfg.timeout_seconds,
                cwd=cfg.cwd or str(self.work_dir),
            )
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
        negotiated = getattr(transport, "protocol_version", "") or ""
        self._statuses[name] = McpServerStatus(
            name=name,
            transport=cfg.transport,
            source=cfg.source,
            enabled=True,
            connected=True,
            tool_count=count,
            protocol_version=negotiated,
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
