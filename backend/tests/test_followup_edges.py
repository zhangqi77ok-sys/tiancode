"""后续改动的边界：会话头、行锁、家庭隔离、迁移校验和、OpenAPI 与页面文案。"""
from __future__ import annotations

import json
import threading
import time
from datetime import date
from pathlib import Path

import psycopg
import pytest
from fastapi.testclient import TestClient

from app.core.schema_guard import assert_additive_schema, schema_statements
from app.core.store import SCHEMA_SQL, PluginStateError, Store, database_url, store
from app.main import create_app
from app.plugins.state import scope_key
from app.schemas.auth import UserContext, UserRole

ROOT = Path(__file__).resolve().parents[2]


@pytest.fixture
def client():
    return TestClient(create_app())


def _register(client: TestClient, phone: str, nickname: str = "女儿小敏") -> dict:
    resp = client.post(
        "/api/v1/auth/register",
        json={
            "phone": phone,
            "password": "Password123!",
            "family_name": "常伴一家人",
            "nickname": nickname,
            "birth_date": "1992-05-15",
        },
    )
    assert resp.status_code == 201, resp.text
    return resp.json()


def _add_elder(client: TestClient, headers: dict, nickname: str, pin: str) -> dict:
    resp = client.post(
        "/api/v1/family/members",
        headers=headers,
        json={
            "nickname": nickname,
            "role": "elder",
            "birth_date": date(date.today().year - 70, 1, 1).isoformat(),
            "pin_code": pin,
        },
    )
    assert resp.status_code == 201, resp.text
    return resp.json()


def test_mismatched_session_headers_are_rejected(client):
    registered = _register(client, "13900002001")
    token = registered["access_token"]
    mismatch = client.get(
        "/api/v1/auth/me",
        headers={"satoken": token, "Authorization": "Bearer not-the-same-token"},
    )
    assert mismatch.status_code == 401
    assert mismatch.json()["detail"] == "satoken 与 Authorization 不一致"

    same = client.get(
        "/api/v1/auth/me",
        headers={"satoken": token, "Authorization": f"Bearer {token}"},
    )
    assert same.status_code == 200
    assert same.json()["nickname"] == "女儿小敏"

    lower = client.get("/api/v1/auth/me", headers={"Authorization": f"bearer {token}"})
    assert lower.status_code == 200


def test_bad_token_detail_does_not_leak_library_text(client):
    resp = client.get("/api/v1/auth/me", headers={"Authorization": "Bearer bad.token.here"})
    assert resp.status_code == 401
    assert resp.json()["detail"] == "无效或已过期的访问令牌"
    assert "Signature" not in resp.text

    spaced = client.get("/api/v1/auth/me", headers={"Authorization": "Bearer bad token"})
    assert spaced.status_code == 401
    assert spaced.json()["detail"] == "无效或已过期的访问令牌"


def test_family_change_invalidates_token_and_nickname_stays_on_same_token(client):
    registered = _register(client, "13900002002")
    token = registered["access_token"]
    headers = {"Authorization": f"Bearer {token}"}
    elder = _add_elder(client, headers, "外婆", "8888")
    invite = client.get("/api/v1/auth/me", headers=headers).json()["invite_code"]
    elder_login = client.post("/api/v1/auth/member-login", json={"invite_code": invite, "pin_code": "8888"})
    assert elder_login.status_code == 200
    elder_token = elder_login.json()["access_token"]
    elder_headers = {"Authorization": f"Bearer {elder_token}"}

    renamed = client.put(
        f"/api/v1/family/members/{elder['id']}",
        headers=headers,
        json={"nickname": "奶奶"},
    )
    assert renamed.status_code == 200
    me = client.get("/api/v1/auth/me", headers=elder_headers)
    assert me.status_code == 200
    assert me.json()["nickname"] == "奶奶"
    assert "settings" not in me.json()

    user = store.get_user(registered["user_id"])
    assert user is not None
    user["family_id"] = "fam_moved_away"
    store.upsert_user(user)
    moved = client.get("/api/v1/auth/me", headers=headers)
    assert moved.status_code == 401
    assert moved.json()["detail"] == "无效或已过期的访问令牌"


def test_schema_stays_additive_and_records_checksum():
    with pytest.raises(RuntimeError, match="schema.sql must stay additive"):
        assert_additive_schema(["DROP TABLE users"])
    statements = schema_statements(SCHEMA_SQL)
    assert_additive_schema(statements)
    assert any(item.startswith("CREATE TABLE IF NOT EXISTS schema_migrations") for item in statements)

    store.ensure_schema()
    row = store._run(
        lambda conn: conn.execute(
            "SELECT version, checksum FROM schema_migrations WHERE version = %s",
            ("schema.sql",),
        ).fetchone()
    )
    assert row is not None
    assert row["version"] == "schema.sql"
    assert len(row["checksum"]) == 64


def test_row_lock_waits_until_external_writer_commits():
    plugin_id = "elder_memory"
    scope = "fam_lock:usr_lock"
    store.update_plugin_state(plugin_id, scope, lambda _current: {"memories": ["旧"]})

    holder = psycopg.connect(database_url())
    finished = threading.Event()
    error: list[BaseException] = []

    def waiter() -> None:
        try:
            def append(state: dict) -> dict:
                memories = list(state.get("memories") or [])
                memories.append("追加")
                return {"memories": memories}

            store.update_plugin_state(plugin_id, scope, append)
        except BaseException as exc:
            error.append(exc)
        finally:
            finished.set()

    thread = threading.Thread(target=waiter)
    try:
        locked = holder.execute(
            "SELECT state_json FROM plugin_state WHERE plugin_id = %s AND scope_key = %s FOR UPDATE",
            (plugin_id, scope),
        ).fetchone()
        assert locked is not None
        thread.start()
        waiting = 0
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            waiting = holder.execute("SELECT COUNT(*) FROM pg_locks WHERE NOT granted").fetchone()[0]
            if waiting >= 1 and not finished.is_set():
                break
            time.sleep(0.05)
        assert waiting >= 1
        assert not finished.is_set()
        holder.execute(
            "UPDATE plugin_state SET state_json = %s WHERE plugin_id = %s AND scope_key = %s",
            (json.dumps({"memories": ["先写", "外部"]}, ensure_ascii=False), plugin_id, scope),
        )
        holder.commit()
        assert finished.wait(5)
    finally:
        if not finished.is_set():
            holder.rollback()
        holder.close()
        thread.join(5)

    assert error == []
    saved = store.get_plugin_state(plugin_id, scope)
    assert saved is not None
    assert saved["memories"] == ["先写", "外部", "追加"]


def test_two_elders_do_not_share_oral_memories(client):
    registered = _register(client, "13900002003")
    headers = {"Authorization": f"Bearer {registered['access_token']}"}
    _add_elder(client, headers, "外婆", "1111")
    _add_elder(client, headers, "爷爷", "2222")
    invite = client.get("/api/v1/auth/me", headers=headers).json()["invite_code"]

    def login(pin: str) -> dict:
        resp = client.post("/api/v1/auth/member-login", json={"invite_code": invite, "pin_code": pin})
        assert resp.status_code == 200, resp.text
        return {"Authorization": f"Bearer {resp.json()['access_token']}"}

    elder_a = login("1111")
    elder_b = login("2222")
    saved = client.post(
        "/api/v1/plugins/elder_memory/action",
        headers=elder_a,
        json={"action_name": "save_oral_story", "payload": {"story_text": "只有外婆记得的红烧肉"}},
    )
    assert saved.status_code == 200
    assert saved.json()["status"] == "ok"

    def latest(who: dict) -> str:
        cards = client.get("/api/v1/plugins/active", headers=who).json()
        memory = next(card for card in cards if card["plugin_id"] == "elder_memory")
        return memory["data"]["latest_memory"]

    assert latest(elder_a) == "只有外婆记得的红烧肉"
    assert latest(elder_b) == "暂无口述记录，点击麦克风随时开讲"


def test_two_stores_keep_both_habit_keys():
    scope = "fam_habit:usr_child"
    barrier = threading.Barrier(2)
    stores = [Store(), Store()]
    errors: list[BaseException] = []

    def flip(target: Store, key: str) -> None:
        try:
            barrier.wait(timeout=3)

            def mark(state: dict) -> dict:
                state[key] = True
                return state

            target.update_plugin_state("child_habit", scope, mark)
        except BaseException as exc:
            errors.append(exc)

    threads = [
        threading.Thread(target=flip, args=(stores[0], "brush_teeth")),
        threading.Thread(target=flip, args=(stores[1], "drink_milk")),
    ]
    try:
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join(5)
        assert errors == []
        saved = stores[0].get_plugin_state("child_habit", scope)
        assert saved is not None
        assert saved["brush_teeth"] is True
        assert saved["drink_milk"] is True
    finally:
        for extra in stores:
            extra.reconnect()


def test_scope_key_rejects_colon():
    context = UserContext(
        user_id="usr:child",
        family_id="fam_ok",
        role=UserRole.CHILD,
        nickname="豆豆",
        birth_date="2018-01-01",
        jti="jti",
        token_exp=1,
    )
    with pytest.raises(PluginStateError, match="插件进度标识无效"):
        scope_key(context)


def test_openapi_uses_shared_response_models(client):
    spec = client.get("/openapi.json").json()
    schemas = spec["components"]["schemas"]
    assert "ProfileOut" in schemas
    assert "ServiceStatus" in schemas
    assert "ErrorBody" in schemas
    assert "FamilySettingsResult" in schemas
    assert "FamilyPluginToggleResult" in schemas
    assert "AdminPluginToggleResult" in schemas
    me = spec["paths"]["/api/v1/auth/me"]["get"]
    assert "401" in me["responses"]
    assert "403" in me["responses"]
    health = spec["paths"]["/healthz"]["get"]["responses"]["200"]
    assert "ServiceStatus" in json.dumps(health)


def test_invocation_counter_and_family_user_query():
    assert store.kv_add_int("nope") == 1
    assert store.kv_add_int("nope") == 2

    home_a = store.list_users_by_family("fam_missing")
    assert home_a == []


def test_family_query_stays_inside_one_home(client):
    first = _register(client, "13900002004", nickname="小敏")
    second = _register(client, "13900002005", nickname="小周")
    headers = {"Authorization": f"Bearer {first['access_token']}"}
    _add_elder(client, headers, "外婆", "3333")

    only_first = store.list_users_by_family(first["family_id"])
    only_second = store.list_users_by_family(second["family_id"])
    assert {item["family_id"] for item in only_first} == {first["family_id"]}
    assert len(only_first) == 2
    assert [item["id"] for item in only_second] == [second["user_id"]]
    counts = store.count_users_by_role()
    assert counts["guardian"] == 2
    assert counts["elder"] == 1


def test_pages_keep_session_unless_unauthorized():
    index = (ROOT / "prototype" / "index.html").read_text(encoding="utf-8")
    admin = (ROOT / "prototype" / "admin.html").read_text(encoding="utf-8")
    login = (ROOT / "prototype" / "login.html").read_text(encoding="utf-8")
    assert "服务暂时不可用，登录状态还在" in index
    assert "resp.status === 401" in index
    assert "网络中断，开关没有保存" in index
    assert "大盘暂时加载失败，登录状态还在" in admin
    assert "escapeHtml(p.name)" in admin
    assert "网络中断" in admin
    assert "loginPassword').value.trim()" not in login
    assert "regPassword').value.trim()" not in login
    assert "authRequestBusy" in login
