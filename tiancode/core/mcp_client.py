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


_SHELL_NAMES = frozenset({
    "sh", "bash", "dash", "zsh", "ksh", "mksh", "fish", "csh", "tcsh",
    "cmd", "powershell", "pwsh", "busybox",
})
_WRAPPER_NAMES = frozenset({"env", "nice", "nohup", "stdbuf", "time", "command"})
_WRAPPER_VALUE_FLAGS = frozenset({"-n", "-o", "-e", "-u", "--unset"})
_INTERPRETERS = frozenset({
    "node", "nodejs", "bun", "deno", "tsx", "ts-node",
    "python", "python3", "python2", "perl", "ruby", "php",
})
_SCRIPT_RUNNERS = frozenset({"npm", "pnpm", "yarn", "bun"})
_SCRIPT_SHORTCUTS = frozenset({"test", "start", "stop", "restart"})
_EVAL_FLAGS = {
    "node": {"-e", "--eval"},
    "nodejs": {"-e", "--eval"},
    "bun": {"-e", "--eval"},
    "python": {"-c"},
    "python3": {"-c"},
    "python2": {"-c"},
    "perl": {"-e"},
    "ruby": {"-e"},
    "php": {"-r"},
}
_SCRIPT_SUFFIXES = frozenset({
    ".js", ".mjs", ".cjs", ".jsx", ".ts", ".tsx", ".mts", ".cts", ".py", ".rb", ".php",
})
_PACKAGE_LAUNCHERS = frozenset({"npx", "npm", "pnpm", "yarn", "bunx", "uvx"})
_PACKAGE_SPEC = re.compile(r"^@[A-Za-z0-9._-]+/[A-Za-z0-9._-]+(?:@[A-Za-z0-9._+-]+)?$")
_SCOPED_INSTALL = re.compile(r"^(@[A-Za-z0-9._-]+/[A-Za-z0-9._-]+)(?:@.+)?$")
_PLAIN_INSTALL = re.compile(r"^([A-Za-z0-9._-]+)(?:@.+)?$")


def _program_name(token: str) -> str:
    name = Path(token).name.lower()
    if name.endswith(".exe"):
        name = name[:-4]
    return name


def _unwrap_stdio_argv(words: List[str]) -> List[str]:
    """Drop leading env/nice/nohup so the real program is what gets checked."""
    argv = list(words)
    while argv and _program_name(argv[0]) in _WRAPPER_NAMES:
        wrapper = _program_name(argv.pop(0))
        while argv and argv[0].startswith("-"):
            flag = argv.pop(0)
            if flag in _WRAPPER_VALUE_FLAGS and argv and not argv[0].startswith("-"):
                argv.pop(0)
        if wrapper == "env":
            while argv and re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*=.*", argv[0]):
                argv.pop(0)
    return argv


def _looks_like_path(token: str) -> bool:
    if not token or token.startswith("-"):
        return False
    if "/" in token or "\\" in token or token.startswith("."):
        return True
    return Path(token).suffix.lower() in _SCRIPT_SUFFIXES


def _regular_workspace_file(token: str, root: Path, base: Path) -> Optional[str]:
    """Reject a script that is missing, linked, or outside the workspace.

    A regular file inside the project returns None. The workspace launcher
    still refuses to execute it: the file is repository code.
    """
    from tiancode.core.path_safety import is_shared_hardlink

    candidate = Path(token)
    dest = candidate if candidate.is_absolute() else base / candidate
    if dest.is_symlink():
        return "工作区 MCP 的脚本是符号链接，已拒绝启动"
    try:
        resolved = dest.resolve()
        resolved.relative_to(root.resolve())
    except (OSError, ValueError):
        return "工作区 MCP 的脚本越出工作区，已拒绝启动"
    if is_shared_hardlink(dest):
        return "工作区 MCP 的脚本是硬链接，已拒绝启动"
    if not dest.is_file():
        return "工作区 MCP 的脚本必须是工作区内的普通文件，已拒绝启动"
    return None


def _runner_script_name(binary: str, args: List[str]) -> Optional[str]:
    """Return the package.json script name, or None when this is not `run`.

    `bun test` is Bun's runner, not `scripts.test`, so it is not a script name.
    """
    positionals: List[str] = []
    index = 0
    while index < len(args):
        arg = args[index]
        if arg == "--":
            if index + 1 < len(args):
                positionals.append(args[index + 1])
            break
        if arg.startswith("-"):
            index += 1
            continue
        positionals.append(arg)
        index += 1
    if not positionals:
        return None
    if positionals[0] == "run":
        return positionals[1] if len(positionals) > 1 else ""
    if binary != "bun" and positionals[0] in _SCRIPT_SHORTCUTS:
        return positionals[0]
    return None


def _named_package_script_error(root: Path, name: str) -> Optional[str]:
    from tiancode.core.path_safety import is_shared_hardlink
    from tiancode.core.polyglot import _read_owned_text
    from tiancode.tools.execution import is_dangerous_command

    if not name or name.startswith("-"):
        return "工作区 MCP 的 package 脚本名无效，已拒绝启动"
    pkg = root / "package.json"
    if pkg.is_symlink() or is_shared_hardlink(pkg):
        return "工作区 MCP 的 package.json 是符号链接或硬链接，已拒绝启动"
    if not pkg.is_file():
        return None
    text = _read_owned_text(pkg)
    if text is None:
        return "工作区 MCP 的 package.json 无法作为普通文件读取，已拒绝启动"
    try:
        data = json.loads(text)
    except json.JSONDecodeError:
        return None
    scripts = data.get("scripts") if isinstance(data, dict) else None
    body = scripts.get(name) if isinstance(scripts, dict) else None
    if not isinstance(body, str):
        return None
    if is_dangerous_command(body):
        return f"工作区 MCP 的 package 脚本 {name} 含高危命令，已拒绝启动"
    return None


def _interpreter_error(binary: str, args: List[str], root: Path, base: Path) -> Optional[str]:
    script_args = list(args)
    if binary == "deno" and script_args[:1] == ["eval"]:
        return "工作区 MCP 的解释器不能内联执行代码，已拒绝启动"
    if binary == "deno" and script_args[:1] == ["run"]:
        script_args = script_args[1:]
    evals = _EVAL_FLAGS.get(binary, frozenset())
    for arg in script_args:
        head = arg.split("=", 1)[0]
        if arg in evals or head in evals:
            return "工作区 MCP 的解释器不能内联执行代码，已拒绝启动"
    if binary in {"python", "python3", "python2"}:
        for index, arg in enumerate(script_args):
            if arg != "-m":
                continue
            module = script_args[index + 1] if index + 1 < len(script_args) else ""
            if not module or "/" in module or "\\" in module or module.startswith(".") or ".." in module.split("."):
                return "工作区 MCP 的 Python 模块名无效，已拒绝启动"
            return None
    positionals: List[str] = []
    for arg in script_args:
        if arg == "--" or arg.startswith("-"):
            continue
        positionals.append(arg)
    paths = [item for item in positionals if _looks_like_path(item)]
    if not paths:
        if positionals:
            return None
        return "工作区 MCP 的解释器缺少工作区内的脚本文件，已拒绝启动"
    for path in paths:
        error = _regular_workspace_file(path, root, base)
        if error:
            return error
    return None


def _arg_points_at_repo_file(token: str) -> bool:
    """True when the argument is a path shape rather than a package spec.

    The file does not have to exist yet. `server.js` can be created in the
    moment before exec, so the shape is enough to refuse it.
    """
    if not token or token.startswith("-"):
        return False
    if _PACKAGE_SPEC.fullmatch(token):
        return False
    if token.startswith(".") or "/" in token or "\\" in token:
        return True
    return Path(token).suffix.lower() in _SCRIPT_SUFFIXES


def _install_dir_name(token: str) -> Optional[str]:
    """Return `node_modules` path for `@scope/name` or `name`, ignoring a version."""
    if not token or token.startswith("-"):
        return None
    scoped = _SCOPED_INSTALL.fullmatch(token)
    if scoped:
        return scoped.group(1)
    plain = _PLAIN_INSTALL.fullmatch(token)
    if plain:
        return plain.group(1)
    return None


def _package_installed(modules: Path, token: str) -> bool:
    installed = _install_dir_name(token)
    if not installed:
        return False
    candidate = modules.joinpath(*installed.split("/"))
    return candidate.is_symlink() or candidate.exists()


def _installed_package_error(program: str, args: List[str], base: Path) -> Optional[str]:
    """Refuse npx/uvx when the named package or bin already lives in the repo.

    `npx -y @scope/pkg` otherwise executes `node_modules/@scope/pkg` from the
    workspace, which is repository code. A package that is not installed yet
    is still launched from the registry.
    """
    if program not in _PACKAGE_LAUNCHERS:
        return None
    modules = base / "node_modules"
    bindir = modules / ".bin"
    for arg in args:
        if not arg or arg.startswith("-"):
            continue
        if _package_installed(modules, arg):
            return "工作区 MCP 会执行仓库里已安装的包，已拒绝启动"
        if _PACKAGE_SPEC.fullmatch(arg):
            continue
        leaf = Path(arg).name
        if not leaf or leaf in {".", ".."}:
            continue
        binary = bindir / leaf
        if binary.is_symlink() or binary.exists():
            return "工作区 MCP 的参数会命中仓库里已安装的可执行文件，已拒绝启动"
    return None


def _workspace_command_error(
    command: str,
    args: List[str],
    root: Path,
    base: Path,
) -> Optional[str]:
    """Reject a workspace stdio command that would execute a repository file.

    A shell, an interpreter (`node server.js`, `python -m`), a package script,
    a path argument, or an already-installed local package does not start.
    `npx -y @scope/pkg` still starts when that package is not in the repo.
    The same check runs again immediately before the process is spawned.
    """
    if not command:
        return None
    from tiancode.tools.execution import is_dangerous_command

    if is_dangerous_command(" ".join([command, *args])):
        return "工作区 MCP 的 command 含高危命令，已拒绝启动"
    words = _unwrap_stdio_argv([command, *args])
    if not words or not words[0].strip():
        return "工作区 MCP 的 command 无效，已拒绝启动"
    program = _program_name(words[0])
    rest = words[1:]
    if program in _SHELL_NAMES:
        return "工作区 MCP 的 command 是 shell，已拒绝启动"
    if program in _SCRIPT_RUNNERS:
        script_name = _runner_script_name(program, rest)
        if script_name is not None:
            if not script_name:
                return "工作区 MCP 的 package 脚本名无效，已拒绝启动"
            if _looks_like_path(script_name):
                file_error = _regular_workspace_file(script_name, root, base)
                if file_error:
                    return file_error
            else:
                script_error = _named_package_script_error(root, script_name)
                if script_error:
                    return script_error
            return "工作区 MCP 不能运行 package 脚本，那会执行仓库里的文件，已拒绝启动"
    if program in _INTERPRETERS:
        error = _interpreter_error(program, rest, root, base)
        if error:
            return error
        return "工作区 MCP 不能用解释器启动，那会执行仓库里的文件，已拒绝启动"
    if _looks_like_path(words[0]):
        error = _regular_workspace_file(words[0], root, base)
        if error:
            return error
        return "工作区 MCP 不能直接启动工作区内的文件，已拒绝启动"
    for arg in rest:
        if _arg_points_at_repo_file(arg):
            return "工作区 MCP 的参数会指向仓库里的文件，已拒绝启动"
    return _installed_package_error(program, rest, base)


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
        resolved_cwd = ""
        cwd_base = sandbox_root
        if raw_cwd:
            cwd_path = Path(raw_cwd).expanduser()
            resolved = (cwd_path if cwd_path.is_absolute() else (base_dir / cwd_path)).resolve()
            if sandbox_root is not None and not _is_inside(resolved, sandbox_root):
                errors.append(f"MCP 服务 '{name}' 的 cwd 越出工作区，已拒绝加载: {resolved}")
                continue
            resolved_cwd = str(resolved)
            cwd_base = resolved
        if sandbox_root is not None:
            command_error = _workspace_command_error(cmd, args, sandbox_root, cwd_base or sandbox_root)
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
        launch_guard = None
        executable_guard = None
        if cfg.transport != "http" and cfg.source.startswith("workspace:"):
            base = Path(cfg.cwd) if cfg.cwd else self.work_dir
            command = cfg.command
            args = list(cfg.args)
            root = self.work_dir

            def launch_guard(
                command: str = command,
                args: List[str] = args,
                root: Path = root,
                base: Path = base,
            ) -> Optional[str]:
                # Called once here and again inside connect(), immediately before exec.
                return _workspace_command_error(command, args, root, base)

            def executable_guard(resolved: str, root: Path = root) -> Optional[str]:
                if _is_inside(Path(resolved), root):
                    return "工作区 MCP 的可执行文件在仓库内，已拒绝启动"
                return None

            command_error = launch_guard()
            if command_error:
                self._statuses[name] = McpServerStatus(
                    name=name,
                    transport=cfg.transport,
                    source=cfg.source,
                    enabled=True,
                    connected=False,
                    error=command_error,
                )
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
                launch_guard=launch_guard,
                executable_guard=executable_guard,
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
