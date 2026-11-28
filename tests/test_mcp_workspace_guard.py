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
    assert _workspace_command_error("java", ["Evil"], work, work) is not None
    assert _workspace_command_error("java", ["-version"], work, work) is not None
    assert _workspace_command_error("make", ["all"], work, work) is not None
    assert _workspace_command_error("env", ["make", "-C", "/tmp"], work, work) is not None


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
        ("uv", ["lock"]),
        ("uv", ["lock", "--no-build"]),
        ("uv", ["export"]),
        ("uv", ["tree"]),
        ("uv", ["--directory", str(work), "lock"]),
        ("uv", ["--directory", str(work), "run", "hello"]),
        ("uv", ["tool", "run", "--with-editable", "evilpkg", "ruff"]),
        ("uv", ["tool", "run", "--with-editable=evilpkg", "ruff"]),
        ("uv", ["tool", "uvx", "--with-editable", "evilpkg"]),
        ("uvx", ["--with-editable", "evilpkg", "ruff"]),
        ("uv", ["tool", "install", "-e", "evilpkg"]),
        ("uv", ["tool", "install", "--editable=evilpkg"]),
        ("uv", ["tool", "install", "--with-editable", "evilpkg", "pip"]),
        ("uv", ["tool", "install", "--with-requirements", "req.txt", "pip"]),
        ("uv", ["tool", "install", "--with-requirements=req.txt", "pip"]),
        ("pdm", ["run", "evil"]),
        ("pdm", ["info"]),
        ("hatch", ["run", "evil"]),
        ("hatchling", ["build"]),
        ("pipenv", ["run", "evil"]),
        ("pipenv", ["run", "--system", "evil"]),
        ("rye", ["run", "evil"]),
        ("make", ["all"]),
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
    assert _workspace_command_error("uv", ["tool", "run", "--with-requirements", "req.txt", "ruff"], work, work) is None
    assert _workspace_command_error("uv", ["tool", "install", "ruff"], work, work) is None
    assert _workspace_command_error("uv", ["version"], work, work) is None
    assert _workspace_command_error("uvx", ["ruff"], work, work) is None
    assert _workspace_command_error("pip", ["install", "cowsay"], work, work) is None
    # Flags after `--` belong to the launched program, not to uv.
    assert _workspace_command_error(
        "uv", ["tool", "run", "ruff", "--", "--with-editable", "evilpkg"], work, work
    ) is None

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

        editable = isolate_workspace_npm(
            "uv",
            ["tool", "run", "--with-editable", "evilpkg", "--with-requirements", "req.txt", "ruff"],
        )
        directories.append(editable.directory)
        joined = " ".join(editable.args)
        assert "--with-editable" not in editable.args
        assert "--with-requirements" not in editable.args
        assert "evilpkg" not in editable.args
        assert "req.txt" not in editable.args
        assert "ruff" in editable.args
        assert "127.0.0.1" not in joined

        kept = isolate_workspace_npm("uvx", ["--with", "requests", "ruff"])
        directories.append(kept.directory)
        assert "--with" in kept.args
        assert "requests" in kept.args
        assert "ruff" in kept.args

        attached = isolate_workspace_npm(
            "uvx",
            ["--with-editable=evilpkg", "--with-requirements=req.txt", "--from", "ruff", "ruff"],
        )
        directories.append(attached.directory)
        assert "--with-editable=evilpkg" not in attached.args
        assert not any(arg.startswith("--with-requirements") for arg in attached.args)
        assert "evilpkg" not in attached.args
        assert "req.txt" not in attached.args
        assert "--from" in attached.args
        assert "ruff" in attached.args

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


_DYNAMIC_BACKEND = """\
from pathlib import Path

def build_editable(wheel_directory, config_settings=None, metadata_directory=None):
    Path("RAN").write_text("editable")
    raise SystemExit("editable-ran")
"""

_DYNAMIC_PROJECT = """\
[project]
name = "evil"
dynamic = ["version"]
requires-python = ">=3.8"

[build-system]
requires = []
build-backend = "backend"
backend-path = ["."]
"""


def _uv_env() -> dict:
    import sys

    env = os.environ.copy()
    env["UV_PYTHON_DOWNLOADS"] = "never"
    env["UV_PYTHON"] = os.environ.get("UV_PYTHON") or sys.executable
    return env


def test_make_runs_the_makefile_the_guard_refuses(tmp_path: Path) -> None:
    import shutil
    import subprocess

    make = shutil.which("make")
    if make is None:
        pytest.skip("make is not installed")
    work = tmp_path / "proj"
    work.mkdir()
    (work / "Makefile").write_text("all:\n\ttouch RAN\n", encoding="utf-8")
    assert _workspace_command_error("make", ["all"], work, work) is not None
    subprocess.run([make, "all"], cwd=work, check=True, timeout=10)
    assert (work / "RAN").is_file()


def test_java_runs_a_workspace_class_the_guard_refuses(tmp_path: Path) -> None:
    import shutil
    import subprocess

    javac = shutil.which("javac")
    java = shutil.which("java")
    if javac is None or java is None:
        pytest.skip("javac or java is not installed")
    work = tmp_path / "proj"
    work.mkdir()
    (work / "Evil.java").write_text(
        "public class Evil { public static void main(String[] a) throws Exception {"
        " new java.io.File(\"RAN\").createNewFile(); } }\n",
        encoding="utf-8",
    )
    compiled = subprocess.run([javac, "Evil.java"], cwd=work, check=False, timeout=20, capture_output=True)
    if compiled.returncode != 0:
        pytest.skip("javac could not compile the probe")
    assert _workspace_command_error("java", ["Evil"], work, work) is not None
    subprocess.run([java, "Evil"], cwd=work, check=True, timeout=10)
    assert (work / "RAN").is_file()


def test_uv_dynamic_metadata_builds_even_with_no_build(tmp_path: Path) -> None:
    import shutil
    import subprocess

    uv = shutil.which("uv")
    if uv is None:
        pytest.skip("uv is not installed")
    env = _uv_env()
    for subcommand in ("lock", "export", "tree"):
        work = tmp_path / subcommand
        work.mkdir()
        (work / "backend.py").write_text(_DYNAMIC_BACKEND, encoding="utf-8")
        (work / "pyproject.toml").write_text(_DYNAMIC_PROJECT, encoding="utf-8")
        assert _workspace_command_error("uv", [subcommand, "--no-build"], work, work) is not None
        subprocess.run(
            [uv, subcommand, "--no-build", "--no-python-downloads"],
            cwd=work,
            env=env,
            check=False,
            timeout=30,
            capture_output=True,
        )
        assert (work / "RAN").read_text(encoding="utf-8") == "editable"


def test_uv_tool_editable_and_requirements_build_until_isolated(tmp_path: Path) -> None:
    import shutil
    import subprocess

    uv = shutil.which("uv")
    if uv is None:
        pytest.skip("uv is not installed")
    from tiancode.core.mcp_transports import _apply_launcher_env, isolate_workspace_npm

    env = _uv_env()

    def project() -> Path:
        work = tmp_path / "proj"
        if work.exists():
            shutil.rmtree(work)
        pkg = work / "evilpkg"
        pkg.mkdir(parents=True)
        (pkg / "backend.py").write_text(_DYNAMIC_BACKEND, encoding="utf-8")
        (pkg / "pyproject.toml").write_text(_DYNAMIC_PROJECT, encoding="utf-8")
        (work / "req.txt").write_text("-e evilpkg\n", encoding="utf-8")
        return work

    def ran(work: Path) -> bool:
        return (work / "evilpkg" / "RAN").is_file() or (work / "RAN").is_file()

    work = project()
    raw = [
        "tool", "run", "--offline", "--no-config", "--isolated",
        "--with-editable", "evilpkg", "--no-python-downloads", "ruff", "--version",
    ]
    assert _workspace_command_error("uv", raw, work, work) is not None
    subprocess.run([uv, *raw], cwd=work, env=env, check=False, timeout=30, capture_output=True)
    assert ran(work)

    work = project()
    requested = [
        "tool", "run", "--offline", "--with-editable", "evilpkg",
        "--with-requirements", "req.txt", "--no-python-downloads", "ruff", "--version",
    ]
    isolated = isolate_workspace_npm("uv", requested)
    assert isolated.directory is not None
    assert "--with-editable" not in isolated.args
    assert "--with-requirements" not in isolated.args
    assert "evilpkg" not in isolated.args
    try:
        subprocess.run(
            [uv, *isolated.args],
            cwd=work,
            env=_apply_launcher_env(env, isolated),
            check=False,
            timeout=30,
            capture_output=True,
        )
    finally:
        shutil.rmtree(isolated.directory, ignore_errors=True)
    assert not ran(work)


@pytest.mark.parametrize(
    ("name", "args", "filename", "text", "extra"),
    [
        (
            "pdm",
            ["run", "evil"],
            "pyproject.toml",
            "[project]\nname = \"x\"\nversion = \"0.1.0\"\nrequires-python = \">=3.8\"\n\n"
            "[tool.pdm.scripts]\nevil = \"touch RAN\"\n",
            {"PDM_CHECK_UPDATE": "0"},
        ),
        (
            "hatch",
            ["run", "evil"],
            "pyproject.toml",
            "[project]\nname = \"x\"\nversion = \"0.1.0\"\nrequires-python = \">=3.8\"\n\n"
            "[tool.hatch.envs.default]\nskip-install = true\n\n"
            "[tool.hatch.envs.default.scripts]\nevil = \"touch RAN\"\n",
            {},
        ),
        (
            "pipenv",
            ["run", "--system", "evil"],
            "Pipfile",
            "[[source]]\nurl = \"https://pypi.org/simple\"\nverify_ssl = true\nname = \"pypi\"\n\n"
            "[scripts]\nevil = \"touch RAN\"\n",
            {"PIPENV_YES": "1"},
        ),
        (
            "rye",
            ["run", "evil"],
            "pyproject.toml",
            "[project]\nname = \"x\"\nversion = \"0.1.0\"\nrequires-python = \">=3.8\"\n\n"
            "[tool.rye]\nmanaged = false\n\n[tool.rye.scripts]\nevil = \"touch RAN\"\n",
            {},
        ),
    ],
)
def test_project_manager_runs_a_repo_script(
    tmp_path: Path,
    name: str,
    args: list,
    filename: str,
    text: str,
    extra: dict,
) -> None:
    import shutil
    import subprocess

    binary = shutil.which(name)
    if binary is None:
        pytest.skip(f"{name} is not installed")
    work = tmp_path / "proj"
    work.mkdir()
    (work / filename).write_text(text, encoding="utf-8")
    assert _workspace_command_error(name, args, work, work) is not None
    env = os.environ.copy()
    env.update(extra)
    subprocess.run(
        [binary, *args],
        cwd=work,
        env=env,
        check=False,
        timeout=60,
        capture_output=True,
    )
    assert (work / "RAN").is_file()


def test_hatchling_build_runs_the_project_hook(tmp_path: Path) -> None:
    import shutil
    import subprocess

    hatchling = shutil.which("hatchling")
    if hatchling is None:
        pytest.skip("hatchling is not installed")
    work = tmp_path / "proj"
    work.mkdir()
    (work / "pyproject.toml").write_text(
        "[project]\nname = \"x\"\nversion = \"0.1.0\"\n"
        "[build-system]\nrequires = [\"hatchling\"]\nbuild-backend = \"hatchling.build\"\n"
        "[tool.hatch.build.hooks.custom]\n",
        encoding="utf-8",
    )
    (work / "hatch_build.py").write_text(
        "from pathlib import Path\n"
        "from hatchling.builders.hooks.plugin.interface import BuildHookInterface\n"
        "class CustomBuildHook(BuildHookInterface):\n"
        "    def initialize(self, version, build_data):\n"
        "        Path(self.root, \"RAN\").write_text(\"hook\")\n",
        encoding="utf-8",
    )
    assert _workspace_command_error("hatchling", ["build"], work, work) is not None
    subprocess.run([hatchling, "build"], cwd=work, check=False, timeout=30, capture_output=True)
    assert (work / "RAN").read_text(encoding="utf-8") == "hook"


_PROJECT_EXECUTORS = (
    "gmake", "bmake", "just", "tox", "nox", "ant", "gradle", "mvn", "mvnDebug",
    "cmake", "ctest", "cpack", "kotlinc", "kotlinc-jvm", "kotlin", "scalac", "scala",
    "groovyc", "groovy", "javac", "javadoc", "jshell", "jdb",
    "ansible", "ansible-playbook", "ansible-inventory", "ansible-console", "git",
)


def test_workspace_refuses_project_executors_without_blocking_the_shell(tmp_path: Path) -> None:
    """Workspace MCP refuses the whole launcher. The shell still checks recipes."""
    from tiancode.core.recipe_guard import recipe_block_reason

    work = tmp_path / "proj"
    work.mkdir()
    for program in _PROJECT_EXECUTORS:
        reason = _workspace_command_error(program, ["--version"], work, work)
        assert reason is not None and "已拒绝" in reason, program
    assert _workspace_command_error("env", ["gmake", "all"], work, work) is not None
    assert _workspace_command_error("/usr/bin/gradle", ["tasks"], work, work) is not None
    assert _workspace_command_error("ansible-config", ["dump"], work, work) is None
    assert _workspace_command_error("ansible-doc", ["ping"], work, work) is None
    assert _workspace_command_error("ansible-vault", ["view", "secret"], work, work) is None
    assert _workspace_command_error("echo", ["hi"], work, work) is None
    # `just --list` and `tox --version` do not run recipes. The shell allows them.
    assert recipe_block_reason("just --list", work) is None
    assert recipe_block_reason("tox --version", work) is None


def _executed(work: Path) -> bool:
    return (work / "RAN").is_file() or (work / "build" / "RAN").is_file()


@pytest.mark.parametrize(
    ("name", "args", "filename", "text"),
    [
        ("gmake", ["all"], "Makefile", "all:\n\ttouch RAN\n"),
        ("bmake", ["all"], "Makefile", "all:\n\ttouch RAN\n"),
        ("just", ["evil"], "justfile", "evil:\n    touch RAN\n"),
        (
            "ant",
            [],
            "build.xml",
            "<project default=\"x\"><target name=\"x\"><touch file=\"RAN\"/></target></project>\n",
        ),
        (
            "gradle",
            ["--no-daemon", "tasks"],
            "build.gradle",
            "new File(rootDir, \"RAN\").text = \"g\"\n",
        ),
        (
            "tox",
            ["run", "-e", "evil"],
            "tox.ini",
            "[tox]\nenvlist = evil\nskipsdist = true\nno_package = true\n\n"
            "[testenv:evil]\nskip_install = true\nallowlist_externals = touch\n"
            "commands = touch {toxinidir}/RAN\n",
        ),
        (
            "cmake",
            ["-S", ".", "-B", "build"],
            "CMakeLists.txt",
            "cmake_minimum_required(VERSION 3.5)\nproject(evil NONE)\n"
            "execute_process(COMMAND ${CMAKE_COMMAND} -E touch ${CMAKE_SOURCE_DIR}/RAN)\n",
        ),
        (
            "ctest",
            ["-N"],
            "CTestTestfile.cmake",
            "execute_process(COMMAND python3 -c \"open('RAN','w').write('x')\")\n"
            "add_test(evil /bin/true)\n",
        ),
        (
            "cpack",
            [],
            "CPackConfig.cmake",
            "execute_process(COMMAND python3 -c \"open('RAN','w').write('x')\")\n",
        ),
        ("groovy", ["evil.groovy"], "evil.groovy", "new File(\"RAN\").text = \"g\"\n"),
        ("kotlinc", ["-script", "evil.kts"], "evil.kts", "java.io.File(\"RAN\").createNewFile()\n"),
        ("kotlinc-jvm", ["-script", "evil.kts"], "evil.kts", "java.io.File(\"RAN\").createNewFile()\n"),
    ],
)
def test_launcher_runs_a_workspace_file_the_guard_refuses(
    tmp_path: Path, name: str, args: list, filename: str, text: str
) -> None:
    import shutil
    import subprocess

    binary = shutil.which(name)
    if binary is None:
        pytest.skip(f"{name} is not installed")
    work = tmp_path / "proj"
    work.mkdir()
    (work / filename).write_text(text, encoding="utf-8")
    assert _workspace_command_error(name, args, work, work) is not None
    env = os.environ.copy()
    if name == "gradle":
        env["GRADLE_USER_HOME"] = str(work / "gradle-home")
    result = subprocess.run(
        [binary, *args],
        cwd=work,
        env=env,
        check=False,
        timeout=90,
        capture_output=True,
    )
    if not _executed(work):
        err = (result.stderr or b"").decode("utf-8", "replace")[-800:]
        raise AssertionError(f"{name} did not execute the workspace file\n{err}")


def test_nox_executes_the_noxfile_the_guard_refuses(tmp_path: Path) -> None:
    import importlib.util
    import shutil
    import subprocess
    import sys

    nox = shutil.which("nox")
    if nox is not None:
        argv = [nox, "-l"]
    elif importlib.util.find_spec("nox") is not None:
        argv = [sys.executable, "-m", "nox", "-l"]
    else:
        pytest.skip("nox is not installed")
    work = tmp_path / "proj"
    work.mkdir()
    (work / "noxfile.py").write_text(
        "from pathlib import Path\nPath('RAN').write_text('top')\nimport nox\n\n"
        "@nox.session(python=False)\n"
        "def evil(session):\n    session.run('true', external=True)\n",
        encoding="utf-8",
    )
    assert _workspace_command_error("nox", ["-l"], work, work) is not None
    env = os.environ.copy()
    # nox treats both of these as color flags and exits before loading the file.
    env.pop("NO_COLOR", None)
    env.pop("FORCE_COLOR", None)
    result = subprocess.run(argv, cwd=work, env=env, check=False, timeout=30, capture_output=True)
    if not _executed(work):
        err = (result.stderr or b"").decode("utf-8", "replace")[-800:]
        raise AssertionError(f"nox did not execute noxfile.py\n{err}")


def test_mvn_runs_a_bound_plugin_the_guard_refuses(tmp_path: Path) -> None:
    import shutil
    import subprocess

    mvn = shutil.which("mvn")
    if mvn is None:
        pytest.skip("mvn is not installed")
    work = tmp_path / "proj"
    work.mkdir()
    (work / "pom.xml").write_text(
        "<project>\n"
        "  <modelVersion>4.0.0</modelVersion>\n"
        "  <groupId>e</groupId><artifactId>e</artifactId><version>1</version>\n"
        "  <build><plugins><plugin>\n"
        "    <groupId>org.apache.maven.plugins</groupId>\n"
        "    <artifactId>maven-antrun-plugin</artifactId><version>3.1.0</version>\n"
        "    <executions><execution><phase>validate</phase><goals><goal>run</goal></goals>\n"
        "      <configuration><target><touch file=\"RAN\"/></target></configuration>\n"
        "    </execution></executions>\n"
        "  </plugin></plugins></build>\n"
        "</project>\n",
        encoding="utf-8",
    )
    assert _workspace_command_error("mvn", ["-B", "validate"], work, work) is not None
    result = subprocess.run(
        [mvn, "-B", "validate"],
        cwd=work,
        check=False,
        timeout=90,
        capture_output=True,
    )
    if not _executed(work):
        text = ((result.stdout or b"") + (result.stderr or b"")).decode("utf-8", "replace")
        if result.returncode != 0:
            pytest.skip("mvn could not run the bound plugin in this environment")
        raise AssertionError(text[-800:])


def test_kotlin_and_scala_run_a_workspace_class_the_guard_refuses(tmp_path: Path) -> None:
    import shutil
    import subprocess

    kotlinc = shutil.which("kotlinc")
    kotlin = shutil.which("kotlin")
    scalac = shutil.which("scalac")
    scala = shutil.which("scala")
    if kotlinc and kotlin:
        work = tmp_path / "kotlin"
        work.mkdir()
        (work / "Evil.kt").write_text(
            "fun main() { java.io.File(\"RAN\").createNewFile() }\n",
            encoding="utf-8",
        )
        compiled = subprocess.run(
            [kotlinc, "Evil.kt", "-d", "."],
            cwd=work,
            check=False,
            timeout=60,
            capture_output=True,
        )
        if compiled.returncode != 0:
            kotlinc = None
        else:
            assert not _executed(work)
            assert _workspace_command_error("kotlin", ["EvilKt"], work, work) is not None
            subprocess.run([kotlin, "EvilKt"], cwd=work, check=True, timeout=30)
            assert _executed(work)
    if scalac and scala:
        work = tmp_path / "scala"
        work.mkdir()
        (work / "Evil.scala").write_text(
            "object Evil {\n"
            "  def main(args: Array[String]): Unit =\n"
            "    new java.io.File(\"RAN\").createNewFile()\n"
            "}\n",
            encoding="utf-8",
        )
        compiled = subprocess.run(
            [scalac, "Evil.scala"],
            cwd=work,
            check=False,
            timeout=60,
            capture_output=True,
        )
        if compiled.returncode != 0:
            scalac = None
        else:
            assert not _executed(work)
            assert _workspace_command_error("scala", ["Evil"], work, work) is not None
            subprocess.run([scala, "Evil"], cwd=work, check=True, timeout=30)
            assert _executed(work)
    if not ((kotlinc and kotlin) or (scalac and scala)):
        pytest.skip("kotlin and scala runners are not installed")


def test_scalac_macro_and_groovyc_config_run_during_compile(tmp_path: Path) -> None:
    import shutil
    import subprocess

    scalac = shutil.which("scalac")
    if scalac is not None:
        work = tmp_path / "scalac"
        work.mkdir()
        (work / "M.scala").write_text(
            "import scala.language.experimental.macros\n"
            "import scala.reflect.macros.blackbox.Context\n"
            "object M {\n"
            "  def t: Unit = macro ti\n"
            "  def ti(c: Context): c.Expr[Unit] = {\n"
            "    new java.io.File(\"RAN\").createNewFile()\n"
            "    import c.universe._\n"
            "    c.Expr[Unit](q\"()\")\n"
            "  }\n"
            "}\n",
            encoding="utf-8",
        )
        (work / "U.scala").write_text(
            "object U { def main(args: Array[String]): Unit = M.t }\n",
            encoding="utf-8",
        )
        defined = subprocess.run(
            [scalac, "M.scala"], cwd=work, check=False, timeout=60, capture_output=True
        )
        if defined.returncode != 0:
            scalac = None
        else:
            assert not _executed(work)
            assert _workspace_command_error("scalac", ["U.scala"], work, work) is not None
            subprocess.run([scalac, "-cp", ".", "U.scala"], cwd=work, check=True, timeout=60)
            assert _executed(work)

    groovyc = shutil.which("groovyc")
    if groovyc is not None:
        work = tmp_path / "groovyc"
        work.mkdir()
        (work / "config.groovy").write_text("new File(\"RAN\").text = \"cfg\"\n", encoding="utf-8")
        (work / "T.groovy").write_text("class T {}\n", encoding="utf-8")
        assert _workspace_command_error("groovyc", ["T.groovy"], work, work) is not None
        subprocess.run(
            [groovyc, "--configscript", "config.groovy", "T.groovy"],
            cwd=work,
            check=True,
            timeout=30,
        )
        assert _executed(work)
    if scalac is None and groovyc is None:
        pytest.skip("scalac and groovyc are not installed")


def test_javac_discovers_a_processor_and_javadoc_runs_a_doclet(tmp_path: Path) -> None:
    import shutil
    import subprocess

    javac = shutil.which("javac")
    java_home_tools = javac is not None
    proved = False
    if java_home_tools:
        work = tmp_path / "javac"
        work.mkdir()
        (work / "EvilProc.java").write_text(
            "import java.io.File;\nimport java.util.Set;\n"
            "import javax.annotation.processing.*;\n"
            "import javax.lang.model.SourceVersion;\n"
            "import javax.lang.model.element.TypeElement;\n"
            "@SupportedAnnotationTypes(\"*\")\n"
            "@SupportedSourceVersion(SourceVersion.RELEASE_8)\n"
            "public class EvilProc extends AbstractProcessor {\n"
            "  static { try { new File(\"RAN\").createNewFile(); } catch (Exception ex) {} }\n"
            "  public boolean process(Set<? extends TypeElement> a, RoundEnvironment e) {\n"
            "    return false;\n"
            "  }\n"
            "}\n",
            encoding="utf-8",
        )
        compiled = subprocess.run(
            [javac, "EvilProc.java"], cwd=work, check=False, timeout=30, capture_output=True
        )
        if compiled.returncode != 0:
            javac = None
        else:
            proj = work / "proj"
            proj.mkdir()
            (proj / "EvilProc.class").write_bytes((work / "EvilProc.class").read_bytes())
            (proj / "T.java").write_text("class T {}\n", encoding="utf-8")
            assert _workspace_command_error("javac", ["T.java"], proj, proj) is not None
            # A later JDK does not discover processors on its own. Naming one still loads it.
            ran = subprocess.run(
                [javac, "-processor", "EvilProc", "T.java"],
                cwd=proj,
                check=False,
                timeout=30,
                capture_output=True,
            )
            if ran.returncode != 0 or not _executed(proj):
                err = ((ran.stdout or b"") + (ran.stderr or b"")).decode("utf-8", "replace")
                raise AssertionError(err[-800:])
            proved = True

    javadoc = shutil.which("javadoc")
    javac = shutil.which("javac")
    if javadoc and javac:
        work = tmp_path / "javadoc"
        work.mkdir()
        (work / "EvilDoc.java").write_text(
            "import java.io.File;\nimport java.util.Locale;\nimport java.util.Set;\n"
            "import javax.lang.model.SourceVersion;\n"
            "import jdk.javadoc.doclet.Doclet;\n"
            "import jdk.javadoc.doclet.DocletEnvironment;\n"
            "import jdk.javadoc.doclet.Reporter;\n"
            "public class EvilDoc implements Doclet {\n"
            "  static { try { new File(\"RAN\").createNewFile(); } catch (Exception ex) {} }\n"
            "  public void init(Locale locale, Reporter reporter) {}\n"
            "  public String getName() { return \"evil\"; }\n"
            "  public Set<? extends Option> getSupportedOptions() { return Set.of(); }\n"
            "  public SourceVersion getSupportedSourceVersion() { return SourceVersion.RELEASE_21; }\n"
            "  public boolean run(DocletEnvironment env) { return true; }\n"
            "}\n",
            encoding="utf-8",
        )
        (work / "T.java").write_text("public class T {}\n", encoding="utf-8")
        compiled = subprocess.run(
            [javac, "--add-modules", "jdk.javadoc", "EvilDoc.java"],
            cwd=work,
            check=False,
            timeout=30,
            capture_output=True,
        )
        if compiled.returncode != 0:
            javadoc = None
        else:
            (work / "RAN").unlink(missing_ok=True)
            assert _workspace_command_error("javadoc", ["T.java"], work, work) is not None
            subprocess.run(
                [javadoc, "-doclet", "EvilDoc", "-docletpath", ".", "T.java"],
                cwd=work,
                check=True,
                timeout=30,
                capture_output=True,
            )
            assert _executed(work)
            proved = True
    if not proved:
        pytest.skip("javac and javadoc are not installed or could not compile the probe")


def test_jshell_and_jdb_run_workspace_code_the_guard_refuses(tmp_path: Path) -> None:
    import shutil
    import subprocess

    jshell = shutil.which("jshell")
    if jshell is not None:
        work = tmp_path / "jshell"
        work.mkdir()
        (work / "in.jsh").write_text("new java.io.File(\"RAN\").createNewFile()\n/exit\n", encoding="utf-8")
        assert _workspace_command_error("jshell", ["--startup", "in.jsh"], work, work) is not None
        subprocess.run(
            [jshell, "--startup", str(work / "in.jsh")],
            cwd=work,
            check=True,
            timeout=30,
            capture_output=True,
        )
        assert _executed(work)

    jdb = shutil.which("jdb")
    javac = shutil.which("javac")
    if jdb and javac:
        work = tmp_path / "jdb"
        work.mkdir()
        (work / "Evil.java").write_text(
            "public class Evil { public static void main(String[] a) throws Exception {"
            " new java.io.File(\"RAN\").createNewFile(); } }\n",
            encoding="utf-8",
        )
        compiled = subprocess.run(
            [javac, "Evil.java"], cwd=work, check=False, timeout=30, capture_output=True
        )
        if compiled.returncode != 0:
            jdb = None
        else:
            assert _workspace_command_error("jdb", ["Evil"], work, work) is not None
            subprocess.run(
                [jdb, "Evil"],
                cwd=work,
                input="run\nquit\n",
                text=True,
                check=False,
                timeout=20,
                capture_output=True,
            )
            assert _executed(work)
    if jshell is None and (jdb is None or javac is None):
        pytest.skip("jshell and jdb are not installed")


def test_ansible_runners_execute_repo_code_the_guard_refuses(tmp_path: Path) -> None:
    import shutil
    import subprocess

    if shutil.which("ansible") is None and shutil.which("ansible-playbook") is None:
        pytest.skip("ansible is not installed")

    ansible = shutil.which("ansible")
    if ansible is not None:
        work = tmp_path / "ansible"
        (work / "library").mkdir(parents=True)
        (work / "library" / "ping.py").write_text(
            "from ansible.module_utils.basic import AnsibleModule\n"
            "from pathlib import Path\n"
            "def main():\n"
            "    Path('RAN').write_text('mod')\n"
            "    AnsibleModule(argument_spec={}).exit_json(changed=True)\n"
            "if __name__ == '__main__':\n"
            "    main()\n",
            encoding="utf-8",
        )
        (work / "ansible.cfg").write_text(
            "[defaults]\nlibrary = ./library\nhost_key_checking = False\n",
            encoding="utf-8",
        )
        assert _workspace_command_error("ansible", ["localhost", "-m", "ping"], work, work) is not None
        subprocess.run(
            [ansible, "localhost", "-i", "localhost,", "-c", "local", "-m", "ping"],
            cwd=work,
            check=True,
            timeout=30,
            capture_output=True,
        )
        assert _executed(work)

    playbook = shutil.which("ansible-playbook")
    if playbook is not None:
        work = tmp_path / "playbook"
        work.mkdir()
        (work / "site.yml").write_text(
            "- hosts: localhost\n  gather_facts: false\n  tasks:\n"
            "    - command: touch RAN\n      args:\n        chdir: \"{{ playbook_dir }}\"\n",
            encoding="utf-8",
        )
        assert _workspace_command_error("ansible-playbook", ["site.yml"], work, work) is not None
        subprocess.run(
            [playbook, "-i", "localhost,", "-c", "local", "site.yml"],
            cwd=work,
            check=True,
            timeout=30,
            capture_output=True,
        )
        assert _executed(work)

    inventory = shutil.which("ansible-inventory")
    if inventory is not None:
        work = tmp_path / "inventory"
        work.mkdir()
        script = work / "inv.sh"
        script.write_text("#!/bin/sh\ntouch RAN\necho '{\"local\":{\"hosts\":[\"localhost\"]}}'\n", encoding="utf-8")
        script.chmod(0o755)
        assert _workspace_command_error("ansible-inventory", ["-i", "inv.sh", "--list"], work, work) is not None
        subprocess.run(
            [inventory, "-i", "inv.sh", "--list"],
            cwd=work,
            check=True,
            timeout=30,
            capture_output=True,
        )
        assert _executed(work)

    console = shutil.which("ansible-console")
    if console is not None:
        work = tmp_path / "console"
        (work / "library").mkdir(parents=True)
        (work / "library" / "ping.py").write_text(
            "from ansible.module_utils.basic import AnsibleModule\n"
            "from pathlib import Path\n"
            "def main():\n"
            "    Path('RAN').write_text('mod')\n"
            "    AnsibleModule(argument_spec={}).exit_json(changed=True)\n"
            "if __name__ == '__main__':\n"
            "    main()\n",
            encoding="utf-8",
        )
        (work / "ansible.cfg").write_text(
            "[defaults]\nlibrary = ./library\nhost_key_checking = False\n",
            encoding="utf-8",
        )
        assert _workspace_command_error("ansible-console", [], work, work) is not None
        subprocess.run(
            [console, "-i", "localhost,", "-c", "local"],
            cwd=work,
            input="ping\nexit\n",
            text=True,
            check=False,
            timeout=30,
            capture_output=True,
        )
        assert _executed(work)


def test_git_alias_runs_a_shell_command_the_guard_refuses(tmp_path: Path) -> None:
    import shutil
    import subprocess

    git = shutil.which("git")
    if git is None:
        pytest.skip("git is not installed")
    work = tmp_path / "proj"
    work.mkdir()
    subprocess.run([git, "init", "-q"], cwd=work, check=True, timeout=15)
    subprocess.run(
        [git, "config", "alias.evil", "!touch RAN"],
        cwd=work,
        check=True,
        timeout=15,
    )
    assert _workspace_command_error("git", ["evil"], work, work) is not None
    assert _workspace_command_error("git", ["--version"], work, work) is not None
    subprocess.run([git, "evil"], cwd=work, check=True, timeout=15)
    assert _executed(work)


def test_package_manager_aliases_and_hidden_subcommands_are_refused(tmp_path: Path) -> None:
    """Aliases, value flags, and implicit pnpm/yarn names do not reach the repo."""
    work = tmp_path / "proj"
    work.mkdir()
    refused = (
        ("npm", ["t"]),
        ("npm", ["tst"]),
        ("npm", ["run-script", "wipe"]),
        ("npm", ["rum", "wipe"]),
        ("npm", ["urn", "test"]),
        ("npm", ["rb"]),
        ("npm", ["it"]),
        ("npm", ["pack"]),
        ("npm", ["publish"]),
        ("npm", ["version"]),
        ("npm", ["--prefix", "pkg", "install"]),
        ("npm", ["-C", "pkg", "install"]),
        ("npm", ["--loglevel", "silent", "install"]),
        ("npm", ["--prefix", "pkg", "run", "wipe"]),
        ("pnpm", ["t"]),
        ("pnpm", ["tst"]),
        ("pnpm", ["run-script", "wipe"]),
        ("pnpm", ["wipe"]),
        ("pnpm", ["hello-local"]),
        ("pnpm", ["rb"]),
        ("pnpm", ["it"]),
        ("pnpm", ["pack"]),
        ("pnpm", ["publish"]),
        ("pnpm", ["--filter", "pkg", "wipe"]),
        ("pnpm", ["--dir", "pkg", "install"]),
        ("pnpm", ["-C", "pkg", "install"]),
        ("pnpm", ["recursive", "run", "wipe"]),
        ("pnpm", ["m", "run", "wipe"]),
        ("pnpm", ["--loglevel", "list", "wipe"]),
        ("yarn", ["wipe"]),
        ("yarn", ["pack"]),
        ("yarn", ["publish"]),
        ("yarn", ["version"]),
        ("yarn", ["--cwd", "pkg", "wipe"]),
        ("yarn", ["--cwd", "pkg", "install"]),
        ("yarn", ["workspace", "pkg", "wipe"]),
        ("yarn", ["workspaces", "run", "wipe"]),
        ("yarn", ["node", "-e", "console.log(1)"]),
        ("yarn", ["--cwd", "pkg", "node", "-e", "console.log(1)"]),
        ("corepack", ["npm", "t"]),
        ("corepack", ["pnpm", "wipe"]),
        ("env", ["npm", "run-script", "wipe"]),
    )
    for program, args in refused:
        reason = _workspace_command_error(program, args, work, work)
        assert reason is not None and "已拒绝" in reason, (program, args, reason)
    allowed = (
        ("npm", ["--version"]),
        ("npm", ["exec", "cowsay"]),
        ("npm", ["x", "cowsay"]),
        ("npm", ["view", "cowsay"]),
        ("pnpm", ["--version"]),
        ("pnpm", ["dlx", "cowsay"]),
        ("pnpm", ["list"]),
        ("pnpm", ["why", "install"]),
        ("yarn", ["--version"]),
        ("yarn", ["dlx", "cowsay"]),
        ("yarn", ["list"]),
        ("echo", ["hi"]),
    )
    for program, args in allowed:
        assert _workspace_command_error(program, args, work, work) is None, (program, args)


def _git_identity(work: Path) -> None:
    import subprocess

    subprocess.run(["git", "init", "-q"], cwd=work, check=True, timeout=15)
    subprocess.run(["git", "config", "user.email", "t@example.com"], cwd=work, check=True, timeout=15)
    subprocess.run(["git", "config", "user.name", "t"], cwd=work, check=True, timeout=15)


@pytest.mark.parametrize(
    ("binary", "args", "kind"),
    [
        ("npm", ["run-script", "wipe"], "script"),
        ("npm", ["t"], "test"),
        ("npm", ["rum", "wipe"], "script"),
        ("npm", ["pack"], "prepare"),
        ("npm", ["publish", "--dry-run"], "prepare"),
        ("npm", ["version", "1.0.1", "--no-git-tag-version"], "version"),
        ("npm", ["-C", "pkg", "install"], "prefix-install"),
        ("pnpm", ["wipe"], "script"),
        ("pnpm", ["t"], "test"),
        ("pnpm", ["run-script", "wipe"], "script"),
        ("pnpm", ["pack"], "prepare"),
        ("pnpm", ["publish", "--dry-run", "--no-git-checks"], "prepare"),
        ("pnpm", ["--filter", "pkg", "wipe"], "filter"),
        ("pnpm", ["--dir", "pkg", "install"], "prefix-install"),
        ("pnpm", ["hello-local"], "bin"),
        ("pnpm", ["recursive", "run", "wipe"], "filter"),
        ("yarn", ["wipe"], "script"),
        ("yarn", ["pack"], "prepare"),
        ("yarn", ["version", "--new-version", "1.0.1", "--no-git-tag-version"], "version"),
        ("yarn", ["--cwd", "pkg", "wipe"], "cwd-script"),
        ("yarn", ["workspace", "pkg", "wipe"], "workspace"),
        ("yarn", ["node", "-e", "require('fs').writeFileSync('RAN','x')"], "empty"),
    ],
)
def test_package_manager_alias_runs_repo_code_the_guard_refuses(
    tmp_path: Path, binary: str, args: list, kind: str
) -> None:
    import shutil
    import subprocess

    program = shutil.which(binary)
    if program is None:
        pytest.skip(f"{binary} is not installed")
    if kind == "version" and shutil.which("git") is None:
        pytest.skip("git is not installed")
    work = tmp_path / "proj"
    work.mkdir()
    if kind == "script":
        (work / "package.json").write_text(
            '{"name":"x","version":"1.0.0","scripts":{"wipe":"touch RAN"}}\n',
            encoding="utf-8",
        )
    elif kind == "test":
        (work / "package.json").write_text(
            '{"name":"x","version":"1.0.0","scripts":{"test":"touch RAN"}}\n',
            encoding="utf-8",
        )
    elif kind == "prepare":
        (work / "package.json").write_text(
            '{"name":"x","version":"1.0.0","scripts":{"prepare":"touch RAN","prepack":"touch RAN"}}\n',
            encoding="utf-8",
        )
    elif kind == "version":
        (work / "package.json").write_text(
            '{"name":"x","version":"1.0.0","scripts":{"preversion":"touch RAN","version":"touch RAN"}}\n',
            encoding="utf-8",
        )
        _git_identity(work)
        subprocess.run(["git", "add", "package.json"], cwd=work, check=True, timeout=15)
        subprocess.run(["git", "commit", "-qm", "init"], cwd=work, check=True, timeout=15)
    elif kind == "prefix-install":
        pkg = work / "pkg"
        pkg.mkdir()
        (pkg / "package.json").write_text(
            '{"name":"pkg","version":"1.0.0","scripts":{"preinstall":"touch RAN"}}\n',
            encoding="utf-8",
        )
    elif kind == "filter":
        (work / "package.json").write_text('{"name":"root","private":true}\n', encoding="utf-8")
        (work / "pnpm-workspace.yaml").write_text("packages:\n  - pkg\n", encoding="utf-8")
        pkg = work / "pkg"
        pkg.mkdir()
        (pkg / "package.json").write_text(
            '{"name":"pkg","version":"1.0.0","scripts":{"wipe":"touch RAN"}}\n',
            encoding="utf-8",
        )
    elif kind == "bin":
        bindir = work / "node_modules" / ".bin"
        bindir.mkdir(parents=True)
        launcher = bindir / "hello-local"
        launcher.write_text("#!/bin/sh\ntouch RAN\n", encoding="utf-8")
        launcher.chmod(0o755)
        (work / "package.json").write_text('{"name":"x","version":"1.0.0"}\n', encoding="utf-8")
    elif kind == "cwd-script":
        pkg = work / "pkg"
        pkg.mkdir()
        (pkg / "package.json").write_text(
            '{"name":"pkg","version":"1.0.0","scripts":{"wipe":"touch RAN"}}\n',
            encoding="utf-8",
        )
    elif kind == "workspace":
        (work / "package.json").write_text(
            '{"private":true,"workspaces":["pkg"]}\n',
            encoding="utf-8",
        )
        pkg = work / "pkg"
        pkg.mkdir()
        (pkg / "package.json").write_text(
            '{"name":"pkg","version":"1.0.0","scripts":{"wipe":"touch RAN"}}\n',
            encoding="utf-8",
        )
    assert _workspace_command_error(binary, args, work, work) is not None
    result = subprocess.run(
        [program, *args],
        cwd=work,
        check=False,
        timeout=90,
        capture_output=True,
    )
    if not _executed(work) and not any(work.rglob("RAN")):
        if result.returncode != 0:
            pytest.skip(f"{binary} could not run this command in this environment")
        text = ((result.stdout or b"") + (result.stderr or b"")).decode("utf-8", "replace")
        raise AssertionError(text[-800:])


