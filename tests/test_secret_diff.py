"""Credential files stay out of diffs and out of commits.

The canary is local fake text. The test does not use a credential or the network.
"""

from __future__ import annotations

import subprocess
from pathlib import Path

from tiancode.core.git_store import GitSnapshotStore
from tiancode.core.history import InMemorySnapshotStore


def _git(repo: Path, *args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(["git", *args], cwd=repo, check=True, capture_output=True, text=True)


def _init_repo(repo: Path) -> None:
    _git(repo, "init")
    _git(repo, "config", "user.email", "tiancode-test@example.com")
    _git(repo, "config", "user.name", "tiancode-test")


def test_untracked_secret_is_hidden_and_not_committed(tmp_path: Path) -> None:
    work = tmp_path / "repo"
    work.mkdir()
    _init_repo(work)
    (work / ".env").write_text("CANARY-OUTSIDE\n", encoding="utf-8")
    (work / "id_rsa").write_text("CANARY-KEY\n", encoding="utf-8")
    (work / "keep.txt").write_text("KEEP-INSIDE\n", encoding="utf-8")
    store = GitSnapshotStore(work)

    blob = "\n".join(item.diff_text for item in store.compute_diffs())
    commit = store.commit("keep the normal file")
    tracked = _git(work, "ls-tree", "-r", "--name-only", "HEAD").stdout

    assert "KEEP-INSIDE" in blob
    assert "CANARY-OUTSIDE" not in blob
    assert "CANARY-KEY" not in blob
    assert "未读取内容" in blob
    assert commit
    assert "keep.txt" in tracked.splitlines()
    assert ".env" not in tracked.splitlines()
    assert "id_rsa" not in tracked.splitlines()
    assert (work / ".env").read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"
    assert (work / "id_rsa").read_text(encoding="utf-8") == "CANARY-KEY\n"


def test_tracked_secret_diff_hides_the_new_body(tmp_path: Path) -> None:
    work = tmp_path / "repo"
    work.mkdir()
    _init_repo(work)
    (work / ".env").write_text("OLD\n", encoding="utf-8")
    (work / "keep.txt").write_text("OLD\n", encoding="utf-8")
    _git(work, "add", ".env", "keep.txt")
    _git(work, "commit", "-m", "seed")
    (work / ".env").write_text("CANARY-OUTSIDE\n", encoding="utf-8")
    (work / "keep.txt").write_text("KEEP-INSIDE\n", encoding="utf-8")

    blob = "\n".join(item.diff_text for item in GitSnapshotStore(work).compute_diffs())

    assert "KEEP-INSIDE" in blob
    assert "CANARY-OUTSIDE" not in blob
    assert (work / ".env").read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"


def test_memory_diff_does_not_replay_a_secret_snapshot(tmp_path: Path) -> None:
    work = tmp_path / "work"
    work.mkdir()
    (work / "keep.txt").write_text("KEEP-INSIDE\n", encoding="utf-8")
    store = InMemorySnapshotStore(work)
    store.record_snapshot(str(work / ".env"), "CANARY-OUTSIDE\n", is_new=False)
    store.record_snapshot(str(work / "keep.txt"), "OLD\n", is_new=False)

    blob = "\n".join(item.diff_text for item in store.compute_diffs())

    assert "KEEP-INSIDE" in blob
    assert "CANARY-OUTSIDE" not in blob
    assert "未读取内容" in blob
