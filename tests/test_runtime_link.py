"""TianShell runtime.json must not be followed when it is a link.

The canary is local fake text. The test does not open a network connection.
"""

from __future__ import annotations

import json
import os
from pathlib import Path

from tiancode.core.mcp_client import load_mcp_configs
from tiancode.core.mcp_discovery import discover_tianshell_mcp


def _runtime(path: Path, token: str) -> None:
    path.write_text(
        json.dumps({"pid": 0, "mcpUrl": "http://127.0.0.1:9/mcp", "token": token}),
        encoding="utf-8",
    )


def _isolate(monkeypatch, tmp_path: Path, runtime: Path) -> None:
    monkeypatch.setenv("TIANSHELL_RUNTIME_PATH", str(runtime))
    monkeypatch.setenv("TIANCODE_MCP_CONFIG", str(tmp_path / "mcp.json"))
    (tmp_path / "mcp.json").write_text('{"mcpServers":{}}', encoding="utf-8")


def test_regular_runtime_is_discovered(tmp_path: Path, monkeypatch) -> None:
    runtime = tmp_path / "runtime.json"
    _runtime(runtime, "PUBLIC-OK")
    _isolate(monkeypatch, tmp_path, runtime)

    found = discover_tianshell_mcp({})

    assert found is not None
    assert found.url == "http://127.0.0.1:9/mcp"
    assert found.headers.get("Authorization") == "Bearer PUBLIC-OK"
    assert "CANARY-OUTSIDE" not in found.source


def test_runtime_symlink_and_hardlink_are_not_discovered(tmp_path: Path, monkeypatch) -> None:
    outside = tmp_path / "outside.json"
    _runtime(outside, "CANARY-OUTSIDE")
    link = tmp_path / "runtime.json"
    link.symlink_to(outside)
    _isolate(monkeypatch, tmp_path, link)

    found = discover_tianshell_mcp({})
    configs, errors = load_mcp_configs(tmp_path)

    assert found is None
    assert "tianshell" not in configs
    assert "CANARY-OUTSIDE" not in "\n".join(errors)
    assert outside.read_text(encoding="utf-8").count("CANARY-OUTSIDE") == 1
    assert link.is_symlink()

    hard = tmp_path / "hard.json"
    os.link(outside, hard)
    monkeypatch.setenv("TIANSHELL_RUNTIME_PATH", str(hard))
    assert discover_tianshell_mcp({}) is None
    assert outside.read_text(encoding="utf-8").count("CANARY-OUTSIDE") == 1


def test_parent_directory_symlink_still_discovers_regular_runtime(tmp_path: Path, monkeypatch) -> None:
    real = tmp_path / "real"
    real.mkdir()
    runtime = real / "runtime.json"
    _runtime(runtime, "PUBLIC-OK")
    parent = tmp_path / "linked-home"
    parent.symlink_to(real, target_is_directory=True)
    _isolate(monkeypatch, tmp_path, parent / "runtime.json")

    found = discover_tianshell_mcp({})

    assert found is not None
    assert found.url == "http://127.0.0.1:9/mcp"
    assert found.headers.get("Authorization") == "Bearer PUBLIC-OK"
