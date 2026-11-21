"""A failed write must not truncate the file it was editing.

The canary is a local fake. The test does not use the network or a credential.
"""

from __future__ import annotations

import os
import stat
from pathlib import Path

from tiancode.core.history import InMemorySnapshotStore
from tiancode.core.path_safety import restore_workspace_text
from tiancode.tools.file_ops import create_file_tools


def _mode(path: Path) -> int:
    return stat.S_IMODE(path.stat().st_mode)


def _tools(work: Path) -> dict:
    store = InMemorySnapshotStore(work)
    return {tool.name: tool for tool in create_file_tools(store, work, auto_heal=False)}


def test_failed_encode_keeps_existing_file_and_skips_new_file(tmp_path: Path) -> None:
    note = tmp_path / "note.txt"
    note.write_text("KEEP-LINE\n", encoding="utf-8")
    tools = _tools(tmp_path)

    replaced = tools["write_file"].invoke({"filename": "note.txt", "content": "OK\ud800"})
    created = tools["write_file"].invoke({"filename": "fresh.txt", "content": "NEW\ud800"})
    edited = tools["edit_file"].invoke(
        {"filename": "note.txt", "old_code": "KEEP-LINE\n", "new_code": "EDIT\ud800"}
    )

    assert "写入已中止" in replaced
    assert "写入已中止" in created
    assert "写入已中止" in edited
    assert note.read_text(encoding="utf-8") == "KEEP-LINE\n"
    assert not (tmp_path / "fresh.txt").exists()


def test_successful_write_replaces_text(tmp_path: Path) -> None:
    note = tmp_path / "note.txt"
    note.write_text("KEEP-LINE\n", encoding="utf-8")
    tools = _tools(tmp_path)

    result = tools["write_file"].invoke({"filename": "note.txt", "content": "NEW-LINE\n"})

    assert "写入成功" in result
    assert note.read_text(encoding="utf-8") == "NEW-LINE\n"
    assert note.is_file() and not note.is_symlink()


def test_write_does_not_modify_the_other_hardlink(tmp_path: Path) -> None:
    outside = tmp_path / "outside.txt"
    outside.write_text("CANARY-OUTSIDE\n", encoding="utf-8")
    work = tmp_path / "work"
    work.mkdir()
    alias = work / "alias.txt"
    os.link(outside, alias)
    tools = _tools(work)

    result = tools["write_file"].invoke({"filename": "alias.txt", "content": "NEW-LINE\n"})

    assert "写入成功" in result
    assert alias.read_text(encoding="utf-8") == "NEW-LINE\n"
    assert outside.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"


def test_existing_modes_survive_write_edit_and_restore(tmp_path: Path) -> None:
    previous = os.umask(0o077)
    try:
        note = tmp_path / "note.txt"
        script = tmp_path / "run.sh"
        note.write_text("KEEP\n", encoding="utf-8")
        script.write_text("KEEP\n", encoding="utf-8")
        os.chmod(note, 0o644)
        os.chmod(script, 0o755)
        tools = _tools(tmp_path)

        assert "写入成功" in tools["write_file"].invoke({"filename": "note.txt", "content": "NEW\n"})
        assert "局部替换成功" in tools["edit_file"].invoke(
            {"filename": "run.sh", "old_code": "KEEP\n", "new_code": "EDIT\n"}
        )
        restore_workspace_text(note, "UNDO\n")
        restore_workspace_text(script, "UNDO\n")

        assert note.read_text(encoding="utf-8") == "UNDO\n"
        assert script.read_text(encoding="utf-8") == "UNDO\n"
        assert _mode(note) == 0o644
        assert _mode(script) == 0o755

        failed = tools["write_file"].invoke({"filename": "run.sh", "content": "BAD\ud800"})
        assert "写入已中止" in failed
        assert script.read_text(encoding="utf-8") == "UNDO\n"
        assert _mode(script) == 0o755
        assert list(tmp_path.glob(".*tiancode-restore")) == []
    finally:
        os.umask(previous)


def test_new_file_is_not_wider_than_0644_masked_by_umask(tmp_path: Path) -> None:
    previous = os.umask(0o000)
    try:
        tools = _tools(tmp_path)
        assert "写入成功" in tools["write_file"].invoke({"filename": "fresh.txt", "content": "NEW\n"})
        created = tmp_path / "fresh.txt"
        assert _mode(created) == 0o644
        assert not created.is_symlink()
    finally:
        os.umask(previous)


def test_replacing_a_symlink_does_not_copy_its_mode(tmp_path: Path) -> None:
    previous = os.umask(0o022)
    try:
        outside = tmp_path / "outside.txt"
        outside.write_text("CANARY-OUTSIDE\n", encoding="utf-8")
        link = tmp_path / "link.txt"
        link.symlink_to(outside)

        restore_workspace_text(link, "NEW\n")

        assert outside.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"
        assert link.is_file() and not link.is_symlink()
        assert link.read_text(encoding="utf-8") == "NEW\n"
        assert _mode(link) == 0o644
        assert list(tmp_path.glob(".*tiancode-restore")) == []
    finally:
        os.umask(previous)
