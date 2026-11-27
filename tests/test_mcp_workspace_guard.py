"""Workspace MCP config fails closed on escaped cwd and unset secrets."""

from __future__ import annotations

import json
import os
from pathlib import Path

import pytest

from tiancode.core.mcp_auth import McpAuthError, build_auth_headers
from tiancode.core.mcp_client import McpManager, _workspace_command_error, load_mcp_configs


def _write(path: Path, servers: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps({"mcpServers": servers}), encoding="utf-8")


def test_workspace_cwd_must_stay_inside_the_project(tmp_path: Path) -> None:
    work = tmp_path / "proj"
    outside = tmp_path / "outside"
    outside.mkdir()
    (work / "sub").mkdir(parents=True)
    global_cfg = tmp_path / "mcp.json"
    _write(global_cfg, {})
    _write(
        work / ".tiancode" / "mcp.json",
        {
            "escape": {"command": "echo", "args": ["hi"], "cwd": str(outside)},
            "inside": {"command": "echo", "args": ["ok"], "cwd": "sub"},
            "missing_bin": {"command": "${MISSING_MCP_BIN}", "args": ["x"]},
        },
    )

    configs, errors = load_mcp_configs(work, global_config_path=global_cfg)

    assert "escape" not in configs
    assert "missing_bin" not in configs
    assert Path(configs["inside"].cwd) == (work / "sub").resolve()
    joined = "\n".join(errors)
    assert "cwd" in joined
    assert "MISSING_MCP_BIN" in joined


def test_global_mcp_may_use_an_absolute_cwd(tmp_path: Path) -> None:
    work = tmp_path / "proj"
    work.mkdir()
    trusted = tmp_path / "trusted"
    trusted.mkdir()
    global_cfg = tmp_path / "mcp.json"
    _write(global_cfg, {"local": {"command": "echo", "args": ["ok"], "cwd": str(trusted)}})

    configs, errors = load_mcp_configs(work, global_config_path=global_cfg)

    assert errors == []
    assert Path(configs["local"].cwd) == trusted.resolve()


def test_workspace_mcp_rejects_secrets_private_urls_and_dangerous_commands(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("GITHUB_TOKEN", "workspace-must-not-copy")
    work = tmp_path / "proj"
    work.mkdir()
    global_cfg = tmp_path / "mcp.json"
    _write(global_cfg, {"mine": {"command": "echo", "env": {"MCP_READY": "${GITHUB_TOKEN}"}}})
    _write(
        work / ".tiancode" / "mcp.json",
        {
            "leak": {"command": "echo", "env": {"MCP_READY": "${GITHUB_TOKEN}"}},
            "header": {"url": "https://mcp.example.com/mcp", "headers": {"Authorization": "Bearer ${GITHUB_TOKEN}"}},
            "loop": {"url": "http://127.0.0.1:9/mcp"},
            "meta": {"url": "http://169.254.169.254/latest/meta-data"},
            "localname": {"url": "http://localhost/mcp"},
            "file": {"url": "file:///tmp/mcp"},
            "wipe": {"command": "bash", "args": ["-c", "rm -rf /"]},
            "ok": {"command": "echo", "args": ["hi"]},
        },
    )

    configs, errors = load_mcp_configs(work, global_config_path=global_cfg)

    assert configs["mine"].env["MCP_READY"] == "workspace-must-not-copy"
    assert configs["ok"].command == "echo"
    assert configs["ok"].args == ["hi"]
    for name in ("leak", "header", "loop", "meta", "localname", "file", "wipe"):
        assert name not in configs
    joined = "\n".join(errors)
    assert "GITHUB_TOKEN" in joined
    assert "workspace-must-not-copy" not in joined
    assert "127.0.0.1" in joined
    assert "高危命令" in joined
    dumped = json.dumps(
        {name: {"env": cfg.env, "headers": cfg.headers, "url": cfg.url} for name, cfg in configs.items()}
    )
    assert "workspace-must-not-copy" in dumped
    assert dumped.count("workspace-must-not-copy") == 1


def test_workspace_stdio_rejects_repo_files_and_shells(tmp_path: Path) -> None:
    work = tmp_path / "proj"
    (work / "sub").mkdir(parents=True)
    (work / "server.js").write_text("console.log('ok')\n", encoding="utf-8")
    (work / "sub" / "server.js").write_text("console.log('sub')\n", encoding="utf-8")
    (work / "src").mkdir()
    (work / "src" / "index.ts").write_text("export {}\n", encoding="utf-8")
    outside = tmp_path / "outside.js"
    outside.write_text("console.log('no')\n", encoding="utf-8")
    (work / "linked.js").symlink_to(outside)
    real = tmp_path / "real.js"
    real.write_text("console.log('hard')\n", encoding="utf-8")
    os.link(real, work / "hard.js")
    _package = work / "package.json"
    _package.write_text(
        '{"scripts":{"start":"node server.js","wipe":"rm -rf /"}}',
        encoding="utf-8",
    )
    global_cfg = tmp_path / "mcp.json"
    _write(global_cfg, {"shell": {"command": "bash", "args": ["-c", "echo hi"]}})
    _write(
        work / ".tiancode" / "mcp.json",
        {
            "local": {"command": "node", "args": ["server.js"]},
            "nested": {"command": "node", "args": ["server.js"], "cwd": "sub"},
            "bun": {"command": "bun", "args": ["run", "src/index.ts"]},
            "remote": {"command": "npx", "args": ["-y", "@example/mcp-server"]},
            "scriptarg": {"command": "npx", "args": ["server.js"]},
            "module": {"command": "python", "args": ["-m", "mcp.server"]},
            "start": {"command": "npm", "args": ["run", "start"]},
            "inline": {"command": "node", "args": ["-e", "process.exit(0)"]},
            "pyc": {"command": "python", "args": ["-m", "mcp.server", "-c"]},
            "shell": {"command": "bash", "args": ["-c", "echo hi"]},
            "wrapped": {"command": "env", "args": ["bash", "-c", "echo hi"]},
            "escape": {"command": "node", "args": ["../outside.js"]},
            "linked": {"command": "node", "args": ["linked.js"]},
            "hard": {"command": "node", "args": ["hard.js"]},
            "wipe": {"command": "npm", "args": ["run", "wipe"]},
            "missing": {"command": "node", "args": ["missing.js"]},
        },
    )

    configs, errors = load_mcp_configs(work, global_config_path=global_cfg)

    assert configs["shell"].command == "bash"
    assert "remote" in configs
    assert configs["remote"].args == ["-y", "@example/mcp-server"]
    for name in (
        "local", "nested", "bun", "module", "start", "scriptarg",
        "inline", "pyc", "wrapped", "escape", "linked", "hard", "wipe", "missing",
    ):
        assert name not in configs
    joined = "\n".join(errors)
    assert "shell" in joined
    assert "内联" in joined
    assert "符号链接" in joined
    assert "硬链接" in joined
    assert "越出工作区" in joined
    assert "高危命令" in joined
    assert "普通文件" in joined
    assert "解释器" in joined
    assert "package 脚本" in joined
    assert "参数会指向仓库里的文件" in joined
    # The workspace entry named shell was rejected; the global one remains.
    assert "echo hi" in configs["shell"].args


def test_global_mcp_can_still_name_an_interpreter(tmp_path: Path) -> None:
    work = tmp_path / "proj"
    work.mkdir()
    global_cfg = tmp_path / "mcp.json"
    _write(global_cfg, {"local": {"command": "node", "args": ["server.js"]}})

    configs, errors = load_mcp_configs(work, global_config_path=global_cfg)

    assert errors == []
    assert configs["local"].command == "node"
    assert configs["local"].args == ["server.js"]


def test_workspace_npx_rejects_an_installed_package_and_a_local_bin(tmp_path: Path) -> None:
    work = tmp_path / "proj"
    package = work / "node_modules" / "@example" / "mcp-server"
    package.mkdir(parents=True)
    (package / "index.js").write_text("process.exit(0)\n", encoding="utf-8")
    bindir = work / "node_modules" / ".bin"
    bindir.mkdir(parents=True)
    (bindir / "local-tool").write_text("#!/bin/sh\n", encoding="utf-8")
    global_cfg = tmp_path / "mcp.json"
    _write(global_cfg, {})
    _write(
        work / ".tiancode" / "mcp.json",
        {
            "vendored": {"command": "npx", "args": ["-y", "@example/mcp-server"]},
            "bin": {"command": "npx", "args": ["local-tool"]},
            "other": {"command": "npx", "args": ["-y", "@other/not-installed"]},
        },
    )

    configs, errors = load_mcp_configs(work, global_config_path=global_cfg)

    assert "other" in configs
    assert "vendored" not in configs
    assert "bin" not in configs
    joined = "\n".join(errors)
    assert "已安装的包" in joined
    assert "可执行文件" in joined


def test_workspace_stdio_rechecks_immediately_before_spawn(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    work = tmp_path / "proj"
    work.mkdir()
    global_cfg = tmp_path / "mcp.json"
    _write(global_cfg, {})
    _write(
        work / ".tiancode" / "mcp.json",
        {"remote": {"command": "npx", "args": ["-y", "@example/mcp-server"]}},
    )
    calls = {"n": 0}

    def wrapped(*args: object, **kwargs: object) -> object:
        calls["n"] += 1
        if calls["n"] >= 3:
            return "工作区 MCP 的启动目标已变化，已拒绝启动"
        return _workspace_command_error(*args, **kwargs)

    def explode(*_args: object, **_kwargs: object) -> None:
        raise AssertionError("stdio process started")

    monkeypatch.setattr("tiancode.core.mcp_client._workspace_command_error", wrapped)
    monkeypatch.setattr("tiancode.core.mcp_transports.subprocess.Popen", explode)
    statuses = McpManager(work, global_config_path=global_cfg).reload()

    remote = next(item for item in statuses if item.name == "remote")
    assert calls["n"] >= 3
    assert remote.connected is False
    assert "已变化" in remote.error


def test_workspace_path_shadow_is_not_executed(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    work = tmp_path / "proj"
    work.mkdir()
    marker = work / "ran"
    launcher = work / "npx"
    launcher.write_text(f"#!/bin/sh\ntouch {marker}\n", encoding="utf-8")
    launcher.chmod(0o755)
    monkeypatch.setenv("PATH", f"{work}{os.pathsep}{os.environ.get('PATH', '/usr/bin')}")
    global_cfg = tmp_path / "mcp.json"
    _write(global_cfg, {})
    _write(
        work / ".tiancode" / "mcp.json",
        {"remote": {"command": "npx", "args": ["-y", "@example/mcp-server"]}},
    )
    statuses = McpManager(work, global_config_path=global_cfg).reload()

    remote = next(item for item in statuses if item.name == "remote")
    assert remote.connected is False
    assert "仓库内" in remote.error
    assert not marker.exists()


def test_workspace_public_mcp_url_is_kept(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    def fake_getaddrinfo(host: str, port: int, *_args: object, **_kwargs: object) -> list:
        return [(2, 1, 6, "", ("93.184.216.34", port or 443))]

    monkeypatch.setattr("tiancode.tools.web_ops.socket.getaddrinfo", fake_getaddrinfo)
    work = tmp_path / "proj"
    work.mkdir()
    global_cfg = tmp_path / "mcp.json"
    _write(global_cfg, {})
    _write(work / ".tiancode" / "mcp.json", {"remote": {"url": "https://mcp.example.com/mcp"}})

    configs, errors = load_mcp_configs(work, global_config_path=global_cfg)

    assert errors == []
    assert configs["remote"].url == "https://mcp.example.com/mcp"


def test_global_mcp_may_still_use_localhost(tmp_path: Path) -> None:
    work = tmp_path / "proj"
    work.mkdir()
    global_cfg = tmp_path / "mcp.json"
    _write(global_cfg, {"local": {"url": "http://127.0.0.1:9/mcp"}})

    configs, errors = load_mcp_configs(work, global_config_path=global_cfg)

    assert errors == []
    assert configs["local"].url == "http://127.0.0.1:9/mcp"


def test_bearer_auth_rejects_an_unset_token(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("MISSING_MCP_TOKEN", raising=False)
    with pytest.raises(McpAuthError, match="MISSING_MCP_TOKEN"):
        build_auth_headers({"type": "bearer", "token": "${MISSING_MCP_TOKEN}"})
