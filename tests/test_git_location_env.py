"""TianCode git commands stay in the work tree when GIT_DIR points elsewhere.

The canary is local fake text. The test does not use a credential or the network.
"""

from __future__ import annotations

import os
import subprocess
from pathlib import Path

from tiancode.core.doctor import _check_git_repo
from tiancode.core.git_store import GitSnapshotStore


def _git(repo: Path, *args: str) -> subprocess.CompletedProcess[str]:
    env = os.environ.copy()
    for name in ("GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"):
        env.pop(name, None)
    return subprocess.run(
        ["git", *args],
        cwd=repo,
        env=env,
        check=True,
        capture_output=True,
        text=True,
    )


def _init(repo: Path) -> None:
    repo.mkdir()
    _git(repo, "init")
    _git(repo, "config", "user.email", "tiancode-test@example.com")
    _git(repo, "config", "user.name", "tiancode-test")


def test_other_git_dir_does_not_enter_this_worktree(tmp_path: Path, monkeypatch) -> None:
    other = tmp_path / "other"
    work = tmp_path / "work"
    _init(other)
    _init(work)
    (other / "notes.txt").write_text("CANARY-OTHER\n", encoding="utf-8")
    (work / "notes.txt").write_text("KEEP-HERE\n", encoding="utf-8")
    _git(other, "add", "notes.txt")
    _git(work, "add", "notes.txt")
    _git(other, "commit", "-m", "other")
    _git(work, "commit", "-m", "work")
    (other / "notes.txt").write_text("CANARY-OTHER-CHANGED\n", encoding="utf-8")
    (work / "local.txt").write_text("KEEP-LOCAL\n", encoding="utf-8")
    other_head = _git(other, "rev-parse", "HEAD").stdout.strip()

    monkeypatch.setenv("GIT_DIR", str(other / ".git"))
    monkeypatch.setenv("GIT_WORK_TREE", str(other))
    monkeypatch.setenv("GIT_INDEX_FILE", str(tmp_path / "elsewhere-index"))
    monkeypatch.setenv("GIT_CONFIG_COUNT", "1")
    monkeypatch.setenv("GIT_CONFIG_KEY_0", "core.abbrev")
    monkeypatch.setenv("GIT_CONFIG_VALUE_0", "7")
    for name, value in (
        ("GIT_AUTHOR_NAME", "tiancode-test"),
        ("GIT_AUTHOR_EMAIL", "tiancode-test@example.com"),
        ("GIT_COMMITTER_NAME", "tiancode-test"),
        ("GIT_COMMITTER_EMAIL", "tiancode-test@example.com"),
    ):
        monkeypatch.setenv(name, value)

    store = GitSnapshotStore(work)
    assert store.is_git_repo() is True
    blob = "\n".join(item.diff_text for item in store.compute_diffs())
    assert "KEEP-LOCAL" in blob
    assert "CANARY-OTHER" not in blob
    assert _check_git_repo(work).status == "OK"

    commit = store.commit("keep the local file")
    assert commit
    assert (other / "notes.txt").read_text(encoding="utf-8") == "CANARY-OTHER-CHANGED\n"
    assert (work / "notes.txt").read_text(encoding="utf-8") == "KEEP-HERE\n"
    assert (work / "local.txt").read_text(encoding="utf-8") == "KEEP-LOCAL\n"
    assert _git(other, "rev-parse", "HEAD").stdout.strip() == other_head
    assert "local.txt" in _git(work, "ls-tree", "-r", "--name-only", "HEAD").stdout
