"""A failed write must not truncate the file it was editing.

The canary is a local fake. The test does not use the network or a credential.
"""

from __future__ import annotations

import os
from pathlib import Path

from tiancode.core.history import InMemorySnapshotStore
from tiancode.tools.file_ops import create_file_tools


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
