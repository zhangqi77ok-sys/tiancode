"""Undo must not follow a symlink or write a snapshot path outside the work directory."""

from __future__ import annotations

import os
from pathlib import Path

from tiancode.core.git_store import GitSnapshotStore
from tiancode.core.history import InMemorySnapshotStore


def _plant_symlink(note: Path, outside: Path) -> None:
    note.unlink()
    note.symlink_to(outside)


def test_memory_undo_replaces_symlink_and_keeps_outside_file(tmp_path: Path) -> None:
    work = tmp_path / "work"
    work.mkdir()
    outside = tmp_path / "outside.txt"
    outside.write_text("OUTSIDE\n", encoding="utf-8")
    note = work / "note.txt"
    note.write_text("ORIGINAL\n", encoding="utf-8")
    store = InMemorySnapshotStore(work_dir=work)
    store.record_snapshot(str(note.resolve()), "ORIGINAL\n", is_new=False)
    _plant_symlink(note, outside)

    reverted = store.rollback()

    assert outside.read_text(encoding="utf-8") == "OUTSIDE\n"
    assert not note.is_symlink()
    assert note.read_text(encoding="utf-8") == "ORIGINAL\n"
    assert any("已还原" in item for item in reverted)


def test_git_undo_replaces_symlink_and_keeps_outside_file(tmp_path: Path) -> None:
    work = tmp_path / "work"
    work.mkdir()
    outside = tmp_path / "outside.txt"
    outside.write_text("OUTSIDE\n", encoding="utf-8")
    note = work / "note.txt"
    note.write_text("ORIGINAL\n", encoding="utf-8")
    store = GitSnapshotStore(work_dir=work)
    store.record_snapshot(str(note.resolve()), "ORIGINAL\n", is_new=False)
    _plant_symlink(note, outside)

    store.rollback()

    assert outside.read_text(encoding="utf-8") == "OUTSIDE\n"
    assert not note.is_symlink()
    assert note.read_text(encoding="utf-8") == "ORIGINAL\n"


def test_undo_of_new_file_unlinks_symlink_only(tmp_path: Path) -> None:
    work = tmp_path / "work"
    work.mkdir()
    outside = tmp_path / "outside.txt"
    outside.write_text("OUTSIDE\n", encoding="utf-8")
    note = work / "note.txt"
    note.symlink_to(outside)
    store = InMemorySnapshotStore(work_dir=work)
    store.record_snapshot(str(work / "note.txt"), "", is_new=True)

    store.rollback()

    assert outside.read_text(encoding="utf-8") == "OUTSIDE\n"
    assert not note.exists()


def test_imported_snapshot_outside_work_dir_is_not_written(tmp_path: Path) -> None:
    work = tmp_path / "work"
    work.mkdir()
    victim = tmp_path / "victim.txt"
    victim.write_text("KEEP\n", encoding="utf-8")
    payload = [{"filename": str(victim), "original_content": "CLOBBER\n", "is_created_new": False}]

    memory = InMemorySnapshotStore(work_dir=work)
    memory.import_snapshots(payload)
    memory.rollback()
    git_store = GitSnapshotStore(work_dir=work)
    git_store.import_snapshots(payload)
    git_store.rollback()

    assert victim.read_text(encoding="utf-8") == "KEEP\n"
    assert not (work / "victim.txt").exists()


def test_undo_of_hardlink_does_not_rewrite_the_other_name(tmp_path: Path) -> None:
    work = tmp_path / "work"
    work.mkdir()
    outside = tmp_path / "outside.txt"
    outside.write_text("SHARED\n", encoding="utf-8")
    note = work / "note.txt"
    os.link(outside, note)
    store = InMemorySnapshotStore(work_dir=work)
    store.record_snapshot(str(note), "ORIGINAL\n", is_new=False)

    store.rollback()

    assert outside.read_text(encoding="utf-8") == "SHARED\n"
    assert not note.is_symlink()
    assert note.stat().st_nlink == 1
    assert note.read_text(encoding="utf-8") == "ORIGINAL\n"


def test_diff_does_not_read_through_symlink(tmp_path: Path) -> None:
    work = tmp_path / "work"
    work.mkdir()
    outside = tmp_path / "outside.txt"
    outside.write_text("SECRET-OUTSIDE\n", encoding="utf-8")
    note = work / "note.txt"
    note.write_text("ORIGINAL\n", encoding="utf-8")
    store = InMemorySnapshotStore(work_dir=work)
    store.record_snapshot(str(note.resolve()), "ORIGINAL\n", is_new=False)
    _plant_symlink(note, outside)

    diffs = store.compute_diffs()
    blob = "\n".join(item.diff_text for item in diffs)
    assert "SECRET-OUTSIDE" not in blob
