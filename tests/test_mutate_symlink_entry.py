"""Writes, edits, and deletes must not follow a symlink to another file.

The canary is a local fake. The test does not use the network or a credential.
"""

from __future__ import annotations

from pathlib import Path

from tiancode.core.history import InMemorySnapshotStore
from tiancode.tools.file_ops import create_file_tools


def _tools(work: Path):
    store = InMemorySnapshotStore(work)
    tools = {tool.name: tool for tool in create_file_tools(store, work, auto_heal=False)}
    return store, tools


def test_in_workspace_symlink_mutation_does_not_change_target(tmp_path: Path) -> None:
    work = tmp_path / "work"
    work.mkdir()
    target = work / "important.txt"
    target.write_text("KEEP-IMPORTANT\n", encoding="utf-8")
    link = work / "alias.txt"
    link.symlink_to(target)
    store, tools = _tools(work)

    edited = tools["edit_file"].invoke(
        {"filename": "alias.txt", "old_code": "KEEP-IMPORTANT\n", "new_code": "EDITED\n"}
    )
    assert "符号链接" in edited
    assert "KEEP-IMPORTANT" not in edited
    assert target.read_text(encoding="utf-8") == "KEEP-IMPORTANT\n"
    assert link.is_symlink()

    written = tools["write_file"].invoke({"filename": "alias.txt", "content": "NEW-ALIAS\n"})
    assert "已用普通文件替换符号链接" in written
    assert "KEEP-IMPORTANT" not in written
    assert target.read_text(encoding="utf-8") == "KEEP-IMPORTANT\n"
    assert link.is_file() and not link.is_symlink()
    assert link.read_text(encoding="utf-8") == "NEW-ALIAS\n"

    store.rollback()
    assert not link.exists()
    assert target.read_text(encoding="utf-8") == "KEEP-IMPORTANT\n"

    link.symlink_to(target)
    deleted = tools["delete_file"].invoke({"filename": "alias.txt"})
    assert "已删除符号链接" in deleted
    assert "未删除其目标文件" in deleted
    assert not link.exists() and not link.is_symlink()
    assert target.read_text(encoding="utf-8") == "KEEP-IMPORTANT\n"
    store.rollback()
    assert not link.exists()
    assert target.read_text(encoding="utf-8") == "KEEP-IMPORTANT\n"


def test_outside_symlink_is_not_opened(tmp_path: Path) -> None:
    work = tmp_path / "work"
    work.mkdir()
    canary = tmp_path / "outside.txt"
    canary.write_text("CANARY-OUTSIDE\n", encoding="utf-8")
    link = work / "escape.txt"
    link.symlink_to(canary)
    _store, tools = _tools(work)

    edited = tools["edit_file"].invoke(
        {"filename": "escape.txt", "old_code": "CANARY-OUTSIDE\n", "new_code": "EDITED\n"}
    )
    assert "符号链接" in edited
    assert "CANARY-OUTSIDE" not in edited
    assert canary.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"
    assert link.is_symlink()

    written = tools["write_file"].invoke({"filename": "escape.txt", "content": "NEW-FILE\n"})
    assert "已用普通文件替换符号链接" in written
    assert "CANARY-OUTSIDE" not in written
    assert canary.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"
    assert link.is_file() and not link.is_symlink()
    assert link.read_text(encoding="utf-8") == "NEW-FILE\n"

    link.unlink()
    link.symlink_to(canary)
    deleted = tools["delete_file"].invoke({"filename": "escape.txt"})
    assert "已删除符号链接" in deleted
    assert canary.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"
    assert not link.exists() and not link.is_symlink()


def test_rejected_symlink_write_leaves_link_and_target(tmp_path: Path) -> None:
    work = tmp_path / "work"
    work.mkdir()
    target = work / "important.py"
    target.write_text("KEEP-IMPORTANT = 1\n", encoding="utf-8")
    link = work / "alias.py"
    link.symlink_to(target)
    _store, tools = _tools(work)

    blocked = tools["write_file"].invoke({"filename": "alias.py", "content": "def bad(\n"})
    assert "语法守卫" in blocked
    assert target.read_text(encoding="utf-8") == "KEEP-IMPORTANT = 1\n"
    assert link.is_symlink()

    text_link = work / "alias.txt"
    text_link.symlink_to(target)
    failed = tools["write_file"].invoke({"filename": "alias.txt", "content": "NEW\ud800"})
    assert "写入已中止" in failed
    assert "符号链接保持不变" in failed
    assert target.read_text(encoding="utf-8") == "KEEP-IMPORTANT = 1\n"
    assert text_link.is_symlink()
    assert not (work / ".alias.txt.tiancode-restore").exists()


def test_parent_symlink_and_traversal_do_not_write_outside(tmp_path: Path) -> None:
    work = tmp_path / "work"
    work.mkdir()
    outside = tmp_path / "outside"
    outside.mkdir()
    canary = outside / "note.txt"
    canary.write_text("CANARY-OUTSIDE\n", encoding="utf-8")
    (work / "linkdir").symlink_to(outside, target_is_directory=True)
    _store, tools = _tools(work)

    through_dir = tools["write_file"].invoke({"filename": "linkdir/note.txt", "content": "NEW\n"})
    escaped = tools["write_file"].invoke({"filename": "../outside/note.txt", "content": "NEW\n"})

    assert "安全拦截" in through_dir
    assert "安全拦截" in escaped
    assert canary.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"
    assert not (work / "note.txt").exists()
