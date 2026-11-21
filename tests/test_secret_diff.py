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


def _rename(repo: Path, source: str, dest: str) -> str:
    before = _git(repo, "rev-parse", "HEAD").stdout.strip()
    (repo / source).rename(repo / dest)
    return before


def _tree(repo: Path) -> str:
    return _git(repo, "ls-tree", "-r", "--name-only", "HEAD").stdout


def test_sensitive_to_normal_rename_is_not_split(tmp_path: Path) -> None:
    work = tmp_path / "repo"
    work.mkdir()
    _init_repo(work)
    (work / ".env").write_text("CANARY-OUTSIDE\n", encoding="utf-8")
    (work / "other.txt").write_text("OLD\n", encoding="utf-8")
    _git(work, "add", ".env", "other.txt")
    _git(work, "commit", "-m", "seed")
    _rename(work, ".env", "notes-renamed.txt")
    (work / "other.txt").write_text("KEEP-INSIDE\n", encoding="utf-8")
    store = GitSnapshotStore(work)

    blob = "\n".join(item.diff_text for item in store.compute_diffs())
    commit = store.commit("keep the normal edit")

    assert "KEEP-INSIDE" in blob
    assert "CANARY-OUTSIDE" not in blob
    assert commit
    tracked = _tree(work)
    assert "other.txt" in tracked.splitlines()
    assert ".env" in tracked.splitlines()
    assert "notes-renamed.txt" not in tracked.splitlines()
    assert _git(work, "show", "HEAD:.env").stdout == "CANARY-OUTSIDE\n"
    assert not (work / ".env").exists()
    assert (work / "notes-renamed.txt").read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"
    assert (work / "other.txt").read_text(encoding="utf-8") == "KEEP-INSIDE\n"


def test_both_sensitive_rename_commits_nothing(tmp_path: Path) -> None:
    work = tmp_path / "repo"
    work.mkdir()
    _init_repo(work)
    (work / ".env").write_text("CANARY-OUTSIDE\n", encoding="utf-8")
    _git(work, "add", ".env")
    _git(work, "commit", "-m", "seed")
    before = _rename(work, ".env", ".env.local")
    store = GitSnapshotStore(work)

    blob = "\n".join(item.diff_text for item in store.compute_diffs())
    commit = store.commit("should not record the rename")

    assert "CANARY-OUTSIDE" not in blob
    assert commit is None
    assert _git(work, "rev-parse", "HEAD").stdout.strip() == before
    assert _tree(work).splitlines() == [".env"]
    assert _git(work, "show", "HEAD:.env").stdout == "CANARY-OUTSIDE\n"
    assert not (work / ".env").exists()
    assert (work / ".env.local").read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"


def test_normal_to_sensitive_rename_keeps_old_blob(tmp_path: Path) -> None:
    work = tmp_path / "repo"
    work.mkdir()
    _init_repo(work)
    (work / "keep.txt").write_text("CANARY-OUTSIDE\n", encoding="utf-8")
    _git(work, "add", "keep.txt")
    _git(work, "commit", "-m", "seed")
    before = _rename(work, "keep.txt", ".env.backup")
    store = GitSnapshotStore(work)

    blob = "\n".join(item.diff_text for item in store.compute_diffs())
    commit = store.commit("should not drop the old file")

    assert "CANARY-OUTSIDE" not in blob
    assert commit is None
    assert _git(work, "rev-parse", "HEAD").stdout.strip() == before
    assert _tree(work).splitlines() == ["keep.txt"]
    assert _git(work, "show", "HEAD:keep.txt").stdout == "CANARY-OUTSIDE\n"
    assert not (work / "keep.txt").exists()
    assert (work / ".env.backup").read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"


def test_normal_rename_still_commits(tmp_path: Path) -> None:
    work = tmp_path / "repo"
    work.mkdir()
    _init_repo(work)
    (work / "keep.txt").write_text("KEEP-INSIDE\n", encoding="utf-8")
    _git(work, "add", "keep.txt")
    _git(work, "commit", "-m", "seed")
    (work / "keep.txt").rename(work / "renamed.txt")
    store = GitSnapshotStore(work)

    blob = "\n".join(item.diff_text for item in store.compute_diffs())
    commit = store.commit("rename a normal file")

    assert "KEEP-INSIDE" in blob
    assert commit
    assert _tree(work).splitlines() == ["renamed.txt"]
    assert _git(work, "show", "HEAD:renamed.txt").stdout == "KEEP-INSIDE\n"
    assert (work / "renamed.txt").read_text(encoding="utf-8") == "KEEP-INSIDE\n"


def test_only_secret_rename_does_not_ask_for_a_commit_message(tmp_path: Path) -> None:
    from tiancode.cli.commands import SlashCommandHandler

    work = tmp_path / "repo"
    work.mkdir()
    _init_repo(work)
    (work / ".env").write_text("CANARY-OUTSIDE\n", encoding="utf-8")
    _git(work, "add", ".env")
    _git(work, "commit", "-m", "seed")
    before = _rename(work, ".env", "notes-renamed.txt")

    class _Engine:
        def __init__(self) -> None:
            self.snapshot_store = GitSnapshotStore(work)
            self.asked = False

        def generate_commit_message(self, task_description: str, diff_text: str) -> str:
            self.asked = True
            return "test: should not run"

    engine = _Engine()
    SlashCommandHandler(engine)._handle_commit("")

    assert engine.asked is False
    assert "CANARY-OUTSIDE" not in "\n".join(item.diff_text for item in engine.snapshot_store.compute_diffs())
    assert _git(work, "rev-parse", "HEAD").stdout.strip() == before
    assert (work / "notes-renamed.txt").read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"


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
