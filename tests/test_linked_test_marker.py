"""Linked build markers must not choose or run an automatic test command.

The canary is local fake text. The test does not use the network or a credential.
"""

from __future__ import annotations

from pathlib import Path

from tiancode.core.history import InMemorySnapshotStore
from tiancode.core.polyglot import detect_project_profile
from tiancode.tools.file_ops import create_file_tools

_MAKEFILE = "test:\n\tpython3 -c \"open({target!r}, 'w').write({payload!r})\"\n"


def _makefile(path: Path, target: Path, payload: str) -> None:
    path.write_text(_MAKEFILE.format(target=str(target), payload=payload), encoding="utf-8")


def test_regular_makefile_still_runs_on_write(tmp_path: Path) -> None:
    marker = tmp_path / "keep.txt"
    _makefile(tmp_path / "Makefile", marker, "KEEP-TEST")
    (tmp_path / "note.txt").write_text("KEEP\n", encoding="utf-8")
    store = InMemorySnapshotStore(tmp_path)
    tools = {tool.name: tool for tool in create_file_tools(store, tmp_path, auto_heal=True)}

    result = tools["write_file"].invoke({"filename": "note.txt", "content": "NEW\n"})

    assert "写入成功" in result
    assert "单测验证通过" in result
    assert marker.read_text(encoding="utf-8") == "KEEP-TEST"
    assert detect_project_profile(tmp_path).test_command == "make test"


def test_symlinked_makefile_does_not_run_on_write(tmp_path: Path) -> None:
    marker = tmp_path / "ran.txt"
    outside = tmp_path / "outside.mk"
    _makefile(outside, marker, "CANARY-OUTSIDE")
    work = tmp_path / "work"
    work.mkdir()
    (work / "Makefile").symlink_to(outside)
    (work / "note.txt").write_text("KEEP\n", encoding="utf-8")
    inside = tmp_path / "inside"
    inside.mkdir()
    _makefile(inside / "real.mk", tmp_path / "unused.txt", "CANARY-INSIDE")
    (inside / "Makefile").symlink_to(Path("real.mk"))
    store = InMemorySnapshotStore(work)
    tools = {tool.name: tool for tool in create_file_tools(store, work, auto_heal=True)}

    result = tools["write_file"].invoke({"filename": "note.txt", "content": "NEW\n"})

    assert "写入成功" in result
    assert "CANARY-OUTSIDE" not in result
    assert "单测验证通过" not in result
    assert not marker.exists()
    assert (work / "Makefile").is_symlink()
    assert outside.read_text(encoding="utf-8").count("CANARY-OUTSIDE") == 1
    assert (work / "note.txt").read_text(encoding="utf-8") == "NEW\n"
    assert detect_project_profile(work).test_command is None
    assert detect_project_profile(inside).test_command is None


def test_regular_pom_selects_mvn_and_symlink_does_not(tmp_path: Path) -> None:
    plain = tmp_path / "java"
    plain.mkdir()
    (plain / "pom.xml").write_text("<project/>\n", encoding="utf-8")
    linked = tmp_path / "java-link"
    linked.mkdir()
    (linked / "pom.xml").symlink_to(plain / "pom.xml")

    assert detect_project_profile(plain).test_command == "mvn test"
    assert detect_project_profile(linked).test_command is None


def test_regular_python_file_still_selects_pytest(tmp_path: Path) -> None:
    (tmp_path / "kept.py").write_text("def kept():\n    return 1\n", encoding="utf-8")

    assert detect_project_profile(tmp_path).test_command == "pytest"


def test_symlinked_python_file_does_not_select_pytest(tmp_path: Path) -> None:
    outside = tmp_path / "outside.py"
    outside.write_text("print('CANARY-OUTSIDE')\n", encoding="utf-8")
    work = tmp_path / "work"
    work.mkdir()
    (work / "alias.py").symlink_to(outside)

    assert detect_project_profile(work).test_command is None
    assert outside.read_text(encoding="utf-8") == "print('CANARY-OUTSIDE')\n"
