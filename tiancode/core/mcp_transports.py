"""Synchronous MCP transports (stdio JSON-RPC lines & Streamable HTTP/SSE)."""

from __future__ import annotations

import json
import os
import queue
import shutil
import subprocess
import sys
import tempfile
import threading
import time
import uuid
from collections import deque
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable, Deque, Dict, List, Optional, Sequence, Tuple
from urllib.parse import urljoin, urlparse

import httpx

from tiancode.core.mcp_auth import (
    McpAuthError,
    OAuthTokenProvider,
    build_auth_headers,
    format_401_hint,
)

# Prefer newest known revision; still accept earlier negotiated versions.
_MCP_PROTOCOL_VERSION = "2025-06-18"
_SUPPORTED_PROTOCOL_VERSIONS = frozenset({"2025-06-18", "2025-03-26", "2024-11-05"})
_DEFAULT_TIMEOUT_SECONDS = 30
_MAX_HTTP_ATTEMPTS = 3
# tools/call is not retried on 408, 500, 502, 503, or 504. A gateway can return
# those after the tool has already run. tools/call retries 429 only when the
# body is empty: that is the rate-limit signal from before a handler writes a
# response. Plain text, HTML, or any other non-empty 429 is not sent again,
# because it cannot be told apart from a gateway that already ran the tool.
# A 429 that already carries a JSON-RPC result or error is that answer and is
# returned as-is. initialize and tools/list still retry every 429. A session
# 404 reconnects, but tools/call is not sent again; only read-only methods
# below are.
_RETRYABLE_HTTP_STATUS = frozenset({408, 429, 500, 502, 503, 504})
_TOOLS_CALL_NO_RETRY = frozenset({408, 500, 502, 503, 504})
_SESSION_REPLAY_METHODS = frozenset({
    "tools/list",
    "resources/list",
    "resources/templates/list",
    "prompts/list",
    "ping",
})
_SAFE_BASE_ENV = frozenset({
    "PATH", "SYSTEMROOT", "SYSTEMDRIVE", "COMSPEC", "TEMP", "TMP", "HOME", "USERPROFILE",
    "APPDATA", "LOCALAPPDATA", "HOMEDRIVE", "HOMEPATH", "PROGRAMDATA", "OS",
    "PATHEXT", "WINDIR", "LANG", "NUMBER_OF_PROCESSORS",
})


def _build_scrubbed_env(server_env: Optional[Dict[str, str]] = None) -> Dict[str, str]:
    """Build subprocess env without leaking parent credentials.

    The allowlist used to keep every `PYTHON*` name, including `PYTHON_TOKEN`.
    Sensitive names are dropped first, using the same rule as the shell.
    """
    from tiancode.tools.execution import is_sensitive_env_name

    clean_env: Dict[str, str] = {}
    for key, value in os.environ.items():
        if is_sensitive_env_name(key):
            continue
        upper = key.upper()
        if upper in _SAFE_BASE_ENV or upper.startswith(("PYTHON", "LC_")):
            clean_env[key] = value
    if server_env:
        clean_env.update({str(k): str(v) for k, v in server_env.items()})
    return clean_env


def _init_params(protocol_version: str = _MCP_PROTOCOL_VERSION) -> Dict[str, Any]:
    return {
        "protocolVersion": protocol_version,
        "capabilities": {},
        "clientInfo": {"name": "tiancode", "version": "0.1.0"},
    }


def _negotiate_protocol_version(result: Any) -> str:
    """Validate initialize result protocolVersion against client support set."""
    if not isinstance(result, dict):
        raise RuntimeError("MCP initialize 响应无效：缺少 result 对象")
    version = str(result.get("protocolVersion") or "").strip()
    if not version:
        raise RuntimeError("MCP initialize 响应缺少 protocolVersion")
    if version not in _SUPPORTED_PROTOCOL_VERSIONS:
        supported = ", ".join(sorted(_SUPPORTED_PROTOCOL_VERSIONS, reverse=True))
        raise RuntimeError(f"MCP 协议版本不兼容: server={version}；客户端支持: {supported}")
    return version


def _unwrap_rpc_result(msg: Any) -> Any:
    if not isinstance(msg, dict):
        raise RuntimeError("MCP 通道已关闭或响应格式无效")
    if msg.get("error"):
        err = msg["error"]
        raise RuntimeError(err.get("message") if isinstance(err, dict) else str(err))
    return msg.get("result")


def _extract_tool_text(result: Any) -> str:
    """Convert MCP tools/call response payload into readable text."""
    if not isinstance(result, dict):
        return str(result or "")
    parts = [
        str(item["text"]) if isinstance(item, dict) and item.get("text") is not None else json.dumps(item, ensure_ascii=False)
        for item in (result.get("content") or [])
    ]
    text = "\n".join(p for p in parts if p).strip() or json.dumps(result, ensure_ascii=False)
    return f"❌ MCP 工具返回错误: {text}" if result.get("isError") and not text.startswith("❌") else text


def _http_body_is_empty(resp: Any) -> bool:
    return not (getattr(resp, "text", "") or "").strip()


def _jsonrpc_reply(resp: Any) -> Optional[Dict[str, Any]]:
    """JSON-RPC result or error already present in an HTTP body, else None.

    A rate-limit page or an empty 429 is not a JSON-RPC reply. A body that
    contains `result` or `error` means this request was answered.
    """
    text = (getattr(resp, "text", "") or "").strip()
    if not text or text[0] not in "{[":
        return None
    try:
        payload = json.loads(text)
    except json.JSONDecodeError:
        return None
    if isinstance(payload, dict) and ("result" in payload or "error" in payload):
        return payload
    return None


_NPM_LOCATION_FLAGS = frozenset({
    "--prefix", "--cache", "--userconfig", "--globalconfig", "--script-shell", "--npx-cache",
})


def _trusted_script_shell() -> Optional[str]:
    """A shell outside the workspace. npm lifecycle scripts must not use a repo path."""
    if os.name == "nt":
        root = os.environ.get("SystemRoot") or os.environ.get("SYSTEMROOT") or ""
        if root:
            candidate = Path(root) / "System32" / "cmd.exe"
            if candidate.is_file():
                return str(candidate)
        return None
    for candidate in ("/bin/sh", "/usr/bin/sh"):
        if Path(candidate).is_file():
            return candidate
    return None


def _strip_npm_location_flags(args: List[str]) -> List[str]:
    """Drop npm location flags. Flags after `--` belong to the package, not npm."""
    cleaned: List[str] = []
    index = 0
    passthrough = False
    while index < len(args):
        arg = args[index]
        if arg == "--":
            passthrough = True
            cleaned.append(arg)
            index += 1
            continue
        if not passthrough:
            name, eq, _value = arg.partition("=")
            if name in _NPM_LOCATION_FLAGS:
                index += 1
                if not eq and index < len(args) and args[index] != "--":
                    index += 1
                continue
        cleaned.append(arg)
        index += 1
    return cleaned


@dataclass
class LauncherIsolation:
    """Args and env pins for one workspace package launch.

    `directory` is a temp tree that must outlive the child. Prefixes and keys
    are removed from the scrubbed environment, then `env` is applied, so a
    project `.npmrc`, `.yarnrc.yml`, or `UV_INDEX_URL` cannot outrank the pin.
    """

    args: List[str]
    directory: Optional[Path] = None
    env: Dict[str, str] = field(default_factory=dict)
    drop_prefixes: Tuple[str, ...] = ()
    drop_keys: Tuple[str, ...] = ()


_PNPM_VALUE_FLAGS = frozenset({
    "--dir", "--filter", "--prefix", "--cwd", "--config", "-C", "-F",
})
_YARN_VALUE_FLAGS = frozenset({"--cwd", "--cache-folder"})
_UV_GLOBAL_VALUE_FLAGS = frozenset({
    "--directory", "--project", "--config-file", "--cache-dir", "--python", "-p",
})
# These choose a registry, a file of pins, or a local wheelhouse. They are
# removed so the launch uses the public index pinned below. `--from` and
# `--with` stay: they choose the package, and a path-shaped value is already
# refused before spawn.
_UV_INDEX_FLAGS = frozenset({
    "--index", "--default-index", "--index-url", "--extra-index-url", "--find-links",
    "--index-strategy", "--config-file", "--env-file",
    "--constraints", "--build-constraints", "--overrides",
    "-i", "-f", "-c", "-b",
})
_UV_BOOL_FLAGS = frozenset({"--no-index", "--no-config", "--isolated"})
_UV_DROP_KEYS = (
    "UV_INDEX_URL", "UV_DEFAULT_INDEX", "UV_EXTRA_INDEX_URL", "UV_FIND_LINKS",
    "UV_INDEX", "UV_CONSTRAINT", "UV_OVERRIDE", "UV_BUILD_CONSTRAINT",
    "UV_CONFIG_FILE", "UV_CACHE_DIR",
)
_PUBLIC_NPM_REGISTRY = "https://registry.npmjs.org"
_PUBLIC_YARN_REGISTRY = "https://registry.yarnpkg.com"
_PUBLIC_PYPI = "https://pypi.org/simple"
# These choose an index, a requirements file, or a wheelhouse. They are removed
# so a workspace `pip install` uses the public index pinned below. `--isolated`
# does not ignore `PIP_CONFIG_FILE`; the env drop does.
_PIP_VALUE_FLAGS = frozenset({
    "-i", "--index-url", "--extra-index-url", "-f", "--find-links",
    "-r", "--requirement", "-c", "--constraint", "--trusted-host", "--cache-dir",
})
_PIP_BOOL_FLAGS = frozenset({"--no-index", "--isolated", "--no-cache-dir"})


# Corepack forwards only when argv[0] is one of these bins, optionally `name@version`.
# `pnpx` is pnpm's dlx bin. `yarnpkg` is yarn. A flag value is not argv[0], so
# `corepack --install-directory pnpm ...` is not a pnpm launch.
_COREPACK_BIN = {
    "npm": "npm",
    "npx": "npx",
    "pnpm": "pnpm",
    "pnpx": "pnpm",
    "yarn": "yarn",
    "yarnpkg": "yarn",
}


def _corepack_forward_plan(token: str) -> Optional[Tuple[str, str, List[str]]]:
    """Return `(spec, program, leading)` when corepack would forward `token`.

    `spec` is the argv token corepack itself must still see, so `pnpm@9` keeps
    its version. `program` is the tool the rest of the checks understand.
    `leading` is inserted in front of the remaining arguments: `pnpx` is
    `pnpm dlx`. `None` means corepack does not forward. The first argument is
    the only one that can forward; `--install-directory pnpm` does not.
    """
    if not token or token.startswith("-") or token == "--":
        return None
    name, sep, version = token.partition("@")
    program = _COREPACK_BIN.get(name)
    if program is None:
        return None
    if name == "pnpx":
        spec = f"pnpm@{version}" if sep else "pnpm"
        return spec, "pnpm", ["dlx"]
    if name == "yarnpkg":
        return token, "yarn", []
    return token, program, []


def _peel_corepack(args: Sequence[str]) -> Optional[Tuple[List[str], str, str, List[str]]]:
    """Split `corepack <bin>[@version] <rest>` into flags, the spec, the program, and the rest.

    Corepack decides from its first argument alone. Flags before that argument
    belong to corepack itself (`enable`, `use`) and are not a package-manager
    launch, even when a flag's value is the word `pnpm`.
    """
    if not args:
        return None
    plan = _corepack_forward_plan(args[0])
    if plan is None:
        return None
    spec, program, leading = plan
    return [], spec, program, [*leading, *list(args[1:])]


def _program_name(command: str) -> str:
    program = Path(command).name.lower()
    if program.endswith(".exe"):
        program = program[:-4]
    return program


def _first_positional(args: Sequence[str], value_flags: frozenset) -> str:
    """First word that is not an option. A flag in `value_flags` consumes the next word."""
    index = 0
    while index < len(args):
        arg = args[index]
        if arg == "--":
            return ""
        if arg.startswith("-"):
            name, eq, _value = arg.partition("=")
            if name in value_flags and not eq and index + 1 < len(args):
                index += 2
                continue
            index += 1
            continue
        return arg
    return ""


def _strip_named_flags(
    args: Sequence[str],
    value_flags: frozenset,
    bool_flags: frozenset = frozenset(),
) -> List[str]:
    """Drop flags in these sets, including `--flag=value`. Flags after `--` stay.

    A boolean flag does not consume the next word. A value flag does, unless
    the value is attached (`--flag=value` or `-iURL`).
    """
    cleaned: List[str] = []
    index = 0
    passthrough = False
    while index < len(args):
        arg = args[index]
        if arg == "--":
            passthrough = True
            cleaned.append(arg)
            index += 1
            continue
        if not passthrough:
            name, eq, _value = arg.partition("=")
            short = arg[:2] if len(arg) > 2 and arg[0] == "-" and not arg.startswith("--") else ""
            if name in bool_flags:
                index += 1
                continue
            if name in value_flags or short in value_flags:
                index += 1
                attached = bool(eq) or bool(short and short in value_flags)
                if not attached and index < len(args) and args[index] != "--":
                    index += 1
                continue
        cleaned.append(arg)
        index += 1
    return cleaned


def _pnpm_location_flag(arg: str) -> bool:
    name = arg.partition("=")[0]
    return name in {"--dir", "-C", "--prefix", "--registry"} or name.startswith("--config")


def _strip_pnpm_location_flags(args: Sequence[str]) -> List[str]:
    cleaned: List[str] = []
    index = 0
    passthrough = False
    while index < len(args):
        arg = args[index]
        if arg == "--":
            passthrough = True
            cleaned.append(arg)
            index += 1
            continue
        if not passthrough and _pnpm_location_flag(arg):
            _name, eq, _value = arg.partition("=")
            index += 1
            if not eq and not arg.startswith("--config.") and index < len(args) and args[index] != "--":
                index += 1
            continue
        cleaned.append(arg)
        index += 1
    return cleaned


def _empty_launch_dir(prefix: str) -> Path:
    root = Path(tempfile.mkdtemp(prefix=prefix))
    (root / "cache").mkdir()
    return root


def _isolate_pnpm_dlx(args: Sequence[str]) -> LauncherIsolation:
    """Run `pnpm dlx` from an empty project so the repo `.npmrc` is not read.

    `--dir` is what stops pnpm from inheriting the workspace registry, including
    a scoped `@scope:registry`. `--config.registry` alone does not. The tool's
    cwd stays the workspace; only pnpm's project directory moves.
    """
    root = _empty_launch_dir("tiancode-pnpm-")
    (root / "store").mkdir()
    flags = [
        "--dir", str(root),
        f"--config.registry={_PUBLIC_NPM_REGISTRY}",
        f"--config.cache-dir={root / 'cache'}",
        f"--config.store-dir={root / 'store'}",
    ]
    shell = _trusted_script_shell()
    if shell:
        flags.append(f"--config.script-shell={shell}")
    return LauncherIsolation(
        args=flags + _strip_pnpm_location_flags(args),
        directory=root,
        env={"npm_config_registry": _PUBLIC_NPM_REGISTRY, **(
            {"npm_config_script_shell": shell} if shell else {}
        )},
        drop_prefixes=("npm_config_", "pnpm_config_"),
    )


def _isolate_yarn_dlx(args: Sequence[str]) -> LauncherIsolation:
    """Keep `yarn dlx` off the repo's `.yarnrc.yml` and `yarnPath`.

    Yarn looks for `YARN_RC_FILENAME` by name, starting at the tool cwd. A
    random name matches nothing the repo planted, so `yarnPath` and
    `npmScopes` are not loaded. `YARN_NPM_REGISTRY_SERVER` does not override a
    scope entry; ignoring the rc file does.
    """
    root = _empty_launch_dir("tiancode-yarn-")
    filename = f"tiancode-yarnrc-{uuid.uuid4().hex}.yml"
    return LauncherIsolation(
        args=list(args),
        directory=root,
        env={
            "YARN_IGNORE_PATH": "1",
            "YARN_RC_FILENAME": filename,
            "YARN_ENABLE_SCRIPTS": "false",
            "YARN_CACHE_FOLDER": str(root / "cache"),
            "YARN_NPM_REGISTRY_SERVER": _PUBLIC_YARN_REGISTRY,
        },
        drop_prefixes=("yarn_",),
    )


def _uv_words(args: Sequence[str], limit: int = 2) -> List[str]:
    """Leading positionals. A uv global flag and its value are not positionals."""
    words: List[str] = []
    index = 0
    while index < len(args) and len(words) < limit:
        arg = args[index]
        if arg == "--":
            break
        if arg.startswith("-"):
            name, eq, _value = arg.partition("=")
            if name in _UV_GLOBAL_VALUE_FLAGS and not eq and index + 1 < len(args):
                index += 2
                continue
            index += 1
            continue
        words.append(arg)
        index += 1
    return words


def _uv_tool_run(program: str, args: Sequence[str]) -> bool:
    if program == "uvx":
        return True
    if program != "uv":
        return False
    return _uv_words(args)[:2] in (["tool", "run"], ["tool", "uvx"])


def _isolate_uv_tool(args: Sequence[str]) -> LauncherIsolation:
    """Pin uvx / `uv tool run` at the public index and a private cache.

    `--no-config` does not override `UV_INDEX_URL`, so the env pin is required.
    `--isolated` ignores a tool that was already installed from another index.
    """
    root = _empty_launch_dir("tiancode-uvx-")
    flags = ["--no-config", "--isolated", "--cache-dir", str(root / "cache")]
    return LauncherIsolation(
        args=flags + _strip_named_flags(
            args,
            _UV_INDEX_FLAGS | {"--cache-dir", "--directory", "--project"},
            _UV_BOOL_FLAGS,
        ),
        directory=root,
        env={
            "UV_INDEX_URL": _PUBLIC_PYPI,
            "UV_DEFAULT_INDEX": _PUBLIC_PYPI,
            "UV_CACHE_DIR": str(root / "cache"),
        },
        drop_keys=_UV_DROP_KEYS,
    )


# These uv commands build the project or run its environment. `uv tool run` is
# not in this set; that launch is pinned above. `uv pip sync` and `uv pip
# compile` read a requirements file that can name a local package.
_UV_PROJECT_COMMANDS = frozenset({"run", "sync", "add", "remove", "build"})
_UV_PIP_REFUSED = frozenset({"sync", "compile"})
_UV_PIP_VALUE_FLAGS = _UV_INDEX_FLAGS | _UV_GLOBAL_VALUE_FLAGS | frozenset({
    "--requirements", "--requirement", "-r",
    "--group", "--extra",
    "--editable", "-e",
    "--target", "-t",
    "--prefix",
    "--excludes",
    "--python-version",
    "--no-editable-package",
    "--no-sources-package",
    "--no-build-isolation-package",
})
_UV_PIP_BOOL_FLAGS = _UV_BOOL_FLAGS | frozenset({
    "--system",
    "--no-sources",
    "--no-build-isolation",
    "--all-extras",
})
# `UV_PYTHON` selects the interpreter even when `--system` is present, so a
# workspace path in that variable executes. uvx keeps `UV_PYTHON` on purpose;
# `uv pip` cannot.
_UV_PIP_DROP_KEYS = _UV_DROP_KEYS + (
    "UV_PYTHON",
    "UV_PROJECT_ENVIRONMENT",
    "UV_SYSTEM_PYTHON",
    "UV_NO_SOURCES",
    "UV_NO_CONFIG",
    "VIRTUAL_ENV",
)


def _split_after_positionals(
    args: Sequence[str],
    count: int,
    value_flags: frozenset,
) -> Tuple[List[str], List[str]]:
    """Split just after the Nth positional. Text after `--` stays in the tail."""
    index = 0
    seen = 0
    while index < len(args):
        arg = args[index]
        if arg == "--":
            return list(args[:index]), list(args[index:])
        if arg.startswith("-"):
            name, eq, _value = arg.partition("=")
            short = ""
            if len(arg) > 2 and arg.startswith("-") and not arg.startswith("--"):
                short = arg[:2]
            index += 1
            takes_value = name in value_flags or short in value_flags
            attached = bool(eq) or bool(short and short in value_flags)
            if takes_value and not attached and index < len(args) and args[index] != "--":
                index += 1
            continue
        seen += 1
        index += 1
        if seen == count:
            return list(args[:index]), list(args[index:])
    return list(args), []


def _isolate_uv_pip(args: Sequence[str]) -> LauncherIsolation:
    """Pin `uv pip` off the repo's config, cache, and virtualenv.

    A project `uv.toml` selects the index. `--no-config` ignores that file, but
    not `UV_INDEX_URL`, so the env pin is required. uv also runs `.venv/bin/python`
    while it looks for an environment, and `UV_PYTHON` still wins over `--system`.
    `--group` can name a path package even with `--no-config`, so group and
    source flags are removed. `--no-sources` and the public index are added for
    `install` only: `uv pip list` rejects those flags. Requirement files are
    removed, because they can point back at a workspace path. Flags after `--`
    stay.
    """
    root = _empty_launch_dir("tiancode-uvpip-")
    cleaned = _strip_named_flags(args, _UV_PIP_VALUE_FLAGS, _UV_PIP_BOOL_FLAGS)
    head, tail = _split_after_positionals(cleaned, 2, _UV_PIP_VALUE_FLAGS)
    pins = ["--system"]
    if _uv_words(cleaned)[1:2] == ["install"]:
        pins.extend([
            "--no-sources",
            "--default-index", _PUBLIC_PYPI,
            "--index-url", _PUBLIC_PYPI,
        ])
    return LauncherIsolation(
        args=["--no-config", "--cache-dir", str(root / "cache"), *head, *pins, *tail],
        directory=root,
        env={
            "UV_INDEX_URL": _PUBLIC_PYPI,
            "UV_DEFAULT_INDEX": _PUBLIC_PYPI,
            "UV_CACHE_DIR": str(root / "cache"),
            "UV_NO_CONFIG": "1",
            "UV_NO_SOURCES": "1",
            "UV_SYSTEM_PYTHON": "1",
        },
        drop_keys=_UV_PIP_DROP_KEYS,
    )


def _is_pip(program: str) -> bool:
    """True for `pip`, `pip3`, and `pip3.12`. `pipx` is a different program."""
    if program in {"pip", "pip3"}:
        return True
    return program.startswith("pip3.") and program[5:].isdigit()


def _isolate_pip(args: Sequence[str]) -> LauncherIsolation:
    """Pin pip at the public index and a private cache.

    `--isolated` ignores user config and `PIP_*` variables, but it still loads
    `PIP_CONFIG_FILE`, so that name is dropped from the environment as well.
    Index, find-links, and requirement flags in the arguments are removed:
    a requirements file can point pip back at a workspace path. Flags after
    `--` belong to the installed program and stay put.
    """
    root = _empty_launch_dir("tiancode-pip-")
    cleaned = _strip_named_flags(args, _PIP_VALUE_FLAGS, _PIP_BOOL_FLAGS)
    pins = ["--index-url", _PUBLIC_PYPI, "--cache-dir", str(root / "cache")]
    if "--" in cleaned:
        at = cleaned.index("--")
        cleaned = cleaned[:at] + pins + cleaned[at:]
    else:
        cleaned = cleaned + pins
    return LauncherIsolation(
        args=["--isolated", *cleaned],
        directory=root,
        env={"PIP_INDEX_URL": _PUBLIC_PYPI, "PIP_CACHE_DIR": str(root / "cache")},
        drop_prefixes=("PIP_",),
    )


def isolate_workspace_npm(command: str, args: List[str]) -> LauncherIsolation:
    """Point a workspace package launch away from the repo's config and bins.

    npx and `npm exec` get an empty prefix. `pnpm dlx` gets an empty `--dir`.
    `yarn dlx`, uvx, pip, and `uv pip` are pinned with env and index flags.
    `corepack pnpm` and `corepack yarn` are the same launch, because corepack
    forwards its first argument. `corepack pnpx` is `pnpm dlx`. `corepack pnpm@9`
    keeps the version. A flag before that argument is corepack's own flag, not
    the forwarded program. `bunx`, `pnpm exec`, `yarn exec`, `pipx`, `cargo`,
    `rustup`, poetry, bundler, composer, gem, and uv's project commands are
    refused before this runs: they execute workspace code and have no prefix
    that keeps the tool's cwd in the repo. `uv pip sync` and `uv pip compile`
    are refused too, because the requirements file is the command.
    The caller's flags after `--` are left untouched. A command this function
    does not isolate is returned unchanged, with no directory.
    """
    program = _program_name(command)
    plain = list(args)
    if program == "corepack":
        peeled = _peel_corepack(plain)
        if peeled is None:
            return LauncherIsolation(args=plain)
        prefix, spec, tool, rest = peeled
        isolated = isolate_workspace_npm(tool, rest)
        isolated.args = prefix + [spec, *isolated.args]
        return isolated
    if program == "pnpm" and _first_positional(plain, _PNPM_VALUE_FLAGS) == "dlx":
        return _isolate_pnpm_dlx(plain)
    if program == "yarn" and _first_positional(plain, _YARN_VALUE_FLAGS) == "dlx":
        return _isolate_yarn_dlx(plain)
    if _uv_tool_run(program, plain):
        return _isolate_uv_tool(plain)
    if program == "uv":
        words = _uv_words(plain)
        if len(words) >= 2 and words[0] == "pip" and words[1] not in _UV_PIP_REFUSED:
            return _isolate_uv_pip(plain)
    if _is_pip(program):
        return _isolate_pip(plain)
    if program not in {"npx", "npm"}:
        return LauncherIsolation(args=plain)
    if program == "npm" and _first_positional(plain, frozenset()) not in {"exec", "x"}:
        return LauncherIsolation(args=plain)
    root = _empty_launch_dir("tiancode-npx-")
    (root / "user.npmrc").write_text("", encoding="utf-8")
    (root / "global.npmrc").write_text("", encoding="utf-8")
    flags = [
        "--prefix", str(root),
        "--cache", str(root / "cache"),
        "--userconfig", str(root / "user.npmrc"),
        "--globalconfig", str(root / "global.npmrc"),
    ]
    shell = _trusted_script_shell()
    if shell:
        flags.extend(["--script-shell", shell])
    return LauncherIsolation(args=flags + _strip_npm_location_flags(plain), directory=root)


def _apply_launcher_env(env: Dict[str, str], isolated: LauncherIsolation) -> Dict[str, str]:
    """Drop project package-manager config, then apply the pins.

    Matching is case-insensitive. Pins are written after the drop, so a parent
    `npm_config_registry` cannot survive by using a different spelling.
    """
    if not isolated.drop_prefixes and not isolated.drop_keys and not isolated.env:
        return env
    prefixes = tuple(item.lower() for item in isolated.drop_prefixes)
    drops = {item.lower() for item in isolated.drop_keys}
    cleaned: Dict[str, str] = {}
    for key, value in env.items():
        lowered = key.lower()
        if any(lowered.startswith(prefix) for prefix in prefixes):
            continue
        if lowered in drops:
            continue
        cleaned[key] = value
    cleaned.update(isolated.env)
    return cleaned


def _retry_wait_seconds(attempt: int, resp: Any) -> float:
    """Backoff for one retry. Honor a short Retry-After; otherwise double a small delay."""
    headers = getattr(resp, "headers", {}) or {}
    raw = headers.get("Retry-After") or headers.get("retry-after") or ""
    try:
        seconds = float(str(raw).strip())
    except (TypeError, ValueError):
        seconds = 0.0
    if 0 < seconds <= 2:
        return seconds
    return min(0.2, 0.05 * (2 ** attempt))


def _first_sse_endpoint(resp: Any) -> str:
    """Return the first SSE endpoint URI. Stop there; do not drain the rest of the stream."""
    event_name = ""
    iterator = getattr(resp, "iter_lines", None)
    if callable(iterator):
        lines = iterator()
    else:
        lines = (getattr(resp, "text", "") or "").splitlines()
    for line in lines:
        if isinstance(line, bytes):
            line = line.decode("utf-8", errors="replace")
        stripped = str(line).strip()
        if not stripped:
            event_name = ""
            continue
        if stripped.startswith("event:"):
            event_name = stripped[6:].strip()
            continue
        if not stripped.startswith("data:"):
            continue
        candidate = stripped[5:].strip()
        if event_name == "endpoint" or (
            not event_name and (candidate.startswith("/") or candidate.startswith("http"))
        ):
            return candidate
    return ""


def _resolve_sse_endpoint(base_url: str, endpoint: str) -> str:
    if endpoint.startswith("http://") or endpoint.startswith("https://"):
        return endpoint
    if endpoint.startswith("/"):
        parsed = urlparse(base_url)
        return f"{parsed.scheme}://{parsed.netloc}{endpoint}"
    base = base_url if base_url.endswith("/") else base_url.rsplit("/", 1)[0] + "/"
    return urljoin(base, endpoint)


def _parse_sse_payload(text: str, req_id: Any) -> Dict[str, Any]:
    """Extract the matching JSON-RPC envelope from an SSE event stream."""
    last_obj: Dict[str, Any] = {}
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped.startswith("data:") or stripped[5:].strip() in ("", "[DONE]"):
            continue
        try:
            obj = json.loads(stripped[5:].strip())
        except json.JSONDecodeError:
            continue
        if isinstance(obj, dict):
            last_obj = obj
            if obj.get("id") == req_id or "result" in obj or "error" in obj:
                return obj
    if not last_obj:
        raise RuntimeError("MCP SSE 响应中未找到有效 JSON-RPC 数据")
    return last_obj


class StdioMcpTransport:
    """Synchronous JSON-RPC 2.0 client over subprocess stdin/stdout lines."""

    def __init__(
        self,
        command: str,
        args: Optional[List[str]] = None,
        env: Optional[Dict[str, str]] = None,
        timeout_seconds: int = _DEFAULT_TIMEOUT_SECONDS,
        cwd: Optional[str] = None,
        launch_guard: Optional[Callable[[], Optional[str]]] = None,
        executable_guard: Optional[Callable[[str], Optional[str]]] = None,
    ) -> None:
        self.command = command
        self.args = list(args or [])
        self.env = dict(env or {})
        self.timeout_seconds = max(1, int(timeout_seconds))
        self.cwd = cwd
        self._launch_guard = launch_guard
        self._executable_guard = executable_guard
        self._isolate_dir: Optional[Path] = None
        self._launcher = LauncherIsolation(list(self.args))
        self._proc: Optional[subprocess.Popen[str]] = None
        self._lock = threading.Lock()
        self._pending: Dict[int, queue.Queue[Any]] = {}
        self._next_id = 0
        self._stderr_tail: Deque[str] = deque(maxlen=15)
        self._stderr_done = threading.Event()
        self.protocol_version: str = _MCP_PROTOCOL_VERSION

    def connect(self) -> None:
        """Start subprocess and execute MCP initialize handshake.

        Workspace servers pass guards that run here, after config load and
        immediately before exec. The child is given the absolute binary that
        just passed the guard, so PATH is not searched again.
        """
        if self._launch_guard is not None:
            error = self._launch_guard()
            if error:
                raise RuntimeError(error)
            isolated = isolate_workspace_npm(self.command, self.args)
            self.args = isolated.args
            self._isolate_dir = isolated.directory
            self._launcher = isolated
        cmd_bin = shutil.which(self.command) or self.command
        if self._executable_guard is not None:
            error = self._executable_guard(cmd_bin)
            if error:
                raise RuntimeError(error)
        flags = 0x08000000 if sys.platform == "win32" else 0
        self._stderr_tail.clear()
        self._stderr_done.clear()
        self._proc = subprocess.Popen(
            [cmd_bin, *self.args],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            encoding="utf-8",
            errors="replace",
            bufsize=1,
            env=_apply_launcher_env(_build_scrubbed_env(self.env), self._launcher),
            cwd=self.cwd,
            creationflags=flags,
            start_new_session=(sys.platform != "win32"),
        )
        threading.Thread(target=self._reader_loop, daemon=True).start()
        threading.Thread(target=self._drain_stderr, daemon=True).start()
        try:
            init_result = self._request("initialize", _init_params())
            self.protocol_version = _negotiate_protocol_version(init_result)
            self._send_line({"jsonrpc": "2.0", "method": "notifications/initialized"})
        except Exception:
            self.close()
            raise

    def list_tools(self) -> List[Dict[str, Any]]:
        return _list_tools_paginated(self._request)

    def call_tool(self, tool_name: str, arguments: Dict[str, Any]) -> str:
        return _extract_tool_text(self._request("tools/call", {"name": tool_name, "arguments": dict(arguments or {})}))

    def _send_line(self, payload: Dict[str, Any]) -> None:
        if not self._proc or not self._proc.stdin:
            raise RuntimeError("MCP stdio 进程未启动")
        self._proc.stdin.write(json.dumps(payload, ensure_ascii=False) + "\n")
        self._proc.stdin.flush()

    def _request(self, method: str, params: Dict[str, Any]) -> Any:
        with self._lock:
            self._next_id += 1
            req_id = self._next_id
            waiter: queue.Queue[Any] = queue.Queue(maxsize=1)
            self._pending[req_id] = waiter
        try:
            self._send_line({"jsonrpc": "2.0", "id": req_id, "method": method, "params": params})
            msg = waiter.get(timeout=self.timeout_seconds)
            if msg is None:
                self._stderr_done.wait(0.25)
                tail = " | ".join(self._stderr_tail)
                raise RuntimeError(f"MCP 通道已关闭: {tail}" if tail else "MCP 通道已关闭或响应格式无效")
            return _unwrap_rpc_result(msg)
        except queue.Empty as exc:
            self.close()
            tail = " | ".join(self._stderr_tail)
            detail = f" ({tail})" if tail else ""
            raise TimeoutError(f"MCP 请求超时 ({self.timeout_seconds}s): {method}{detail}") from exc
        finally:
            with self._lock:
                self._pending.pop(req_id, None)

    def _reader_loop(self) -> None:
        proc = self._proc
        if not proc or not proc.stdout:
            return
        try:
            for line in proc.stdout:
                raw = line.strip()
                if not raw:
                    continue
                try:
                    msg = json.loads(raw)
                except json.JSONDecodeError:
                    continue
                if isinstance(msg, dict) and "id" in msg:
                    with self._lock:
                        waiter = _pending_waiter(self._pending, msg["id"])
                    if waiter is not None:
                        waiter.put_nowait(msg)
        finally:
            self._wake_pending()

    def _drain_stderr(self) -> None:
        proc = self._proc
        try:
            if proc and proc.stderr:
                for line in proc.stderr:
                    text = line.strip()
                    if text:
                        self._stderr_tail.append(text[:240])
        finally:
            self._stderr_done.set()

    def _wake_pending(self) -> None:
        with self._lock:
            waiters = list(self._pending.values())
            self._pending.clear()
        for waiter in waiters:
            try:
                waiter.put_nowait(None)
            except queue.Full:
                pass

    def close(self) -> None:
        """Terminate subprocess cleanly before closing stdout/stderr pipes."""
        proc, self._proc = self._proc, None
        isolate_dir, self._isolate_dir = self._isolate_dir, None
        self._wake_pending()
        if not proc:
            if isolate_dir is not None:
                shutil.rmtree(isolate_dir, ignore_errors=True)
            return
        try:
            if proc.stdin:
                proc.stdin.close()
        except OSError:
            pass
        if proc.poll() is None:
            try:
                proc.wait(timeout=0.35)
            except subprocess.TimeoutExpired:
                from tiancode.tools.execution import kill_process_tree
                kill_process_tree(proc)
        for stream in (proc.stdout, proc.stderr):
            try:
                if stream:
                    stream.close()
            except OSError:
                pass
        if isolate_dir is not None:
            shutil.rmtree(isolate_dir, ignore_errors=True)


def _pending_waiter(pending: Dict[Any, Any], msg_id: Any) -> Any:
    """Find the waiter for a JSON-RPC id. Some servers stringify a numeric id."""
    if isinstance(msg_id, bool):
        return None
    if msg_id in pending:
        return pending[msg_id]
    alt: Any = None
    if isinstance(msg_id, str) and msg_id.isdecimal():
        try:
            alt = int(msg_id)
        except ValueError:
            return None
    elif isinstance(msg_id, int):
        alt = str(msg_id)
    if alt is None:
        return None
    return pending.get(alt)


def _list_tools_paginated(request_fn) -> List[Dict[str, Any]]:
    """Collect tools/list pages following optional nextCursor."""
    tools: List[Dict[str, Any]] = []
    cursor: Optional[str] = None
    for _ in range(50):
        params: Dict[str, Any] = {}
        if cursor:
            params["cursor"] = cursor
        result = request_fn("tools/list", params) or {}
        tools.extend(list(result.get("tools") or []))
        cursor = result.get("nextCursor") or result.get("next_cursor")
        if not cursor:
            break
    return tools


class HttpMcpTransport:
    """Synchronous Streamable HTTP / SSE MCP transport backed by httpx."""

    def __init__(
        self,
        url: str,
        headers: Optional[Dict[str, str]] = None,
        timeout_seconds: int = _DEFAULT_TIMEOUT_SECONDS,
        http_client: Any = None,
        auth: Optional[Dict[str, Any]] = None,
        auth_provider: Any = None,
    ) -> None:
        self.url = url
        self.headers = dict(headers or {})
        self.timeout_seconds = max(1, int(timeout_seconds))
        self._auth_cfg = dict(auth or {}) if auth else {}
        self._owns_client = http_client is None
        try:
            self._client = http_client or httpx.Client(timeout=self.timeout_seconds)
        except Exception:
            self._client = httpx.Client(timeout=self.timeout_seconds, trust_env=False)
        self._auth_provider = auth_provider
        self._owns_auth_provider = False
        self.session_id: Optional[str] = None
        self.protocol_version: str = _MCP_PROTOCOL_VERSION
        self._initialized = False
        self._next_id = 0
        self._legacy_sse = False
        self._post_endpoint = self.url
        self._rpc_lock = threading.RLock()
        self._sleeper = time.sleep

    def connect(self) -> None:
        with self._rpc_lock:
            self._connect_unlocked()

    def _connect_unlocked(self) -> None:
        self._ensure_auth_headers(force_refresh=False)
        try:
            init_result = self._request("initialize", _init_params(), allow_legacy_fallback=True)
        except Exception as first_exc:
            # Streamable HTTP may 401 before/during initialize; refresh once then retry.
            if self._maybe_refresh_auth_from_error(first_exc):
                init_result = self._request("initialize", _init_params(), allow_legacy_fallback=True)
            else:
                raise
        self.protocol_version = _negotiate_protocol_version(init_result)
        self._initialized = True
        self._post({"jsonrpc": "2.0", "method": "notifications/initialized"}, expect_response=False)

    def list_tools(self) -> List[Dict[str, Any]]:
        return _list_tools_paginated(self._request)

    def call_tool(self, tool_name: str, arguments: Dict[str, Any]) -> str:
        return _extract_tool_text(self._request("tools/call", {"name": tool_name, "arguments": dict(arguments or {})}))

    def _ensure_auth_headers(self, *, force_refresh: bool) -> None:
        if self._auth_provider is None and self._auth_cfg:
            try:
                kind = str(self._auth_cfg.get("type") or "bearer").strip().lower()
                if kind in ("client_credentials", "oauth_client_credentials", "oauth"):
                    self._auth_provider = OAuthTokenProvider.from_config(
                        self._auth_cfg, resource_url=self.url, http_client=self._client
                    )
                    self._owns_auth_provider = True
                else:
                    self.headers.update(
                        build_auth_headers(self._auth_cfg, resource_url=self.url, http_client=self._client)
                    )
                    return
            except McpAuthError:
                raise
        if self._auth_provider is not None:
            token = self._auth_provider.get_access_token(force_refresh=force_refresh)
            self.headers["Authorization"] = f"Bearer {token}"

    def _maybe_refresh_auth_from_error(self, exc: Exception) -> bool:
        text = str(exc)
        if "401" not in text:
            return False
        if self._auth_provider is None and not self._auth_cfg:
            return False
        try:
            self._ensure_auth_headers(force_refresh=True)
            return True
        except Exception:
            return False

    def _base_headers(self) -> Dict[str, str]:
        headers = {
            "Content-Type": "application/json",
            "Accept": "application/json, text/event-stream",
            **self.headers,
        }
        if self._initialized:
            headers["MCP-Protocol-Version"] = self.protocol_version
        if self.session_id:
            headers["Mcp-Session-Id"] = self.session_id
        return headers

    def _capture_session(self, resp: Any) -> None:
        sid = resp.headers.get("Mcp-Session-Id") or resp.headers.get("mcp-session-id")
        if sid:
            self.session_id = sid

    def _raise_http_error(self, resp: Any) -> None:
        status = getattr(resp, "status_code", 0)
        body = getattr(resp, "text", "") or ""
        if status == 401:
            www = resp.headers.get("WWW-Authenticate") or resp.headers.get("www-authenticate") or ""
            raise RuntimeError(format_401_hint(status, www, self.url))
        raise RuntimeError(f"MCP HTTP 错误 ({status}): {body[:200]}")

    def _should_retry_http(self, status: int, payload: Dict[str, Any], attempt: int, resp: Any) -> bool:
        if attempt + 1 >= _MAX_HTTP_ATTEMPTS or status not in _RETRYABLE_HTTP_STATUS:
            return False
        method = str(payload.get("method") or "")
        if method == "tools/call" and status in _TOOLS_CALL_NO_RETRY:
            return False
        if method == "tools/call" and status == 429:
            # An empty body is the only 429 treated as "try again, nothing ran".
            # Anything else may already be the tool's result.
            return _http_body_is_empty(resp)
        return True

    def _exchange(self, payload: Dict[str, Any]) -> Any:
        """POST once, retrying transient HTTP statuses with backoff."""
        resp: Any = None
        for attempt in range(_MAX_HTTP_ATTEMPTS):
            resp = self._client.post(
                self._post_endpoint,
                json=payload,
                headers=self._base_headers(),
                timeout=self.timeout_seconds,
            )
            status = getattr(resp, "status_code", 200)
            if not self._should_retry_http(status, payload, attempt, resp):
                return resp
            self._sleeper(_retry_wait_seconds(attempt, resp))
        return resp

    def _try_legacy_sse_endpoint(self) -> None:
        """Fall back to 2024-11-05 HTTP+SSE. Read only until the endpoint event."""
        headers = {"Accept": "text/event-stream", **{k: v for k, v in self.headers.items() if k.lower() != "content-type"}}
        stream = getattr(self._client, "stream", None)
        if callable(stream):
            with self._client.stream("GET", self.url, headers=headers, timeout=self.timeout_seconds) as resp:
                if getattr(resp, "status_code", 200) >= 400:
                    self._raise_http_error(resp)
                endpoint = _first_sse_endpoint(resp)
        else:
            resp = self._client.get(self.url, headers=headers, timeout=self.timeout_seconds)
            if getattr(resp, "status_code", 200) >= 400:
                self._raise_http_error(resp)
            endpoint = _first_sse_endpoint(resp)
        if not endpoint:
            raise RuntimeError("MCP 旧版 HTTP+SSE 未返回 endpoint 事件")
        self._post_endpoint = _resolve_sse_endpoint(self.url, endpoint)
        self._legacy_sse = True

    def _post(self, payload: Dict[str, Any], *, expect_response: bool, allow_legacy_fallback: bool = False) -> Any:
        with self._rpc_lock:
            return self._post_unlocked(payload, expect_response=expect_response, allow_legacy_fallback=allow_legacy_fallback)

    def _post_unlocked(self, payload: Dict[str, Any], *, expect_response: bool, allow_legacy_fallback: bool = False) -> Any:
        resp = self._exchange(payload)
        status = getattr(resp, "status_code", 200)
        method = str(payload.get("method") or "")
        if status == 429 and method == "tools/call":
            reply = _jsonrpc_reply(resp)
            if reply is not None:
                self._capture_session(resp)
                return reply
        # Session expired → open a new session. Replay only read-only methods.
        # tools/call is not sent again: the gateway may already have run it.
        if status == 404 and self.session_id and payload.get("method") != "initialize":
            method = str(payload.get("method") or "")
            self.session_id = None
            self._initialized = False
            self.connect()
            if method not in _SESSION_REPLAY_METHODS:
                raise RuntimeError("MCP 会话已失效 (404)，该请求可能已被网关执行，未再次发送。")
            resp = self._exchange(payload)
            status = getattr(resp, "status_code", 200)
        if status in (404, 405) and allow_legacy_fallback and payload.get("method") == "initialize" and not self._legacy_sse:
            self._try_legacy_sse_endpoint()
            resp = self._exchange(payload)
            status = getattr(resp, "status_code", 200)
        if status == 401 and self._auth_provider is not None:
            self._ensure_auth_headers(force_refresh=True)
            resp = self._exchange(payload)
            status = getattr(resp, "status_code", 200)
        if status >= 400:
            self._raise_http_error(resp)
        self._capture_session(resp)
        text = (getattr(resp, "text", "") or "").strip()
        if not expect_response or not text or status == 202:
            return None
        ctype = (resp.headers.get("Content-Type") or resp.headers.get("content-type") or "").lower()
        return _parse_sse_payload(text, payload.get("id")) if "text/event-stream" in ctype else json.loads(text)

    def _request(
        self,
        method: str,
        params: Dict[str, Any],
        *,
        allow_legacy_fallback: bool = False,
    ) -> Any:
        self._next_id += 1
        return _unwrap_rpc_result(
            self._post(
                {"jsonrpc": "2.0", "id": self._next_id, "method": method, "params": params},
                expect_response=True,
                allow_legacy_fallback=allow_legacy_fallback,
            )
        )

    def close(self) -> None:
        with self._rpc_lock:
            self._close_unlocked()

    def _close_unlocked(self) -> None:
        if self.session_id and not self._legacy_sse:
            try:
                headers = self._base_headers()
                self._client.delete(self.url, headers=headers, timeout=min(5, self.timeout_seconds))
            except Exception:
                pass
        self.session_id = None
        self._initialized = False
        if self._owns_auth_provider and self._auth_provider is not None:
            try:
                self._auth_provider.close()
            except Exception:
                pass
        if self._owns_client and hasattr(self._client, "close"):
            self._client.close()
