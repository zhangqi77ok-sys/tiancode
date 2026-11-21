"""A symlinked package.json must not choose or run its test script.

The canary is a local fake. The test does not use the network or a credential.
"""

from __future__ import annotations

import json
from pathlib import Path

from tiancode.core.history import InMemorySnapshotStore
from tiancode.core.polyglot import detect_project_profile
from tiancode.tools.file_ops import create_file_tools


def _package(path: Path, script: str) -> None:
    path.write_text(json.dumps({"scripts": {"test": script}}), encoding="utf-8")


def test_regular_package_json_still_selects_npm_test(tmp_path: Path) -> None:
    _package(tmp_path / "package.json", "echo KEEP-TEST")

    profile = detect_project_profile(tmp_path)

    assert profile.test_command == "npm test"
    assert profile.language == "JavaScript/TypeScript"


def test_symlinked_package_json_does_not_run_on_write(tmp_path: Path) -> None:
    marker = tmp_path / "ran.txt"
    outside = tmp_path / "outside-package.json"
    script = f"python3 -c \"open({str(marker)!r}, 'w').write('CANARY-OUTSIDE')\""
    _package(outside, script)
    work = tmp_path / "work"
    work.mkdir()
    (work / "package.json").symlink_to(outside)
    (work / "note.txt").write_text("KEEP\n", encoding="utf-8")
    inside = work / "real-package.json"
    _package(inside, "echo CANARY-INSIDE")
    linked_inside = tmp_path / "inside-link"
    linked_inside.mkdir()
    _package(linked_inside / "real-package.json", "echo CANARY-INSIDE")
    (linked_inside / "package.json").symlink_to(Path("real-package.json"))
    store = InMemorySnapshotStore(work)
    tools = {tool.name: tool for tool in create_file_tools(store, work, auto_heal=True)}

    result = tools["write_file"].invoke({"filename": "note.txt", "content": "NEW\n"})

    assert "写入成功" in result
    assert "CANARY-OUTSIDE" not in result
    assert "单测验证通过" not in result
    assert not marker.exists()
    assert (work / "package.json").is_symlink()
    assert outside.read_text(encoding="utf-8").count("CANARY-OUTSIDE") == 1
    assert (work / "note.txt").read_text(encoding="utf-8") == "NEW\n"
    assert detect_project_profile(work).test_command is None
    assert detect_project_profile(linked_inside).test_command is None
