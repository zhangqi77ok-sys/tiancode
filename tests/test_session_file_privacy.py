"""Session saves stay private and do not follow a temp-name symlink.

The canary is a local fake. The test does not use the network or a credential.
"""

from __future__ import annotations

import os
import stat
from pathlib import Path

from tiancode.core.session_store import SessionStore


def _mode(path: Path) -> int:
    return stat.S_IMODE(os.lstat(path).st_mode)


def test_saved_session_is_owner_rw_even_with_permissive_umask(tmp_path: Path) -> None:
    previous = os.umask(0o000)
    try:
        store = SessionStore(storage_dir=tmp_path / "sessions")
        store.save("s1", str(tmp_path), [{"type": "human", "data": {"content": "KEEP-NOTE"}}])
        saved = tmp_path / "sessions" / "s1.json"
        loaded = store.load("s1")

        assert saved.is_file() and not saved.is_symlink()
        assert _mode(saved) == 0o600
        assert loaded is not None
        assert loaded.messages[0]["data"]["content"] == "KEEP-NOTE"
        assert list((tmp_path / "sessions").glob("*.tmp")) == []
    finally:
        os.umask(previous)


def test_temp_symlink_does_not_receive_session_body(tmp_path: Path) -> None:
    previous = os.umask(0o022)
    try:
        sessions = tmp_path / "sessions"
        sessions.mkdir()
        outside = tmp_path / "outside.txt"
        outside.write_text("CANARY-OUTSIDE\n", encoding="utf-8")
        (sessions / "s2.json.tmp").symlink_to(outside)
        store = SessionStore(storage_dir=sessions)

        store.save("s2", str(tmp_path), [{"type": "human", "data": {"content": "KEEP-NOTE"}}])

        saved = sessions / "s2.json"
        assert outside.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"
        assert saved.is_file() and not saved.is_symlink()
        assert _mode(saved) == 0o600
        assert "KEEP-NOTE" in saved.read_text(encoding="utf-8")
        assert "CANARY-OUTSIDE" not in saved.read_text(encoding="utf-8")
        assert not (sessions / "s2.json.tmp").exists()
    finally:
        os.umask(previous)


def test_failed_save_removes_temp_and_leaves_outside_file(tmp_path: Path, monkeypatch) -> None:
    sessions = tmp_path / "sessions"
    sessions.mkdir()
    outside = tmp_path / "outside.txt"
    outside.write_text("CANARY-OUTSIDE\n", encoding="utf-8")
    (sessions / "s3.json.tmp").symlink_to(outside)

    def boom(*_args, **_kwargs) -> None:
        raise OSError("simulated write failure")

    monkeypatch.setattr("tiancode.core.session_store.json.dump", boom)
    store = SessionStore(storage_dir=sessions)

    try:
        store.save("s3", str(tmp_path), [{"type": "human", "data": {"content": "KEEP-NOTE"}}])
    except IOError:
        pass
    else:
        raise AssertionError("save should fail when the JSON write fails")

    assert outside.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"
    assert not (sessions / "s3.json").exists()
    assert not (sessions / "s3.json.tmp").exists()
