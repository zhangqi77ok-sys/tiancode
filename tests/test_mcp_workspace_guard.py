"""Workspace MCP config fails closed on escaped cwd and unset secrets."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from tiancode.core.mcp_auth import McpAuthError, build_auth_headers
from tiancode.core.mcp_client import load_mcp_configs


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


def test_bearer_auth_rejects_an_unset_token(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("MISSING_MCP_TOKEN", raising=False)
    with pytest.raises(McpAuthError, match="MISSING_MCP_TOKEN"):
        build_auth_headers({"type": "bearer", "token": "${MISSING_MCP_TOKEN}"})
