"""口述记忆的行锁、成员隔离、控制字符和 payload 上限。"""
from __future__ import annotations

import threading
import time

import psycopg
import pytest
from fastapi.testclient import TestClient

from app.core.store import MAX_PLUGIN_STATE_CHARS, PluginStateError, database_url, store
from app.main import create_app
from app.plugins.state import scope_key
from app.schemas.auth import UserContext, UserRole
from app.schemas.plugin import MAX_ACTION_PAYLOAD_CHARS


@pytest.fixture
def client():
    return TestClient(create_app())


def _register(client: TestClient, phone: str) -> tuple[dict, dict]:
    resp = client.post(
        "/api/v1/auth/register",
        json={
            "phone": phone,
            "password": "Password123!",
            "family_name": "记忆之家",
            "nickname": "监护人",
            "birth_date": "1990-01-01",
        },
    )
    assert resp.status_code == 201, resp.text
    headers = {"Authorization": f"Bearer {resp.json()['access_token']}"}
    me = client.get("/api/v1/auth/me", headers=headers)
    assert me.status_code == 200, me.text
    return headers, me.json()


def _add_elder(client: TestClient, headers: dict, nickname: str, pin: str) -> dict:
    created = client.post(
        "/api/v1/family/members",
        headers=headers,
        json={"nickname": nickname, "role": "elder", "birth_date": "1949-03-03", "pin_code": pin},
    )
    assert created.status_code == 201, created.text
    return created.json()


def _login(client: TestClient, invite: str, pin: str) -> dict:
    login = client.post("/api/v1/auth/member-login", json={"invite_code": invite, "pin_code": pin})
    assert login.status_code == 200, login.text
    return {"Authorization": f"Bearer {login.json()['access_token']}"}


def _save(client: TestClient, headers: dict, text: str):
    return client.post(
        "/api/v1/plugins/elder_memory/action",
        headers=headers,
        json={"action_name": "save_oral_story", "payload": {"story_text": text}},
    )


def _context(family_id: str, user_id: str) -> UserContext:
    return UserContext(
        user_id=user_id,
        family_id=family_id,
        role=UserRole.ELDER,
        nickname="外婆",
        birth_date="1949-03-03",
        jti="not-used",
        token_exp=0,
    )


def test_scope_key_rejects_missing_or_ambiguous_ids():
    with pytest.raises(PluginStateError):
        scope_key(_context("", "usr_1"))
    with pytest.raises(PluginStateError):
        scope_key(_context("fam_1", "usr:2"))
    with pytest.raises(PluginStateError):
        scope_key(_context("f" * 65, "usr_1"))
    assert scope_key(_context("fam_1", "usr_1")) == "fam_1:usr_1"


def test_two_elders_in_one_family_do_not_share_memories(client):
    headers, me = _register(client, "13700002001")
    _add_elder(client, headers, "外婆", "1919")
    _add_elder(client, headers, "外公", "2929")
    grandma = _login(client, me["invite_code"], "1919")
    grandpa = _login(client, me["invite_code"], "2929")

    saved = _save(client, grandma, "院子里的桂花")
    assert saved.status_code == 200, saved.text

    grandma_cards = client.get("/api/v1/plugins/active", headers=grandma).json()
    grandpa_cards = client.get("/api/v1/plugins/active", headers=grandpa).json()
    grandma_memory = next(card for card in grandma_cards if card["plugin_id"] == "elder_memory")
    grandpa_memory = next(card for card in grandpa_cards if card["plugin_id"] == "elder_memory")
    assert grandma_memory["data"]["latest_memory"] == "院子里的桂花"
    assert grandpa_memory["data"]["recorded_memories_count"] == 0
    assert "桂花" not in grandpa_memory["data"]["latest_memory"]


def test_control_character_and_oversized_payload_are_rejected(client):
    headers, me = _register(client, "13700002002")
    _add_elder(client, headers, "外婆", "3939")
    elder = _login(client, me["invite_code"], "3939")

    nulled = _save(client, elder, "有一段\x00空白")
    assert nulled.status_code == 400
    assert "控制字符" in nulled.json()["detail"]

    huge = "记" * (MAX_ACTION_PAYLOAD_CHARS + 1)
    oversized = client.post(
        "/api/v1/plugins/elder_memory/action",
        headers=elder,
        json={"action_name": "save_oral_story", "payload": {"story_text": huge}},
    )
    assert oversized.status_code == 422
    assert "payload" in oversized.text

    kept = _save(client, elder, "正常的一句")
    assert kept.status_code == 200, kept.text


def test_failed_or_oversized_update_does_not_leave_a_row():
    scope = "fam_rollback:usr_rollback"

    def reject(_current):
        raise PluginStateError("停在这里")

    with pytest.raises(PluginStateError, match="停在这里"):
        store.update_plugin_state("elder_memory", scope, reject)
    assert store.get_plugin_state("elder_memory", scope) is None

    def huge(_current):
        return {"blob": "x" * (MAX_PLUGIN_STATE_CHARS + 1)}

    with pytest.raises(PluginStateError, match="保存上限"):
        store.update_plugin_state("elder_memory", scope, huge)
    assert store.get_plugin_state("elder_memory", scope) is None


def test_append_waits_for_the_row_lock_and_keeps_both_lines():
    scope = "fam_lock:usr_lock"
    store.update_plugin_state("elder_memory", scope, lambda _current: {"memories": ["先写"]})

    holder = psycopg.connect(database_url())
    started = threading.Event()
    finished = threading.Event()
    result: dict = {}
    worker: threading.Thread | None = None
    try:
        locked = holder.execute(
            "SELECT state_json FROM plugin_state WHERE plugin_id = %s AND scope_key = %s FOR UPDATE",
            ("elder_memory", scope),
        ).fetchone()
        assert locked is not None

        def append_after_lock():
            started.set()

            def add(current):
                memories = list(current.get("memories") or [])
                memories.append("追加")
                return {"memories": memories}

            result["state"] = store.update_plugin_state("elder_memory", scope, add)
            finished.set()

        worker = threading.Thread(target=append_after_lock)
        worker.start()
        assert started.wait(2)

        waiting = 0
        deadline = time.time() + 3
        while time.time() < deadline:
            row = holder.execute("SELECT COUNT(*) FROM pg_locks WHERE NOT granted").fetchone()
            waiting = int(row[0]) if row else 0
            if waiting:
                break
            time.sleep(0.05)
        assert waiting >= 1
        assert not finished.is_set()

        holder.execute(
            "UPDATE plugin_state SET state_json = %s WHERE plugin_id = %s AND scope_key = %s",
            ('{"memories":["先写","外部"]}', "elder_memory", scope),
        )
        holder.commit()
        assert finished.wait(3)
    finally:
        if not finished.is_set():
            try:
                holder.rollback()
            except Exception:
                pass
        holder.close()
        if worker is not None:
            worker.join(timeout=3)

    assert result["state"]["memories"] == ["先写", "外部", "追加"]
