"""McpService facade: single entry for config, status, reload, and close."""

from __future__ import annotations

import json
from pathlib import Path

from tiancode.core.mcp_service import McpService


def _write_mcp_json(path: Path, servers: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps({"mcpServers": servers}, ensure_ascii=False), encoding="utf-8")


def test_mcp_service_status_summary_for_disabled_server(tmp_path: Path, monkeypatch) -> None:
    global_cfg = tmp_path / "global_mcp.json"
    _write_mcp_json(
        global_cfg,
        {"demo": {"command": "true", "disabled": True}},
    )
    monkeypatch.setenv("TIANCODE_MCP_CONFIG", str(global_cfg))

    work = tmp_path / "ws"
    work.mkdir()
    mcp = McpService(work_dir=work, global_config_path=global_cfg)

    statuses = mcp.statuses()
    assert len(statuses) == 1
    assert statuses[0].name == "demo"
    assert statuses[0].enabled is False
    assert statuses[0].connected is False

    summary = mcp.status_summary()
    assert summary is not None
    assert "demo:停用(0)" in summary
    assert mcp.config_errors() == []
    assert mcp.list_tools() == []


def test_mcp_service_workspace_overrides_global(tmp_path: Path, monkeypatch) -> None:
    global_cfg = tmp_path / "global_mcp.json"
    _write_mcp_json(global_cfg, {"shared": {"command": "echo", "disabled": True}})
    monkeypatch.setenv("TIANCODE_MCP_CONFIG", str(global_cfg))

    work = tmp_path / "project"
    ws_cfg = work / ".tiancode" / "mcp.json"
    _write_mcp_json(ws_cfg, {"shared": {"command": "echo", "disabled": True, "args": ["ws"]}})

    mcp = McpService(work_dir=work, global_config_path=global_cfg)
    statuses = mcp.reload()
    assert len(statuses) == 1
    assert "workspace:" in statuses[0].source


def test_mcp_service_reload_and_close(tmp_path: Path, monkeypatch) -> None:
    global_cfg = tmp_path / "global_mcp.json"
    _write_mcp_json(global_cfg, {})
    monkeypatch.setenv("TIANCODE_MCP_CONFIG", str(global_cfg))

    work = tmp_path / "empty_ws"
    work.mkdir()
    mcp = McpService(work_dir=work, global_config_path=global_cfg)
    assert mcp.reload() == []
    assert mcp.status_summary() is None
    mcp.close()
