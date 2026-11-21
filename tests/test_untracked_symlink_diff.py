"""Untracked diffs must not follow a symlink out of the git workspace.

The canary is a fake local file. The test does not use the network or a credential.
"""

from __future__ import annotations

import subprocess
from pathlib import Path

from tiancode.core.git_store import GitSnapshotStore


def _init_repo(path: Path) -> None:
    subprocess.run(["git", "init"], cwd=path, check=True, capture_output=True)


def _diff_blob(store: GitSnapshotStore) -> str:
    return "\n".join(item.diff_text for item in store.compute_diffs())


def test_untracked_symlink_diff_does_not_read_outside_file(tmp_path: Path) -> None:
    work = tmp_path / "repo"
    outside = tmp_path / "outside"
    work.mkdir()
    outside.mkdir()
    canary = outside / "secret.txt"
    canary.write_text("CANARY-OUTSIDE-SECRET\n", encoding="utf-8")
    _init_repo(work)
    (work / "keep.txt").write_text("KEEP-INSIDE\n", encoding="utf-8")
    (work / "notes.txt").symlink_to(canary)

    blob = _diff_blob(GitSnapshotStore(work))

    assert "KEEP-INSIDE" in blob
    assert "CANARY-OUTSIDE-SECRET" not in blob
    assert "符号链接，未读取目标" in blob
    assert canary.read_text(encoding="utf-8") == "CANARY-OUTSIDE-SECRET\n"


def test_untracked_directory_symlink_does_not_inline_children(tmp_path: Path) -> None:
    work = tmp_path / "repo"
    outside = tmp_path / "outside"
    work.mkdir()
    outside.mkdir()
    canary = outside / "secret.txt"
    canary.write_text("CANARY-DIR-CHILD\n", encoding="utf-8")
    _init_repo(work)
    (work / "escape").symlink_to(outside, target_is_directory=True)

    blob = _diff_blob(GitSnapshotStore(work))

    assert "CANARY-DIR-CHILD" not in blob
    assert canary.read_text(encoding="utf-8") == "CANARY-DIR-CHILD\n"
