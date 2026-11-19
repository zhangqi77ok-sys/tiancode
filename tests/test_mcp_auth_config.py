"""mcp.json auth / env expansion / type hint parsing."""

from __future__ import annotations

import json
from pathlib import Path

from tiancode.core.mcp_client import load_mcp_configs


def test_load_http_auth_and_env_expansion(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("REMOTE_MCP_URL", "https://platform.example.com/mcp")
    monkeypatch.setenv("REMOTE_MCP_TOKEN", "tok-env")
    global_cfg = tmp_path / "mcp.json"
    global_cfg.write_text(
        json.dumps(
            {
                "mcpServers": {
                    "remote": {
                        "type": "http",
                        "url": "${REMOTE_MCP_URL}",
                        "auth": {"type": "bearer", "token": "${REMOTE_MCP_TOKEN}"},
                        "headers": {"X-Trace": "tiancode"},
                    }
                }
            }
        ),
        encoding="utf-8",
    )
    work = tmp_path / "ws"
    work.mkdir()
    configs, errors = load_mcp_configs(work, global_config_path=global_cfg)
    assert errors == []
    cfg = configs["remote"]
    assert cfg.transport == "http"
    assert cfg.url == "https://platform.example.com/mcp"
    assert cfg.auth["type"] == "bearer"
    assert cfg.headers["X-Trace"] == "tiancode"
    # auth token is expanded at request time via transport; config keeps template or expanded?
    # load expands headers/url/command; auth dict values are expanded when building headers.
    assert cfg.auth["token"] == "${REMOTE_MCP_TOKEN}"


def test_load_client_credentials_auth_shape(tmp_path: Path) -> None:
    global_cfg = tmp_path / "mcp.json"
    global_cfg.write_text(
        json.dumps(
            {
                "mcpServers": {
                    "paid": {
                        "url": "https://mcp.vendor.com/v1",
                        "auth": {
                            "type": "client_credentials",
                            "tokenUrl": "https://auth.vendor.com/oauth/token",
                            "clientId": "cid",
                            "clientSecret": "sec",
                            "scopes": ["tools"],
                        },
                    }
                }
            }
        ),
        encoding="utf-8",
    )
    work = tmp_path / "ws"
    work.mkdir()
    configs, errors = load_mcp_configs(work, global_config_path=global_cfg)
    assert errors == []
    assert configs["paid"].auth["type"] == "client_credentials"
    assert configs["paid"].auth["tokenUrl"].startswith("https://auth.vendor.com")