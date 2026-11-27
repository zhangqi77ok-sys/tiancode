"""Session and memory documents carry a schema version and refuse a downgrade overwrite."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from tiancode.core.cognitive_memory import MEMORY_SCHEMA_VERSION, CognitiveMemoryStore
from tiancode.core.session_store import SESSION_SCHEMA_VERSION, SessionStore, migrate_session_payload


def test_v0_session_loads_and_next_save_stamps_schema(tmp_path: Path) -> None:
    store = SessionStore(storage_dir=tmp_path / "sessions")
    path = tmp_path / "sessions" / "legacy.json"
    path.write_text(
        json.dumps(
            {
                "metadata": {
                    "session_id": "legacy",
                    "work_dir": str(tmp_path),
                    "created_at": "t0",
                    "updated_at": "t1",
                    "message_count": 1,
                },
                "messages": [{"type": "human", "data": {"content": "old"}}],
                "plan": None,
                "snapshots": [],
            }
        ),
        encoding="utf-8",
    )

    loaded = store.load("legacy")
    assert loaded is not None
    assert loaded.messages[0]["data"]["content"] == "old"
    assert store.last_load_error is None

    store.save("legacy", str(tmp_path), loaded.messages, plan=None, snapshots=[])
    stamped = json.loads(path.read_text(encoding="utf-8"))
    assert stamped["schema_version"] == SESSION_SCHEMA_VERSION
    assert stamped["metadata"]["created_at"] == "t0"


def test_future_session_is_not_loaded_or_overwritten(tmp_path: Path) -> None:
    store = SessionStore(storage_dir=tmp_path / "sessions")
    path = tmp_path / "sessions" / "future.json"
    original = {
        "schema_version": SESSION_SCHEMA_VERSION + 5,
        "metadata": {"session_id": "future", "work_dir": str(tmp_path), "created_at": "t0", "updated_at": "t1"},
        "messages": [{"type": "human", "data": {"content": "KEEP"}}],
        "extra_future_field": {"keep": True},
    }
    path.write_text(json.dumps(original), encoding="utf-8")

    assert store.load("future") is None
    assert store.last_load_error is not None
    assert store.last_load_error.kind == "unsupported"
    with pytest.raises(OSError, match="已拒绝覆盖"):
        store.save("future", str(tmp_path), [{"type": "human", "data": {"content": "NEW"}}])
    assert json.loads(path.read_text(encoding="utf-8"))["extra_future_field"] == {"keep": True}


def test_corrupt_session_is_distinguished_from_missing(tmp_path: Path) -> None:
    store = SessionStore(storage_dir=tmp_path / "sessions")
    (tmp_path / "sessions" / "broken.json").write_text("{", encoding="utf-8")
    assert store.load("broken") is None
    assert store.last_load_error is not None
    assert store.last_load_error.kind == "corrupt"
    assert store.load("missing") is None
    assert store.last_load_error is not None
    assert store.last_load_error.kind == "not_found"
    with pytest.raises(Exception):
        migrate_session_payload(["not", "an", "object"])


def test_v0_memory_loads_and_future_schema_is_not_rewritten(tmp_path: Path) -> None:
    legacy = tmp_path / "memory.json"
    legacy.write_text(
        json.dumps(
            {
                "workspaces": {},
                "episodes": [],
                "facts": [{
                    "fact_id": "f1",
                    "key": "style",
                    "content": "KEEP-FACT",
                    "scope": "global",
                    "workspace_path": "",
                    "source_session_id": "",
                    "importance": 0.8,
                    "updated_at": "t0",
                    "access_count": 1,
                    "valid_from": "t0",
                    "invalid_at": "",
                    "superseded_by": "",
                }],
            }
        ),
        encoding="utf-8",
    )
    store = CognitiveMemoryStore(memory_path=legacy)
    assert store.facts[0].content == "KEEP-FACT"
    assert store._persist_blocked is False
    store.touch_workspace(tmp_path, session_id="s1")
    stamped = json.loads(legacy.read_text(encoding="utf-8"))
    assert stamped["schema_version"] == MEMORY_SCHEMA_VERSION
    assert stamped["facts"][0]["content"] == "KEEP-FACT"

    future = tmp_path / "future.json"
    future_payload = {"schema_version": MEMORY_SCHEMA_VERSION + 3, "facts": [{"content": "DO-NOT-TOUCH"}], "marker": "v-next"}
    future.write_text(json.dumps(future_payload), encoding="utf-8")
    blocked = CognitiveMemoryStore(memory_path=future)
    assert blocked.facts == []
    assert blocked._persist_blocked is True
    blocked.touch_workspace(tmp_path, session_id="s2")
    assert json.loads(future.read_text(encoding="utf-8")) == future_payload
