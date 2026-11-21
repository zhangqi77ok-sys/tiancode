"""Generating a project guide must not follow a symlink or hard link.

The canary is a local fake. The test does not use the network or a credential.
"""

from __future__ import annotations

import os
import stat
from pathlib import Path

import pytest

from tiancode.core.project_rules import write_project_guide


def _mode(path: Path) -> int:
    return stat.S_IMODE(os.lstat(path).st_mode)


def test_new_guide_is_a_regular_file_and_second_call_keeps_it(tmp_path: Path) -> None:
    work = tmp_path / "work"
    work.mkdir()

    target, written = write_project_guide(work)
    again, rewritten = write_project_guide(work)

    assert written is True
    assert target == work / "TIANCODE.md"
    assert target.is_file() and not target.is_symlink()
    text = target.read_text(encoding="utf-8")
    assert "开发与协作规范" in text
    assert rewritten is False
    assert again.read_text(encoding="utf-8") == text
    assert not (work / "TIANCODE.md.tmp").exists()
    assert not (work / ".TIANCODE.md.tiancode-restore").exists()


def test_tmp_symlink_and_hardlink_do_not_receive_the_guide(tmp_path: Path) -> None:
    work = tmp_path / "work"
    work.mkdir()
    symlink_canary = tmp_path / "symlink-canary.txt"
    hardlink_canary = tmp_path / "hardlink-canary.txt"
    symlink_canary.write_text("CANARY-TMP-SYMLINK\n", encoding="utf-8")
    hardlink_canary.write_text("CANARY-TMP-HARD\n", encoding="utf-8")
    hard_inode = os.lstat(hardlink_canary).st_ino
    (work / "TIANCODE.md.tmp").symlink_to(symlink_canary)
    os.link(hardlink_canary, work / ".TIANCODE.md.tiancode-restore")

    target, written = write_project_guide(work)

    assert written is True
    assert symlink_canary.read_text(encoding="utf-8") == "CANARY-TMP-SYMLINK\n"
    assert hardlink_canary.read_text(encoding="utf-8") == "CANARY-TMP-HARD\n"
    assert os.lstat(hardlink_canary).st_ino == hard_inode
    assert os.lstat(target).st_ino != hard_inode
    assert target.is_file() and not target.is_symlink()
    text = target.read_text(encoding="utf-8")
    assert "开发与协作规范" in text
    assert "CANARY-" not in text
    assert (work / "TIANCODE.md.tmp").is_symlink()
    assert not (work / ".TIANCODE.md.tiancode-restore").exists()


def test_final_symlink_is_not_followed(tmp_path: Path) -> None:
    previous = os.umask(0o022)
    try:
        work = tmp_path / "work"
        work.mkdir()
        canary = tmp_path / "outside.txt"
        canary.write_text("CANARY-FINAL\n", encoding="utf-8")
        link = work / "TIANCODE.md"
        link.symlink_to(canary)

        skipped, wrote = write_project_guide(work)
        assert wrote is False
        assert skipped == link
        assert canary.read_text(encoding="utf-8") == "CANARY-FINAL\n"
        assert link.is_symlink()

        target, wrote = write_project_guide(work, force=True)

        assert wrote is True
        assert target == link
        assert canary.read_text(encoding="utf-8") == "CANARY-FINAL\n"
        assert link.is_file() and not link.is_symlink()
        assert _mode(link) == 0o644
        assert "CANARY-FINAL" not in link.read_text(encoding="utf-8")
        assert "开发与协作规范" in link.read_text(encoding="utf-8")
    finally:
        os.umask(previous)


def test_force_keeps_regular_file_mode_and_failed_write_keeps_original(tmp_path: Path, monkeypatch) -> None:
    work = tmp_path / "work"
    work.mkdir()
    guide = work / "TIANCODE.md"
    guide.write_text("KEEP-GUIDE\n", encoding="utf-8")
    os.chmod(guide, 0o600)
    canary = tmp_path / "outside.txt"
    canary.write_text("CANARY-OUTSIDE\n", encoding="utf-8")
    (work / "TIANCODE.md.tmp").symlink_to(canary)

    target, wrote = write_project_guide(work, force=True)

    assert wrote is True
    assert target == guide
    assert _mode(guide) == 0o600
    assert "开发与协作规范" in guide.read_text(encoding="utf-8")
    assert canary.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"
    guide.write_text("KEEP-GUIDE\n", encoding="utf-8")
    os.chmod(guide, 0o600)

    def boom(_work_dir: Path) -> str:
        return "BAD\ud800"

    monkeypatch.setattr("tiancode.core.project_rules.generate_project_guide", boom)
    with pytest.raises(UnicodeError):
        write_project_guide(work, force=True)

    assert guide.read_text(encoding="utf-8") == "KEEP-GUIDE\n"
    assert _mode(guide) == 0o600
    assert canary.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"
    assert not (work / ".TIANCODE.md.tiancode-restore").exists()


def test_guide_path_outside_workspace_is_rejected(tmp_path: Path) -> None:
    work = tmp_path / "work"
    work.mkdir()
    outside = tmp_path / "outside.md"
    outside.write_text("CANARY-OUTSIDE\n", encoding="utf-8")

    with pytest.raises(PermissionError):
        write_project_guide(work, filename="../outside.md")

    assert outside.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"
    assert not (work / "TIANCODE.md").exists()
