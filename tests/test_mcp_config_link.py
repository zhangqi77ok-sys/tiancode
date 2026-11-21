"""MCP config must not follow a link to a file outside the workspace.

The canary is a local fake. The test does not use the network or a credential.
"""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path

from tiancode.core.mcp_client import load_mcp_configs
from tiancode.core.mcp_service import McpService


def _outside_config(path: Path, marker: Path) -> None:
    payload = {
        "mcpServers": {
            "leak": {
                "command": sys.executable,
                "args": ["-c", f"open({str(marker)!r}, 'w').write('RAN')"],
                "note": "CANARY-OUTSIDE",
            }
        }
    }
    path.write_text(json.dumps(payload), encoding="utf-8")


def _assert_not_adopted(configs: dict, errors: list[str], outside: Path, marker: Path) -> None:
    blob = "\n".join(errors)
    assert "leak" not in configs
    assert "CANARY-OUTSIDE" not in blob
    assert "RAN" not in blob
    assert outside.read_text(encoding="utf-8").count("CANARY-OUTSIDE") == 1
    assert not marker.exists()


def test_workspace_symlink_and_hardlink_are_not_loaded(tmp_path: Path) -> None:
    outside = tmp_path / "outside.json"
    marker = tmp_path / "ran.txt"
    _outside_config(outside, marker)
    global_cfg = tmp_path / "global.json"
    global_cfg.write_text('{"mcpServers":{}}', encoding="utf-8")

    linked = tmp_path / "linked"
    (linked / ".tiancode").mkdir(parents=True)
    (linked / ".tiancode" / "mcp.json").symlink_to(outside)
    configs, errors = load_mcp_configs(linked, global_config_path=global_cfg)
    assert any("符号链接" in item for item in errors)
    _assert_not_adopted(configs, errors, outside, marker)

    hard = tmp_path / "hard"
    (hard / ".tiancode").mkdir(parents=True)
    os.link(outside, hard / ".tiancode" / "mcp.json")
    configs, errors = load_mcp_configs(hard, global_config_path=global_cfg)
    assert any("硬链接" in item for item in errors)
    _assert_not_adopted(configs, errors, outside, marker)

    parent = tmp_path / "parent"
    parent.mkdir()
    outside_dir = tmp_path / "outside-dir"
    outside_dir.mkdir()
    (outside_dir / "mcp.json").write_text(outside.read_text(encoding="utf-8"), encoding="utf-8")
    (parent / ".tiancode").symlink_to(outside_dir, target_is_directory=True)
    configs, errors = load_mcp_configs(parent, global_config_path=global_cfg)
    assert any("安全拦截" in item for item in errors)
    _assert_not_adopted(configs, errors, outside, marker)
    assert (outside_dir / "mcp.json").read_text(encoding="utf-8").count("CANARY-OUTSIDE") == 1


def test_global_symlink_is_not_loaded(tmp_path: Path) -> None:
    outside = tmp_path / "outside.json"
    marker = tmp_path / "ran.txt"
    _outside_config(outside, marker)
    global_link = tmp_path / "global.json"
    global_link.symlink_to(outside)
    work = tmp_path / "work"
    work.mkdir()

    configs, errors = load_mcp_configs(work, global_config_path=global_link)

    assert any("符号链接" in item for item in errors)
    _assert_not_adopted(configs, errors, outside, marker)


def test_reload_does_not_start_a_command_from_a_linked_config(tmp_path: Path) -> None:
    outside = tmp_path / "outside.json"
    marker = tmp_path / "ran.txt"
    _outside_config(outside, marker)
    global_cfg = tmp_path / "global.json"
    global_cfg.write_text('{"mcpServers":{}}', encoding="utf-8")
    work = tmp_path / "work"
    (work / ".tiancode").mkdir(parents=True)
    (work / ".tiancode" / "mcp.json").symlink_to(outside)

    service = McpService(work_dir=work, global_config_path=global_cfg)
    statuses = service.reload()

    assert statuses == []
    assert any("符号链接" in item for item in service.config_errors())
    assert "CANARY-OUTSIDE" not in "\n".join(service.config_errors())
    assert not marker.exists()
    assert outside.read_text(encoding="utf-8").count("CANARY-OUTSIDE") == 1
    service.close()
