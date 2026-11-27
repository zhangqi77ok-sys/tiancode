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


def test_workspace_npm_install_does_not_start(tmp_path: Path) -> None:
    work = tmp_path / "proj"
    work.mkdir()
    global_cfg = tmp_path / "mcp.json"
    _write(global_cfg, {"global_install": {"command": "npm", "args": ["install", "left-pad"]}})
    _write(
        work / ".tiancode" / "mcp.json",
        {
            "install": {"command": "npm", "args": ["install", "@example/mcp-server"]},
            "ci": {"command": "npm", "args": ["ci"]},
            "add": {"command": "pnpm", "args": ["add", "left-pad"]},
        },
    )

    configs, errors = load_mcp_configs(work, global_config_path=global_cfg)

    assert "global_install" in configs
    for name in ("install", "ci", "add"):
        assert name not in configs
    assert "node_modules" in "\n".join(errors)


def test_workspace_npx_prefix_stays_outside_the_repo(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    work = tmp_path / "proj"
    work.mkdir()
    global_cfg = tmp_path / "mcp.json"
    _write(global_cfg, {"mine": {"command": "npx", "args": ["-y", "@example/mcp-server"]}})
    _write(
        work / ".tiancode" / "mcp.json",
        {"remote": {"command": "npx", "args": ["--prefix=.", "-y", "@example/mcp-server"]}},
    )
    calls: list[tuple[list[str], object]] = []

    def explode(argv: list[str], **kwargs: object) -> None:
        calls.append((list(argv), kwargs.get("cwd")))
        raise OSError("stopped")

    monkeypatch.setattr("tiancode.core.mcp_transports.shutil.which", lambda _cmd: "/usr/bin/npx")
    monkeypatch.setattr("tiancode.core.mcp_transports.subprocess.Popen", explode)
    statuses = McpManager(work, global_config_path=global_cfg).reload()

    by_name = {item.name: item for item in statuses}
    assert by_name["remote"].connected is False
    assert "stopped" in by_name["remote"].error
    workspace_call = next(call for call in calls if "--prefix" in call[0] and str(work) not in call[0])
    argv, cwd = workspace_call
    prefix = Path(argv[argv.index("--prefix") + 1])
    assert prefix.is_absolute()
    assert work.resolve() not in prefix.resolve().parents
    assert Path(cwd) == work.resolve()
    global_call = next(call for call in calls if call[0][1:] == ["-y", "@example/mcp-server"])
    assert "--prefix" not in global_call[0]
    McpManager(work, global_config_path=global_cfg).close_all()


def test_isolated_npx_does_not_run_a_workspace_bin(tmp_path: Path) -> None:
    import shutil
    import subprocess

    npx = shutil.which("npx")
    if npx is None:
        pytest.skip("npx is not installed")
    from tiancode.core.mcp_transports import isolate_workspace_npm

    work = tmp_path / "proj"
    bindir = work / "node_modules" / ".bin"
    bindir.mkdir(parents=True)
    marker = work / "ran"
    launcher = bindir / "hello-local"
    launcher.write_text(f"#!/bin/sh\ntouch {marker}\n", encoding="utf-8")
    launcher.chmod(0o755)
    (work / ".npmrc").write_text("script-shell=/tmp/tiancode-evil-shell\n", encoding="utf-8")
    isolated = isolate_workspace_npm("npx", ["--offline", "--yes", "hello-local"])
    args, directory = isolated.args, isolated.directory
    assert directory is not None
    try:
        result = subprocess.run(
            [npx, *args],
            cwd=work,
            check=False,
            timeout=30,
            capture_output=True,
        )
    finally:
        shutil.rmtree(directory, ignore_errors=True)

    assert not marker.exists()
    assert b"tiancode-evil-shell" not in result.stderr


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


def test_workspace_refuses_launchers_that_run_repo_bins(tmp_path: Path) -> None:
    work = tmp_path / "proj"
    work.mkdir()
    for command, args in (
        ("bunx", ["hello"]),
        ("bun", ["x", "hello"]),
        ("bun", ["--cwd", "/tmp", "bunx", "hello"]),
        ("pnpm", ["--dir", "/tmp", "exec", "hello"]),
        ("yarn", ["exec", "hello"]),
    ):
        reason = _workspace_command_error(command, args, work, work)
        assert reason is not None and "可执行文件" in reason
    assert _workspace_command_error("pnpm", ["dlx", "cowsay"], work, work) is None
    assert _workspace_command_error("yarn", ["dlx", "cowsay"], work, work) is None
    assert _workspace_command_error("uvx", ["ruff"], work, work) is None
    assert _workspace_command_error("npx", ["-y", "cowsay"], work, work) is None
    assert _workspace_command_error("corepack", ["pnpm", "exec", "hello"], work, work) is not None
    assert _workspace_command_error("corepack", ["yarn", "exec", "hello"], work, work) is not None
    assert _workspace_command_error("corepack", ["pnpm", "install"], work, work) is not None
    assert _workspace_command_error("corepack", ["pnpm", "dlx", "cowsay"], work, work) is None
    assert _workspace_command_error("corepack", ["pnpm@9.15.0", "exec", "hello"], work, work) is not None
    assert _workspace_command_error("corepack", ["yarnpkg", "exec", "hello"], work, work) is not None
    assert _workspace_command_error("corepack", ["pnpx", "hello"], work, work) is None
    assert _workspace_command_error("corepack", ["use", "yarn"], work, work) is not None
    assert _workspace_command_error("corepack", ["up"], work, work) is not None
    flagged = _workspace_command_error("corepack", ["--install-directory", "pnpm", "use", "yarn"], work, work)
    assert flagged is not None and "corepack use" in flagged
    assert _workspace_command_error("corepack", ["--install-directory", "pnpm", "dlx", "cowsay"], work, work) is None
    assert _workspace_command_error("pip", ["install", "cowsay"], work, work) is None
    assert _workspace_command_error("pip3", ["install", "evil.whl"], work, work) is not None
    assert _workspace_command_error("pip", ["install", "file:evil.tar.gz"], work, work) is not None
    assert _workspace_command_error("pipx", ["run", "cowsay"], work, work) is not None
    assert _workspace_command_error("cargo", ["install", "serde"], work, work) is not None
    assert _workspace_command_error("rustup", ["run", "stable", "cargo", "check"], work, work) is not None
    assert _workspace_command_error("go", ["run", "main.go"], work, work) is not None


def test_workspace_refuses_project_managers_and_shim_writes(tmp_path: Path) -> None:
    work = tmp_path / "proj"
    (work / "bin").mkdir(parents=True)
    for command, args in (
        ("poetry", ["run", "hello"]),
        ("bundle", ["exec", "hello"]),
        ("bundler", ["env"]),
        ("composer", ["run", "evil"]),
        ("composer.phar", ["install"]),
        ("gem", ["build", "evil.gemspec"]),
        ("uv", ["run", "hello"]),
        ("uv", ["sync"]),
        ("uv", ["add", "cowsay"]),
        ("uv", ["remove", "cowsay"]),
        ("uv", ["build"]),
        ("uv", ["--directory", str(work), "run", "hello"]),
        ("uv", ["pip", "sync", "req.txt"]),
        ("uv", ["pip", "compile", "req.in"]),
        ("uv", ["pip", "install", "-e", "evilpkg"]),
        ("uv", ["pip", "install", "--editable=evilpkg"]),
        ("uv", ["pip", "install", "evil.tar.gz"]),
        ("uv", ["pip", "install", "file:evil.tar.gz"]),
        ("pip", ["install", "-e", "evilpkg"]),
        ("pip3", ["install", "--editable", "evilpkg"]),
    ):
        reason = _workspace_command_error(command, args, work, work)
        assert reason is not None, (command, args)
    assert _workspace_command_error("uv", ["pip", "install", "cowsay"], work, work) is None
    assert _workspace_command_error("uv", ["tool", "run", "ruff"], work, work) is None
    assert _workspace_command_error("uvx", ["ruff"], work, work) is None
    assert _workspace_command_error("pip", ["install", "cowsay"], work, work) is None

    assert _workspace_command_error("corepack", ["enable", "--install-directory", "bin"], work, work) is not None
    assert _workspace_command_error("corepack", ["enable", "--install-directory=bin"], work, work) is not None
    assert _workspace_command_error("corepack", ["enable", "--install-directory=."], work, work) is not None
    assert _workspace_command_error(
        "corepack", ["enable", f"--install-directory={work}"], work, work
    ) is not None
    assert _workspace_command_error("corepack", ["enable", "--install-directory=bin/../bin"], work, work) is not None
    outside = tmp_path / "outside"
    assert _workspace_command_error(
        "corepack", ["enable", f"--install-directory={outside}"], work, work
    ) is None
    assert _workspace_command_error("corepack", ["enable"], work, work) is None
    assert _workspace_command_error("corepack", ["enable", "--help"], work, work) is None
    assert _workspace_command_error("corepack", ["enable", "--install-directory=bin", "-h"], work, work) is None
    assert _workspace_command_error("corepack", ["prepare", "yarn@1.22.22"], work, work) is None
    assert _workspace_command_error("corepack", ["-h"], work, work) is None


def test_workspace_package_launchers_are_pinned_outside_the_repo(tmp_path: Path) -> None:
    import shutil

    from tiancode.core.mcp_transports import _PUBLIC_NPM_REGISTRY, _apply_launcher_env, isolate_workspace_npm

    work = tmp_path / "proj"
    work.mkdir()
    directories = []
    try:
        pnpm = isolate_workspace_npm(
            "pnpm",
            ["--dir", str(work), "--config.registry=http://127.0.0.1:9", "dlx", "cowsay"],
        )
        directories.append(pnpm.directory)
        assert pnpm.directory is not None
        joined = " ".join(pnpm.args)
        assert str(work) not in joined
        assert "127.0.0.1" not in joined
        assert str(pnpm.directory) in pnpm.args
        assert "--config.registry=https://registry.npmjs.org" in pnpm.args
        env = _apply_launcher_env(
            {
                "npm_config_registry": "http://127.0.0.1:9",
                "PNPM_CONFIG_REGISTRY": "http://127.0.0.1:9",
                "PATH": "/usr/bin",
            },
            pnpm,
        )
        assert env["npm_config_registry"] == _PUBLIC_NPM_REGISTRY
        assert "PNPM_CONFIG_REGISTRY" not in env
        assert env["PATH"] == "/usr/bin"

        yarn = isolate_workspace_npm("yarn", ["dlx", "cowsay"])
        directories.append(yarn.directory)
        env = _apply_launcher_env(
            {
                "YARN_IGNORE_PATH": "0",
                "YARN_NPM_REGISTRY_SERVER": "http://127.0.0.1:9",
                "COREPACK_HOME": "/keep",
            },
            yarn,
        )
        assert env["YARN_IGNORE_PATH"] == "1"
        assert env["YARN_ENABLE_SCRIPTS"] == "false"
        assert env["YARN_NPM_REGISTRY_SERVER"] == "https://registry.yarnpkg.com"
        assert env["YARN_RC_FILENAME"].startswith("tiancode-yarnrc-")
        assert env["COREPACK_HOME"] == "/keep"
        assert work.resolve() not in Path(env["YARN_CACHE_FOLDER"]).resolve().parents

        uv = isolate_workspace_npm(
            "uvx",
            ["--index-url", "http://127.0.0.1:9/simple", "--from", "ruff", "ruff"],
        )
        directories.append(uv.directory)
        assert "--index-url" not in uv.args
        assert "http://127.0.0.1:9/simple" not in uv.args
        assert "--from" in uv.args
        assert "--no-config" in uv.args and "--isolated" in uv.args
        env = _apply_launcher_env({"UV_INDEX_URL": "http://127.0.0.1:9/simple", "UV_PYTHON": "3.12"}, uv)
        assert env["UV_INDEX_URL"] == "https://pypi.org/simple"
        assert env["UV_PYTHON"] == "3.12"

        tool = isolate_workspace_npm("uv", ["tool", "run", "--index-url=http://127.0.0.1:9/simple", "ruff"])
        directories.append(tool.directory)
        assert "127.0.0.1" not in " ".join(tool.args)
        assert tool.args[0] == "--no-config"
        assert "ruff" in tool.args

        via_corepack = isolate_workspace_npm("corepack", ["pnpm", "--dir", str(work), "dlx", "cowsay"])
        directories.append(via_corepack.directory)
        assert via_corepack.args[0] == "pnpm"
        assert str(work) not in via_corepack.args
        assert "--dir" in via_corepack.args

        versioned = isolate_workspace_npm("corepack", ["pnpm@9.15.0", "--dir", str(work), "dlx", "cowsay"])
        directories.append(versioned.directory)
        assert versioned.args[0] == "pnpm@9.15.0"
        assert str(work) not in versioned.args
        assert "--config.registry=https://registry.npmjs.org" in versioned.args

        via_pnpx = isolate_workspace_npm("corepack", ["pnpx", "cowsay"])
        directories.append(via_pnpx.directory)
        assert via_pnpx.args[0] == "pnpm"
        assert "dlx" in via_pnpx.args
        assert "--dir" in via_pnpx.args
        assert "cowsay" in via_pnpx.args

        not_a_forward = isolate_workspace_npm(
            "corepack",
            ["--install-directory", "pnpm", "dlx", "cowsay"],
        )
        assert not_a_forward.directory is None
        assert not_a_forward.args == ["--install-directory", "pnpm", "dlx", "cowsay"]

        pip = isolate_workspace_npm(
            "pip",
            ["install", "--extra-index-url", "http://127.0.0.1:9/extra", "-r", "requirements.txt", "cowsay"],
        )
        directories.append(pip.directory)
        joined = " ".join(pip.args)
        assert pip.args[0] == "--isolated"
        assert "--index-url" in pip.args
        assert "https://pypi.org/simple" in pip.args
        assert "127.0.0.1" not in joined
        assert "--extra-index-url" not in pip.args
        assert "-r" not in pip.args
        assert "requirements.txt" not in pip.args
        assert "cowsay" in pip.args
        env = _apply_launcher_env(
            {
                "PIP_INDEX_URL": "http://127.0.0.1:9/simple",
                "PIP_CONFIG_FILE": str(work / "pip.conf"),
                "PATH": "/usr/bin",
            },
            pip,
        )
        assert env["PIP_INDEX_URL"] == "https://pypi.org/simple"
        assert "PIP_CONFIG_FILE" not in env
        assert env["PATH"] == "/usr/bin"

        uvpip = isolate_workspace_npm(
            "uv",
            [
                "pip", "install",
                "--extra-index-url", "http://127.0.0.1:9/extra",
                "-r", "req.txt",
                "--group", "dev",
                "--find-links", "wheels",
                "-e", "evilpkg",
                "cowsay",
            ],
        )
        directories.append(uvpip.directory)
        joined = " ".join(uvpip.args)
        assert uvpip.args[0] == "--no-config"
        assert "--system" in uvpip.args
        assert "--no-sources" in uvpip.args
        assert "--default-index" in uvpip.args
        assert "https://pypi.org/simple" in uvpip.args
        assert "cowsay" in uvpip.args
        assert "--extra-index-url" not in uvpip.args
        assert "--group" not in uvpip.args
        assert "-e" not in uvpip.args
        assert "evilpkg" not in uvpip.args
        assert "req.txt" not in uvpip.args
        assert "wheels" not in uvpip.args
        assert "127.0.0.1" not in joined
        assert uvpip.directory is not None
        assert work.resolve() not in Path(next(
            uvpip.args[index + 1] for index, arg in enumerate(uvpip.args) if arg == "--cache-dir"
        )).resolve().parents
        env = _apply_launcher_env(
            {
                "UV_INDEX_URL": "http://127.0.0.1:9/simple",
                "UV_PYTHON": str(work / ".venv" / "bin" / "python"),
                "UV_PROJECT_ENVIRONMENT": str(work / ".venv"),
                "VIRTUAL_ENV": str(work / ".venv"),
                "PATH": "/usr/bin",
            },
            uvpip,
        )
        assert env["UV_INDEX_URL"] == "https://pypi.org/simple"
        assert env["UV_SYSTEM_PYTHON"] == "1"
        assert "UV_PYTHON" not in env
        assert "UV_PROJECT_ENVIRONMENT" not in env
        assert "VIRTUAL_ENV" not in env
        assert env["PATH"] == "/usr/bin"

        uvlist = isolate_workspace_npm("uv", ["pip", "list"])
        directories.append(uvlist.directory)
        assert "--system" in uvlist.args
        assert "--no-sources" not in uvlist.args
        assert "--index-url" not in uvlist.args
    finally:
        for directory in directories:
            if directory is not None:
                shutil.rmtree(directory, ignore_errors=True)


def test_pnpm_dlx_does_not_run_a_workspace_bin(tmp_path: Path) -> None:
    import shutil
    import subprocess

    pnpm = shutil.which("pnpm")
    if pnpm is None:
        pytest.skip("pnpm is not installed")
    from tiancode.core.mcp_transports import _apply_launcher_env, isolate_workspace_npm

    work = tmp_path / "proj"
    bindir = work / "node_modules" / ".bin"
    bindir.mkdir(parents=True)
    marker = work / "ran"
    launcher = bindir / "hello-local"
    launcher.write_text(f"#!/bin/sh\ntouch {marker}\n", encoding="utf-8")
    launcher.chmod(0o755)
    (work / ".npmrc").write_text(
        "registry=http://127.0.0.1:9\n@evil:registry=http://127.0.0.1:9\n",
        encoding="utf-8",
    )
    isolated = isolate_workspace_npm("pnpm", ["dlx", "hello-local"])
    assert isolated.directory is not None
    env = _apply_launcher_env(dict(os.environ), isolated)
    try:
        subprocess.run(
            [pnpm, *isolated.args],
            cwd=work,
            env=env,
            check=False,
            timeout=25,
            capture_output=True,
        )
    finally:
        shutil.rmtree(isolated.directory, ignore_errors=True)

    assert not marker.exists()


def test_yarn_dlx_does_not_run_a_repo_yarn_path(tmp_path: Path) -> None:
    import shutil
    import subprocess

    yarn = shutil.which("yarn")
    if yarn is None:
        pytest.skip("yarn is not installed")
    from tiancode.core.mcp_transports import _apply_launcher_env, isolate_workspace_npm

    work = tmp_path / "proj"
    work.mkdir()
    marker = work / "ran"
    evil = work / "evil.cjs"
    evil.write_text(
        f"require('fs').writeFileSync({str(marker)!r}, 'x')\nprocess.exit(42)\n",
        encoding="utf-8",
    )
    (work / ".yarnrc.yml").write_text(f"yarnPath: {json.dumps(str(evil))}\n", encoding="utf-8")
    isolated = isolate_workspace_npm("yarn", ["dlx", "--help"])
    assert isolated.directory is not None
    env = _apply_launcher_env(dict(os.environ), isolated)
    try:
        result = subprocess.run(
            [yarn, *isolated.args],
            cwd=work,
            env=env,
            check=False,
            timeout=25,
            capture_output=True,
        )
    finally:
        shutil.rmtree(isolated.directory, ignore_errors=True)

    assert not marker.exists()
    # Yarn 1 treats `dlx` as a script name and never reads yarnPath. Berry
    # prints dlx help when the rc file and yarnPath are ignored.
    combined = result.stdout + result.stderr
    if b"yarn run v1." in combined and b"package.json" in combined:
        return
    assert result.returncode == 0
    assert b"temporary environment" in result.stdout


def test_pip_install_does_not_use_a_workspace_index(tmp_path: Path) -> None:
    import shutil
    import subprocess

    pip = shutil.which("pip3") or shutil.which("pip")
    if pip is None:
        pytest.skip("pip is not installed")
    from tiancode.core.mcp_transports import _apply_launcher_env, isolate_workspace_npm

    work = tmp_path / "proj"
    work.mkdir()
    config = work / "pip.conf"
    config.write_text(
        "[global]\nindex-url = http://127.0.0.1:9/simple\nextra-index-url = http://127.0.0.1:9/extra\n",
        encoding="utf-8",
    )
    isolated = isolate_workspace_npm(
        "pip3",
        ["install", "--extra-index-url", "http://127.0.0.1:9/extra", "--dry-run", "--no-deps", "pip"],
    )
    assert isolated.directory is not None
    env = _apply_launcher_env(
        {**os.environ, "PIP_CONFIG_FILE": str(config), "PIP_INDEX_URL": "http://127.0.0.1:9/simple"},
        isolated,
    )
    try:
        result = subprocess.run(
            [pip, *isolated.args],
            cwd=work,
            env=env,
            check=False,
            timeout=30,
            capture_output=True,
        )
    finally:
        shutil.rmtree(isolated.directory, ignore_errors=True)

    combined = result.stdout + result.stderr
    assert b"127.0.0.1" not in combined
    assert result.returncode == 0


def test_uv_pip_does_not_use_the_workspace(tmp_path: Path) -> None:
    import shutil
    import subprocess

    uv = shutil.which("uv")
    if uv is None:
        pytest.skip("uv is not installed")
    from tiancode.core.mcp_transports import _apply_launcher_env, isolate_workspace_npm

    work = tmp_path / "proj"
    work.mkdir()
    marker = work / "ran"
    venv_python = work / ".venv" / "bin" / "python"
    venv_python.parent.mkdir(parents=True)
    (work / ".venv" / "pyvenv.cfg").write_text(
        "home = /usr/bin\ninclude-system-site-packages = false\nversion = 3.12.3\n",
        encoding="utf-8",
    )
    venv_python.write_text(f"#!/bin/sh\necho hijacked > {marker}\nexit 1\n", encoding="utf-8")
    venv_python.chmod(0o755)
    (work / "uv.toml").write_text('index-url = "http://127.0.0.1:9/simple"\n', encoding="utf-8")
    isolated = isolate_workspace_npm(
        "uv",
        ["pip", "install", "--dry-run", "--no-deps", "pip"],
    )
    assert isolated.directory is not None
    env = _apply_launcher_env(
        {**os.environ, "UV_INDEX_URL": "http://127.0.0.1:9/simple", "UV_PYTHON": str(venv_python)},
        isolated,
    )
    try:
        result = subprocess.run(
            [uv, *isolated.args],
            cwd=work,
            env=env,
            check=False,
            timeout=30,
            capture_output=True,
        )
    finally:
        shutil.rmtree(isolated.directory, ignore_errors=True)

    combined = result.stdout + result.stderr
    assert b"127.0.0.1" not in combined
    assert not marker.exists()
    assert result.returncode == 0


def test_corepack_enable_writes_shims_the_guard_refuses(tmp_path: Path) -> None:
    import shutil
    import subprocess

    corepack = shutil.which("corepack")
    if corepack is None:
        pytest.skip("corepack is not installed")
    work = tmp_path / "proj"
    work.mkdir()
    assert _workspace_command_error("corepack", ["enable", "--install-directory=bin"], work, work) is not None

    help_dir = tmp_path / "help"
    help_dir.mkdir()
    helped = subprocess.run(
        [corepack, "enable", "--install-directory=bin", "-h"],
        cwd=help_dir,
        check=False,
        timeout=20,
        capture_output=True,
    )
    assert helped.returncode == 0
    assert not (help_dir / "bin").exists()

    shims = tmp_path / "shims"
    shims.mkdir()
    wrote = subprocess.run(
        [corepack, "enable", "--install-directory", str(shims)],
        cwd=work,
        check=False,
        timeout=20,
        capture_output=True,
    )
    assert wrote.returncode == 0
    assert (shims / "pnpm").is_symlink() or (shims / "pnpm").exists()
    assert not (work / "pnpm").exists()

