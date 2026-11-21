"""Symlink writes and deletes restore the link itself and do not open its target.

The canary is a local fake. The test does not use the network or a credential.
"""

from __future__ import annotations

import json
import os
from pathlib import Path

from tiancode.core.git_store import GitSnapshotStore
from tiancode.core.history import InMemorySnapshotStore
from tiancode.core.path_safety import WorkspacePathError
from tiancode.core.session_store import SessionStore
from tiancode.tools.file_ops import create_file_tools


def _tools(work: Path, store=None):
    chosen = store if store is not None else InMemorySnapshotStore(work)
    tools = {tool.name: tool for tool in create_file_tools(chosen, work, auto_heal=False)}
    return chosen, tools


def _assert_link(path: Path, link_text: str) -> None:
    assert path.is_symlink()
    assert os.readlink(path) == link_text


def test_write_and_delete_undo_restore_relative_symlink(tmp_path: Path) -> None:
    work = tmp_path / "work"
    work.mkdir()
    target = work / "important.txt"
    target.write_text("KEEP-IMPORTANT\n", encoding="utf-8")
    link = work / "alias.txt"
    link.symlink_to(Path("important.txt"))
    link_text = os.readlink(link)
    store, tools = _tools(work)

    edited = tools["edit_file"].invoke(
        {"filename": "alias.txt", "old_code": "KEEP-IMPORTANT\n", "new_code": "EDITED\n"}
    )
    assert "未读取或修改其目标" in edited
    assert "KEEP-IMPORTANT" not in edited
    assert target.read_text(encoding="utf-8") == "KEEP-IMPORTANT\n"
    _assert_link(link, link_text)

    written = tools["write_file"].invoke({"filename": "alias.txt", "content": "NEW-ALIAS\n"})
    assert "已用普通文件替换符号链接" in written
    assert "/undo 会把该名字还原成原来的符号链接" in written
    assert "KEEP-IMPORTANT" not in written
    assert target.read_text(encoding="utf-8") == "KEEP-IMPORTANT\n"
    assert link.is_file() and not link.is_symlink()
    assert link.read_text(encoding="utf-8") == "NEW-ALIAS\n"
    snap = next(iter(store.get_all_snapshots().values()))
    assert snap.link_target == link_text
    assert snap.original_content == ""
    assert snap.is_created_new is False

    store.rollback()
    _assert_link(link, link_text)
    assert target.read_text(encoding="utf-8") == "KEEP-IMPORTANT\n"

    deleted = tools["delete_file"].invoke({"filename": "alias.txt"})
    assert "已删除符号链接" in deleted
    assert "/undo 会把该名字还原成原来的符号链接" in deleted
    assert "无损恢复" not in deleted
    assert not link.exists() and not link.is_symlink()
    assert target.read_text(encoding="utf-8") == "KEEP-IMPORTANT\n"

    store.rollback()
    _assert_link(link, link_text)
    assert target.read_text(encoding="utf-8") == "KEEP-IMPORTANT\n"


def test_outside_and_dangling_links_roundtrip_without_opening_target(tmp_path: Path) -> None:
    work = tmp_path / "work"
    work.mkdir()
    canary = tmp_path / "outside.txt"
    canary.write_text("CANARY-OUTSIDE\n", encoding="utf-8")
    outside = work / "escape.txt"
    outside.symlink_to(canary)
    outside_text = os.readlink(outside)
    dangling = work / "missing.txt"
    dangling.symlink_to("missing-target.txt")
    dangling_text = os.readlink(dangling)
    store, tools = _tools(work)

    edited = tools["edit_file"].invoke(
        {"filename": "escape.txt", "old_code": "CANARY-OUTSIDE\n", "new_code": "EDITED\n"}
    )
    assert "未读取或修改其目标" in edited
    assert "CANARY-OUTSIDE" not in edited
    assert canary.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"
    _assert_link(outside, outside_text)

    written = tools["write_file"].invoke({"filename": "escape.txt", "content": "NEW-FILE\n"})
    assert "CANARY-OUTSIDE" not in written
    assert canary.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"
    assert outside.is_file() and not outside.is_symlink()
    store.rollback()
    _assert_link(outside, outside_text)
    assert canary.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"

    deleted = tools["delete_file"].invoke({"filename": "escape.txt"})
    assert "CANARY-OUTSIDE" not in deleted
    assert not outside.exists() and not outside.is_symlink()
    assert canary.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"
    store.rollback()
    _assert_link(outside, outside_text)
    assert canary.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"

    tools["write_file"].invoke({"filename": "missing.txt", "content": "NEW-DANGLING\n"})
    assert dangling.is_file() and not dangling.is_symlink()
    assert not (work / "missing-target.txt").exists()
    store.rollback()
    _assert_link(dangling, dangling_text)
    assert not (work / "missing-target.txt").exists()

    tools["delete_file"].invoke({"filename": "missing.txt"})
    assert not dangling.exists() and not dangling.is_symlink()
    store.rollback()
    _assert_link(dangling, dangling_text)
    assert not (work / "missing-target.txt").exists()


def test_undo_leaves_regular_file_when_restore_name_is_taken(tmp_path: Path) -> None:
    work = tmp_path / "work"
    work.mkdir()
    target = work / "important.txt"
    target.write_text("KEEP-IMPORTANT\n", encoding="utf-8")
    link = work / "alias.txt"
    link.symlink_to(Path("important.txt"))
    link_text = os.readlink(link)
    store, tools = _tools(work)
    tools["write_file"].invoke({"filename": "alias.txt", "content": "NEW-ALIAS\n"})
    blocker = work / ".alias.txt.tiancode-restore-link"
    blocker.write_text("BLOCK\n", encoding="utf-8")

    try:
        store.rollback()
        raised = False
    except WorkspacePathError:
        raised = True

    assert raised
    assert link.is_file() and not link.is_symlink()
    assert link.read_text(encoding="utf-8") == "NEW-ALIAS\n"
    assert blocker.read_text(encoding="utf-8") == "BLOCK\n"
    assert target.read_text(encoding="utf-8") == "KEEP-IMPORTANT\n"
    blocker.unlink()
    store.rollback()
    _assert_link(link, link_text)
    assert not blocker.exists()
    assert target.read_text(encoding="utf-8") == "KEEP-IMPORTANT\n"


def test_rejected_symlink_write_leaves_link_and_target(tmp_path: Path) -> None:
    work = tmp_path / "work"
    work.mkdir()
    target = work / "important.py"
    target.write_text("KEEP-IMPORTANT = 1\n", encoding="utf-8")
    link = work / "alias.py"
    link.symlink_to(Path("important.py"))
    link_text = os.readlink(link)
    _store, tools = _tools(work)

    blocked = tools["write_file"].invoke({"filename": "alias.py", "content": "def bad(\n"})
    assert "语法守卫" in blocked
    assert target.read_text(encoding="utf-8") == "KEEP-IMPORTANT = 1\n"
    _assert_link(link, link_text)

    text_link = work / "alias.txt"
    text_link.symlink_to(Path("important.py"))
    text_link_text = os.readlink(text_link)
    failed = tools["write_file"].invoke({"filename": "alias.txt", "content": "NEW\ud800"})
    assert "写入已中止" in failed
    assert "符号链接保持不变" in failed
    assert target.read_text(encoding="utf-8") == "KEEP-IMPORTANT = 1\n"
    _assert_link(text_link, text_link_text)
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


def test_session_reload_undo_restores_link_and_old_text_snapshot(tmp_path: Path) -> None:
    work = tmp_path / "work"
    work.mkdir()
    target = work / "important.txt"
    target.write_text("KEEP-IMPORTANT\n", encoding="utf-8")
    canary = tmp_path / "outside.txt"
    canary.write_text("CANARY-OUTSIDE\n", encoding="utf-8")
    link = work / "alias.txt"
    link.symlink_to(Path("important.txt"))
    link_text = os.readlink(link)
    outside = work / "escape.txt"
    outside.symlink_to(Path("..") / "outside.txt")
    outside_text = os.readlink(outside)
    plain = work / "plain.txt"
    plain.write_text("DIRTY\n", encoding="utf-8")
    store, tools = _tools(work)
    store.record_snapshot(str(plain), "OLD-TEXT\n", is_new=False)
    assert "原来的符号链接" in tools["write_file"].invoke({"filename": "alias.txt", "content": "NEW-ALIAS\n"})
    assert "原来的符号链接" in tools["delete_file"].invoke({"filename": "escape.txt"})

    sessions = SessionStore(storage_dir=tmp_path / "sessions")
    sessions.save("s-link", str(work), [], snapshots=store.export_snapshots())
    saved_file = next((tmp_path / "sessions").glob("*.json"))
    raw = saved_file.read_text(encoding="utf-8")
    assert "CANARY-OUTSIDE" not in raw
    assert "KEEP-IMPORTANT" not in raw
    payload = json.loads(raw)
    exported = {item["filename"]: item for item in payload["snapshots"]}
    plain_item = next(item for item in exported.values() if item["filename"].endswith("plain.txt"))
    assert "link_target" not in plain_item
    assert plain_item["original_content"] == "OLD-TEXT\n"
    link_item = next(item for item in exported.values() if item.get("link_target") == link_text)
    assert link_item["original_content"] == ""
    assert link_item["is_created_new"] is False

    loaded = sessions.load("s-link")
    assert loaded is not None
    resumed = InMemorySnapshotStore(work)
    resumed.import_snapshots(loaded.snapshots)
    resumed.rollback()

    _assert_link(link, link_text)
    _assert_link(outside, outside_text)
    assert target.read_text(encoding="utf-8") == "KEEP-IMPORTANT\n"
    assert canary.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"
    assert plain.read_text(encoding="utf-8") == "OLD-TEXT\n"


def test_git_checkpoint_and_import_restore_symlink(tmp_path: Path) -> None:
    work = tmp_path / "work"
    work.mkdir()
    target = work / "important.txt"
    target.write_text("KEEP-IMPORTANT\n", encoding="utf-8")
    link = work / "alias.txt"
    link.symlink_to(Path("important.txt"))
    link_text = os.readlink(link)
    store, tools = _tools(work, GitSnapshotStore(work))
    tools["write_file"].invoke({"filename": "alias.txt", "content": "NEW-ALIAS\n"})
    exported = store.export_snapshots()
    checkpoint = store.create_checkpoint("link")
    link.write_text("LATER\n", encoding="utf-8")

    store.rollback(checkpoint)
    _assert_link(link, link_text)
    assert target.read_text(encoding="utf-8") == "KEEP-IMPORTANT\n"

    link.unlink()
    link.write_text("NEW-AGAIN\n", encoding="utf-8")
    restored = GitSnapshotStore(work)
    restored.import_snapshots(exported)
    restored.rollback()
    _assert_link(link, link_text)
    assert target.read_text(encoding="utf-8") == "KEEP-IMPORTANT\n"
    assert "CANARY-OUTSIDE" not in json.dumps(exported)
