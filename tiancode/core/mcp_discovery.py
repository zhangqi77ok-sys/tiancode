"""Auto-discovery mechanisms for local companion daemons and MCP servers."""

from __future__ import annotations

import json
import os
import stat
import sys
from pathlib import Path
from typing import Any, Dict, Optional

__all__ = [
    "discover_tianshell_mcp",
    "resolve_tianshell_runtime_path",
]

_DEFAULT_TIANSHELL_TIMEOUT = 5


def _runtime_file(path: Path) -> Path:
    """Absolute runtime.json path. The final name is not resolved."""
    raw = path.expanduser()
    if not raw.is_absolute():
        raw = Path.cwd() / raw
    try:
        parent = raw.parent.resolve()
    except OSError:
        parent = raw.parent.absolute()
    return parent / raw.name


def _is_present_runtime(path: Path) -> bool:
    """True for a regular file or a symlink. Does not follow the final name."""
    try:
        info = os.lstat(path)
    except OSError:
        return False
    return stat.S_ISLNK(info.st_mode) or stat.S_ISREG(info.st_mode)


def resolve_tianshell_runtime_path() -> Optional[Path]:
    """Return the runtime.json path. A final symlink stays visible to the caller."""
    override = os.environ.get("TIANSHELL_RUNTIME_PATH", "").strip()
    if override:
        return _runtime_file(Path(override))
    if os.environ.get("PYTEST_CURRENT_TEST"):
        return None
    home = os.environ.get("TIANSHELL_HOME", "").strip()
    if home:
        return _runtime_file(Path(home) / "runtime.json")
    if sys.platform == "win32":
        appdata = os.environ.get("APPDATA", "").strip()
        if appdata:
            return _runtime_file(Path(appdata) / "tianshell" / "runtime.json")
        return _runtime_file(Path.home() / "AppData" / "Roaming" / "tianshell" / "runtime.json")
    xdg_config = os.environ.get("XDG_CONFIG_HOME", "").strip()
    if xdg_config:
        return _runtime_file(Path(xdg_config) / "tianshell" / "runtime.json")
    cfg_path = _runtime_file(Path.home() / ".config" / "tianshell" / "runtime.json")
    if _is_present_runtime(cfg_path):
        return cfg_path
    return _runtime_file(Path.home() / ".tianshell" / "runtime.json")


def _is_pid_alive(pid: int) -> bool:
    """Check if process with given PID is currently alive without throwing."""
    if pid <= 0:
        return False
    if sys.platform == "win32":
        import ctypes
        from ctypes import wintypes
        kernel32 = ctypes.windll.kernel32
        handle = kernel32.OpenProcess(0x1000, False, pid)  # PROCESS_QUERY_LIMITED_INFORMATION
        if not handle:
            return False
        exit_code = wintypes.DWORD()
        alive = False
        if kernel32.GetExitCodeProcess(handle, ctypes.byref(exit_code)):
            alive = (exit_code.value == 259)  # STILL_ACTIVE
        kernel32.CloseHandle(handle)
        return alive
    try:
        os.kill(pid, 0)
        return True
    except (OSError, ProcessLookupError):
        return False


def discover_tianshell_mcp(
    configured_servers: Optional[Dict[str, Any]] = None,
) -> Optional[Any]:
    """Discover a running local TianShell instance via runtime.json without blocking."""
    from tiancode.core.mcp_client import McpServerConfig

    if configured_servers and "tianshell" in configured_servers:
        return None
    runtime_path = resolve_tianshell_runtime_path()
    if runtime_path is None or runtime_path.is_symlink():
        return None
    from tiancode.core.path_safety import is_shared_hardlink

    if is_shared_hardlink(runtime_path) or not runtime_path.is_file():
        return None
    try:
        flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
        fd = os.open(runtime_path, flags)
        with os.fdopen(fd, "r", encoding="utf-8-sig") as handle:
            data = json.loads(handle.read())
    except (OSError, json.JSONDecodeError):
        return None
    if not isinstance(data, dict):
        return None

    pid = data.get("pid")
    if isinstance(pid, int) and pid > 0 and not _is_pid_alive(pid):
        return None

    mcp_url = str(data.get("mcpUrl") or data.get("mcp_url") or "").strip()
    port = data.get("port")
    if not mcp_url and isinstance(port, int) and port > 0:
        mcp_url = f"http://127.0.0.1:{port}/mcp"
    if not mcp_url:
        return None

    token = str(data.get("token") or "").strip()
    headers = {"Authorization": f"Bearer {token}"} if token else {}
    return McpServerConfig(
        name="tianshell",
        transport="http",
        url=mcp_url,
        headers=headers,
        timeout_seconds=_DEFAULT_TIANSHELL_TIMEOUT,
        disabled=False,
        source=f"auto:{runtime_path}",
    )
