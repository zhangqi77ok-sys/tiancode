"""运行时边界：断线重试、部分设置、坏数据、口述上限、调用计数和数据库 503。"""
from __future__ import annotations

import os
import time
from datetime import date

import psycopg
import pytest
from fastapi.testclient import TestClient

from app.core.security import DEFAULT_EXPIRES_IN, decode_jwt
from app.core.store import DuplicateRecord, store
from app.main import create_app
from app.plugins.builtin_child import MAX_QUESTION_CHARS, _habit_stars
from app.plugins.builtin_elder import MAX_ORAL_CHARS, MAX_ORAL_ENTRIES
from app.plugins.loader import load_builtin_plugins
from app.plugins.registry import plugin_registry
from app.plugins.state import scope_key
from app.schemas.auth import UserContext, UserRole


@pytest.fixture
def client():
    return TestClient(create_app())


def _register(client: TestClient, phone: str = "13700001111"):
    resp = client.post(
        "/api/v1/auth/register",
        json={
            "phone": phone,
            "password": "Password123!",
            "family_name": "边界之家",
            "nickname": "监护人",
            "birth_date": "1990-01-01",
        },
    )
    assert resp.status_code == 201, resp.text
    body = resp.json()
    headers = {"Authorization": f"Bearer {body['access_token']}"}
    me = client.get("/api/v1/auth/me", headers=headers)
    assert me.status_code == 200, me.text
    return headers, me.json(), body


def _add_elder(client: TestClient, headers: dict, pin: str = "4545") -> dict:
    created = client.post(
        "/api/v1/family/members",
        headers=headers,
        json={"nickname": "外婆", "role": "elder", "birth_date": "1950-02-02", "pin_code": pin},
    )
    assert created.status_code == 201, created.text
    return created.json()


def _elder_headers(client: TestClient, invite: str, pin: str = "4545") -> dict:
    login = client.post("/api/v1/auth/member-login", json={"invite_code": invite, "pin_code": pin})
    assert login.status_code == 200, login.text
    return {"Authorization": f"Bearer {login.json()['access_token']}"}


def test_register_expires_in_matches_token_lifetime(client):
    _headers, _me, body = _register(client, "13700001112")
    assert body["expires_in"] == DEFAULT_EXPIRES_IN
    claims = decode_jwt(body["access_token"], os.environ["JWT_SECRET"])
    assert claims["exp"] - claims["iat"] == body["expires_in"]


def test_duplicate_phone_and_invite_collision(client, monkeypatch):
    _register(client, "13700001113")
    again = client.post(
        "/api/v1/auth/register",
        json={
            "phone": "13700001113",
            "password": "Password123!",
            "family_name": "重复之家",
            "nickname": "另一位",
            "birth_date": "1991-02-02",
        },
    )
    assert again.status_code == 409

    monkeypatch.setattr(store, "get_user_by_phone", lambda _phone: None)
    raced = client.post(
        "/api/v1/auth/register",
        json={
            "phone": "13700001113",
            "password": "Password123!",
            "family_name": "竞争之家",
            "nickname": "竞争",
            "birth_date": "1991-02-02",
        },
    )
    assert raced.status_code == 409
    assert raced.json()["detail"] == "该手机号已注册，请直接登录"
    assert store.count_families() == 1

    calls = {"n": 0}
    real = store.insert_family_with_user

    def flaky(family, user):
        calls["n"] += 1
        if calls["n"] == 1:
            raise DuplicateRecord("families_invite_code_key")
        return real(family, user)

    monkeypatch.setattr(store, "insert_family_with_user", flaky)
    monkeypatch.setattr(store, "get_user_by_phone", lambda _phone: None)
    created = client.post(
        "/api/v1/auth/register",
        json={
            "phone": "13700001114",
            "password": "Password123!",
            "family_name": "重试之家",
            "nickname": "重试",
            "birth_date": "1992-03-03",
        },
    )
    assert created.status_code == 201, created.text
    assert calls["n"] == 2


def test_partial_settings_keep_the_other_flag_and_plugin_toggle(client):
    headers, me, _body = _register(client, "13700001115")
    _add_elder(client, headers)
    toggled = client.put(
        "/api/v1/family/plugins/elder_opera/toggle",
        headers=headers,
        json={"enabled": False},
    )
    assert toggled.status_code == 200
    saved = client.put(
        "/api/v1/family/settings",
        headers=headers,
        json={"medication_reminder_enabled": False},
    )
    assert saved.status_code == 200, saved.text
    settings = saved.json()["settings"]
    assert settings["medication_reminder_enabled"] is False
    assert settings["kids_curfew_enabled"] is True
    assert settings["plugins"]["elder_opera"] is False

    empty = client.put("/api/v1/family/settings", headers=headers, json={})
    assert empty.status_code == 422
    nothing = client.put(
        "/api/v1/family/settings",
        headers=headers,
        json={"kids_curfew_enabled": None},
    )
    assert nothing.status_code == 422

    elder_headers = _elder_headers(client, me["invite_code"])
    blocked = client.post(
        "/api/v1/plugins/elder_medication/action",
        headers=elder_headers,
        json={"action_name": "checkin", "payload": {}},
    )
    assert blocked.status_code == 400
    assert "关闭用药提醒" in blocked.json()["detail"]


def test_plugin_action_errors_are_http_400_and_memory_is_bounded(client):
    headers, me, _body = _register(client, "13700001116")
    elder = _add_elder(client, headers, pin="5656")
    elder_headers = _elder_headers(client, me["invite_code"], pin="5656")

    empty_name = client.post(
        "/api/v1/plugins/elder_memory/action",
        headers=elder_headers,
        json={"action_name": "", "payload": {}},
    )
    assert empty_name.status_code == 422

    unknown = client.post(
        "/api/v1/plugins/elder_opera/action",
        headers=elder_headers,
        json={"action_name": "sing", "payload": {}},
    )
    assert unknown.status_code == 400
    assert "不支持的动作" in unknown.json()["detail"]

    blank = client.post(
        "/api/v1/plugins/elder_memory/action",
        headers=elder_headers,
        json={"action_name": "save_oral_story", "payload": {"story_text": "  "}},
    )
    assert blank.status_code == 400

    too_long = client.post(
        "/api/v1/plugins/elder_memory/action",
        headers=elder_headers,
        json={"action_name": "save_oral_story", "payload": {"story_text": "事" * (MAX_ORAL_CHARS + 1)}},
    )
    assert too_long.status_code == 400

    for index in range(MAX_ORAL_ENTRIES + 1):
        saved = client.post(
            "/api/v1/plugins/elder_memory/action",
            headers=elder_headers,
            json={"action_name": "save_oral_story", "payload": {"story_text": f"第{index}段"}},
        )
        assert saved.status_code == 200, saved.text
    cards = client.get("/api/v1/plugins/active", headers=elder_headers).json()
    memory = next(card for card in cards if card["plugin_id"] == "elder_memory")
    assert memory["data"]["recorded_memories_count"] == MAX_ORAL_ENTRIES
    assert memory["data"]["latest_memory"] == f"第{MAX_ORAL_ENTRIES}段"

    removed = client.delete(f"/api/v1/family/members/{elder['id']}", headers=headers)
    assert removed.status_code == 204
    context = UserContext(
        user_id=elder["id"],
        family_id=me["family_id"],
        role=UserRole.ELDER,
        nickname="外婆",
        birth_date="1950-02-02",
        jti="not-used",
        token_exp=0,
    )
    assert store.get_plugin_state("elder_memory", scope_key(context)) is None


def test_corrupt_json_and_one_broken_card_do_not_fail_the_page(client, monkeypatch):
    headers, me, _body = _register(client, "13700001117")
    _add_elder(client, headers)
    elder_headers = _elder_headers(client, me["invite_code"])
    elder_user = next(user for user in store.list_users_by_family(me["family_id"]) if user["role"] == "elder")
    scope = f"{me['family_id']}:{elder_user['id']}"

    store._run(
        lambda conn: conn.execute(
            "UPDATE families SET settings_json = %s WHERE id = %s",
            ("{", me["family_id"]),
        )
    )
    profile = client.get("/api/v1/auth/me", headers=headers)
    assert profile.status_code == 200, profile.text
    assert profile.json()["settings"] == {}

    store.set_plugin_state("elder_opera", scope, {"index": {"bad": True}})
    store.set_plugin_state("elder_memory", scope, {"memories": "不是列表"})
    store.set_plugin_state(
        "child_habit",
        scope,
        {"brush_teeth": "yes", "drink_milk": True, "sleep_on_time": False, "extra": True},
    )
    cards = client.get("/api/v1/plugins/active", headers=elder_headers)
    assert cards.status_code == 200, cards.text
    by_id = {card["plugin_id"]: card for card in cards.json()}
    assert by_id["elder_opera"]["data"]["current_track"]["id"] == "op_01"
    assert by_id["elder_memory"]["data"]["recorded_memories_count"] == 0
    elder_context = UserContext(
        user_id=elder_user["id"],
        family_id=me["family_id"],
        role=UserRole.ELDER,
        nickname="外婆",
        birth_date="1950-02-02",
        jti="not-used",
        token_exp=0,
    )
    assert _habit_stars(elder_context, "child_habit") == {
        "brush_teeth": False,
        "drink_milk": True,
        "sleep_on_time": False,
    }

    load_builtin_plugins()
    plugin = plugin_registry.get_plugin("elder_opera")

    async def boom(*_args, **_kwargs):
        raise RuntimeError("card render failed")

    monkeypatch.setattr(plugin, "render_card", boom)
    survived = client.get("/api/v1/plugins/active", headers=elder_headers)
    assert survived.status_code == 200, survived.text
    survived_ids = [card["plugin_id"] for card in survived.json()]
    assert "elder_opera" not in survived_ids
    assert "elder_memory" in survived_ids
    assert "elder_medication" in survived_ids


def test_question_text_is_truncated_and_invocations_increment_atomically(client):
    headers, me, _body = _register(client, "13700001118")
    created = client.post(
        "/api/v1/family/members",
        headers=headers,
        json={
            "nickname": "豆豆",
            "role": "child",
            "birth_date": date(date.today().year - 8, 1, 1).isoformat(),
            "pin_code": "6767",
        },
    )
    assert created.status_code == 201, created.text
    child_headers = _elder_headers(client, me["invite_code"], pin="6767")
    asked = client.post(
        "/api/v1/plugins/child_encyclopedia/action",
        headers=child_headers,
        json={"action_name": "ask_question", "payload": {"question": "为什么" * 300}},
    )
    assert asked.status_code == 200, asked.text
    assert len(asked.json()["data"]["question"]) == MAX_QUESTION_CHARS

    store.kv_set("plugin_invocations:child_encyclopedia", "nope")
    assert store.kv_add_int("plugin_invocations:child_encyclopedia", 1) == 1
    again = client.post(
        "/api/v1/plugins/child_encyclopedia/action",
        headers=child_headers,
        json={"action_name": "ask_question", "payload": {"question": 12}},
    )
    assert again.status_code == 200, again.text
    assert again.json()["data"]["question"] == ""

    admin = client.post(
        "/api/v1/admin/login",
        json={"username": "admin", "password": os.environ["ADMIN_PASSWORD"]},
    )
    assert admin.status_code == 200, admin.text
    plugins = client.get(
        "/api/v1/admin/plugins",
        headers={"Authorization": f"Bearer {admin.json()['access_token']}"},
    )
    assert plugins.status_code == 200, plugins.text
    encyclopedia = next(item for item in plugins.json() if item["id"] == "child_encyclopedia")
    assert encyclopedia["invocations_count"] == 2
    assert admin.json()["expires_in"] == DEFAULT_EXPIRES_IN


def test_login_failure_read_does_not_rewrite_a_fresh_window():
    now = time.time()
    store.append_login_failure("guardian:13700001119", window_seconds=300, now=now)
    raw = store.kv_get("login_fail:guardian:13700001119")
    kept = store.read_login_failures("guardian:13700001119", window_seconds=300, now=now + 1)
    assert len(kept) == 1
    assert store.kv_get("login_fail:guardian:13700001119") == raw
    store.read_login_failures("guardian:13700001119", window_seconds=300, now=now + 301)
    assert store.kv_get("login_fail:guardian:13700001119") is None


def test_store_replaces_a_dropped_or_stale_connection():
    store.ping()
    first = store._conn
    assert first is not None
    original = first.execute
    tripped = {"done": False}

    def execute(query, params=None, **kwargs):
        text = query if isinstance(query, str) else str(query)
        if "COUNT(*)" in text and not tripped["done"]:
            tripped["done"] = True
            raise psycopg.OperationalError("connection lost")
        return original(query, params, **kwargs)

    first.execute = execute
    assert store.count_families() == 0
    assert tripped["done"] is True
    assert store._conn is not first

    current = store._conn
    assert current is not None
    current.close()
    assert store.count_families() == 0
    assert store._conn is not current

    stale = store._conn
    store._url = "postgresql://stale"
    store.ping()
    assert store._conn is not stale
    assert store._url == os.environ["TIANCODE_DATABASE_URL"]


def test_database_outage_on_an_api_route_is_503(client, monkeypatch):
    headers, _me, _body = _register(client, "13700001120")

    def boom(*_args, **_kwargs):
        raise psycopg.OperationalError("db down")

    monkeypatch.setattr(store, "get_user", boom)
    resp = client.get("/api/v1/auth/me", headers=headers)
    assert resp.status_code == 503
    assert resp.json()["detail"] == "database not ready"
    assert psycopg.OperationalError in client.app.exception_handlers
    assert psycopg.InterfaceError in client.app.exception_handlers


def test_family_user_query_and_role_counts_stay_inside_one_family(client):
    headers_a, me_a, _body = _register(client, "13700001121")
    headers_b, me_b, _body_b = _register(client, "13700001122")
    _add_elder(client, headers_a, pin="7878")
    _add_elder(client, headers_b, pin="8787")

    only_a = store.list_users_by_family(me_a["family_id"])
    only_b = store.list_users_by_family(me_b["family_id"])
    assert {user["family_id"] for user in only_a} == {me_a["family_id"]}
    assert {user["family_id"] for user in only_b} == {me_b["family_id"]}
    assert len(only_a) == 2
    assert len(only_b) == 2

    counts = store.count_users_by_role()
    assert counts["guardian"] == 2
    assert counts["elder"] == 2
    assert "child" not in counts

    rows = store._run(
        lambda conn: conn.execute(
            "SELECT indexname FROM pg_indexes WHERE indexname = 'idx_revoked_jti_exp'"
        ).fetchall()
    )
    assert rows
