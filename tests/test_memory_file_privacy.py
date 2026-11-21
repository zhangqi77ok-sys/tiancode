"""Memory saves stay private and do not follow a symlink or hard link.

The note and canaries are local fakes. The test does not use the network or a credential.
"""

from __future__ import annotations

import json
import os
import stat
from pathlib import Path

from tiancode.core.cognitive_memory import CognitiveMemoryStore


def _mode(path: Path) -> int:
    return stat.S_IMODE(os.lstat(path).st_mode)


def _canary_fact(text: str) -> str:
    payload = {
        "workspaces": {},
        "episodes": [],
        "facts": [
            {
                "fact_id": "fact_canary",
                "key": "leak",
                "content": text,
                "scope": "global",
                "workspace_path": "",
                "source_session_id": "",
                "importance": 0.8,
                "updated_at": "2020-01-01T00:00:00+00:00",
                "access_count": 1,
            }
        ],
    }
    return json.dumps(payload, indent=2) + "\n"


def test_saved_memory_is_owner_rw_under_open_umasks(tmp_path: Path) -> None:
    for umask in (0o000, 0o022):
        directory = tmp_path / f"umask-{umask:03o}"
        directory.mkdir()
        path = directory / "brain_memory.json"
        previous = os.umask(umask)
        try:
            store = CognitiveMemoryStore(memory_path=path)
            store.consolidate_session(
                "sess",
                directory,
                [{"type": "human", "data": {"content": "SECRET-NOTE"}}],
            )
        finally:
            os.umask(previous)

        assert path.is_file() and not path.is_symlink()
        assert _mode(path) == 0o600
        assert "SECRET-NOTE" in path.read_text(encoding="utf-8")
        assert list(directory.glob("*.tmp")) == []


def test_existing_memory_mode_is_tightened_not_widened(tmp_path: Path) -> None:
    previous = os.umask(0o022)
    try:
        loose = tmp_path / "loose.json"
        loose.write_text('{"workspaces": {}, "episodes": [], "facts": []}\n', encoding="utf-8")
        os.chmod(loose, 0o644)
        private = tmp_path / "private.json"
        private.write_text('{"workspaces": {}, "episodes": [], "facts": []}\n', encoding="utf-8")
        os.chmod(private, 0o600)

        CognitiveMemoryStore(memory_path=loose).upsert_fact("SECRET-NOTE")
        CognitiveMemoryStore(memory_path=private).upsert_fact("SECRET-NOTE")

        assert _mode(loose) == 0o600
        assert _mode(private) == 0o600
        assert "SECRET-NOTE" in loose.read_text(encoding="utf-8")
        assert "SECRET-NOTE" in private.read_text(encoding="utf-8")
    finally:
        os.umask(previous)


def test_memory_symlink_is_not_followed_or_loaded(tmp_path: Path, monkeypatch) -> None:
    outside = tmp_path / "outside.json"
    original = _canary_fact("CANARY-OUTSIDE")
    outside.write_text(original, encoding="utf-8")
    link = tmp_path / "brain_memory.json"
    link.symlink_to(outside)
    monkeypatch.setenv("TIANCODE_MEMORY_PATH", str(link))

    store = CognitiveMemoryStore()
    assert store.facts == []
    store.upsert_fact("SECRET-NOTE")

    saved = store.memory_path
    assert outside.read_text(encoding="utf-8") == original
    assert link.is_file() and not link.is_symlink()
    assert saved.is_file() and not saved.is_symlink()
    assert _mode(saved) == 0o600
    text = saved.read_text(encoding="utf-8")
    assert "SECRET-NOTE" in text
    assert "CANARY-OUTSIDE" not in text
    assert not saved.with_suffix(".json.tmp").exists()


def test_temp_symlink_and_hardlinks_do_not_change_canaries(tmp_path: Path) -> None:
    previous = os.umask(0o022)
    try:
        outside = tmp_path / "tmp-symlink.txt"
        outside.write_text("CANARY-TMP-SYMLINK\n", encoding="utf-8")
        temp_hard = tmp_path / "tmp-hard.txt"
        temp_hard.write_text("CANARY-TMP-HARD\n", encoding="utf-8")
        dest_hard = tmp_path / "dest-hard.txt"
        dest_hard.write_text("CANARY-DEST-HARD\n", encoding="utf-8")
        dest_inode = os.lstat(dest_hard).st_ino
        temp_inode = os.lstat(temp_hard).st_ino

        memory = tmp_path / "brain_memory.json"
        os.link(dest_hard, memory)
        memory.with_suffix(".json.tmp").symlink_to(outside)
        # A second save uses a new temp name; cover that hard link on its own file.
        other = tmp_path / "other_memory.json"
        other.write_text('{"workspaces": {}, "episodes": [], "facts": []}\n', encoding="utf-8")
        os.link(temp_hard, other.with_suffix(".json.tmp"))

        CognitiveMemoryStore(memory_path=memory).upsert_fact("SECRET-NOTE")
        CognitiveMemoryStore(memory_path=other).upsert_fact("SECRET-NOTE")

        assert outside.read_text(encoding="utf-8") == "CANARY-TMP-SYMLINK\n"
        assert temp_hard.read_text(encoding="utf-8") == "CANARY-TMP-HARD\n"
        assert os.lstat(temp_hard).st_ino == temp_inode
        assert dest_hard.read_text(encoding="utf-8") == "CANARY-DEST-HARD\n"
        assert os.lstat(dest_hard).st_ino == dest_inode
        assert os.lstat(memory).st_ino != dest_inode
        assert os.lstat(other).st_ino != temp_inode
        for saved in (memory, other):
            assert saved.is_file() and not saved.is_symlink()
            assert _mode(saved) == 0o600
            text = saved.read_text(encoding="utf-8")
            assert "SECRET-NOTE" in text
            assert "CANARY-" not in text
            assert not saved.with_suffix(".json.tmp").exists()
    finally:
        os.umask(previous)


def test_failed_save_keeps_memory_and_outside_canary(tmp_path: Path, monkeypatch) -> None:
    original = '{"workspaces": {}, "episodes": [], "facts": []}\n'
    path = tmp_path / "brain_memory.json"
    path.write_text(original, encoding="utf-8")
    os.chmod(path, 0o600)
    outside = tmp_path / "outside.txt"
    outside.write_text("CANARY-OUTSIDE\n", encoding="utf-8")
    path.with_suffix(".json.tmp").symlink_to(outside)

    def boom(*_args, **_kwargs) -> None:
        raise TypeError("simulated memory encode failure")

    monkeypatch.setattr("tiancode.core.cognitive_memory.json.dump", boom)
    store = CognitiveMemoryStore(memory_path=path)

    try:
        store.upsert_fact("SECRET-NOTE")
    except TypeError:
        pass
    else:
        raise AssertionError("save should fail when encoding fails")

    assert path.read_text(encoding="utf-8") == original
    assert _mode(path) == 0o600
    assert outside.read_text(encoding="utf-8") == "CANARY-OUTSIDE\n"
    assert not path.with_suffix(".json.tmp").exists()
