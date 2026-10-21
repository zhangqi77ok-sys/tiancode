"""AuthZ, runtime secrets, and family member management.

These cases describe the phase-1 contract. They fail on the pre-fix tree.
"""
from __future__ import annotations

from datetime import date

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from app.api.v1.admin import router as admin_router
from app.api.v1.auth import router as auth_router
from app.core.store import store
from app.api.v1.plugins import router as plugins_router
from app.core.security import hash_password, verify_password
from app.main import create_app


@pytest.fixture
def client():
    app = FastAPI()
    app.include_router(auth_router)
    app.include_router(plugins_router)
    app.include_router(admin_router)
    return TestClient(app)


def _register(client: TestClient, phone: str = "13900001111"):
    resp = client.post(
        "/api/v1/auth/register",
        json={
            "phone": phone,
            "password": "Password123!",
            "family_name": "安测之家",
            "nickname": "监护人",
            "birth_date": "1990-01-01",
        },
    )
    assert resp.status_code == 201, resp.text
    headers = {"Authorization": f"Bearer {resp.json()['access_token']}"}
    me = client.get("/api/v1/auth/me", headers=headers)
    assert me.status_code == 200, me.text
    return headers, me.json()["invite_code"], me.json()["family_id"]


def _add_member(client, headers, *, nickname, role, birth_date, pin_code):
    return client.post(
        "/api/v1/family/members",
        headers=headers,
        json={
            "nickname": nickname,
            "role": role,
            "birth_date": birth_date,
            "pin_code": pin_code,
        },
    )


def test_guardian_cannot_mint_admin_or_guardian_member(client):
    headers, _, _ = _register(client)
    admin = _add_member(
        client, headers, nickname="伪管理员", role="admin", birth_date="1990-01-01", pin_code="1357"
    )
    assert admin.status_code == 422

    guardian = _add_member(
        client, headers, nickname="第二监护", role="guardian", birth_date="1985-02-02", pin_code="2468"
    )
    assert guardian.status_code == 422


def test_poisoned_admin_row_cannot_pin_login(client):
    headers, invite, family_id = _register(client, "13900001112")
    store.upsert_user({
        "id": "usr_poison_admin",
        "family_id": family_id,
        "nickname": "poison",
        "role": "admin",
        "birth_date": "1990-01-01",
        "phone": None,
        "password_hash": None,
        "pin_code": "9999",
        "pin_hash": hash_password("9999"),
    })
    # headers unused; the row is planted directly
    assert headers
    login = client.post(
        "/api/v1/auth/member-login",
        json={"invite_code": invite, "pin_code": "9999"},
    )
    assert login.status_code == 401
    assert login.json()["detail"] == "邀请码或 Pin 码错误"


def test_family_roles_cannot_call_admin_routes(client):
    headers, invite, _ = _register(client, "13900001113")
    elder = _add_member(
        client, headers, nickname="外婆", role="elder", birth_date="1955-03-12", pin_code="1111"
    )
    child = _add_member(
        client, headers, nickname="豆豆", role="child", birth_date=date(date.today().year - 8, 1, 1).isoformat(), pin_code="2222"
    )
    assert elder.status_code == 201
    assert child.status_code == 201

    elder_token = client.post(
        "/api/v1/auth/member-login", json={"invite_code": invite, "pin_code": "1111"}
    ).json()["access_token"]
    child_token = client.post(
        "/api/v1/auth/member-login", json={"invite_code": invite, "pin_code": "2222"}
    ).json()["access_token"]

    for token in (headers["Authorization"].split()[1], elder_token, child_token):
        resp = client.get("/api/v1/admin/dashboard/stats", headers={"Authorization": f"Bearer {token}"})
        assert resp.status_code == 403
        assert "权限不足" in resp.json()["detail"]


def test_member_crud_is_family_scoped_and_delete_kills_token(client):
    headers_a, invite_a, _ = _register(client, "13900001114")
    headers_b, _, _ = _register(client, "13900001115")

    created = _add_member(
        client, headers_a, nickname="外婆", role="elder", birth_date="1954-01-01", pin_code="3434"
    )
    assert created.status_code == 201
    elder_id = created.json()["id"]
    assert created.json()["pin_code"] == "3434"

    listed = client.get("/api/v1/family/members", headers=headers_b)
    assert listed.status_code == 200
    assert all(item["nickname"] != "外婆" for item in listed.json())

    outsider = client.delete(f"/api/v1/family/members/{elder_id}", headers=headers_b)
    assert outsider.status_code == 404

    renamed = client.put(
        f"/api/v1/family/members/{elder_id}",
        headers=headers_a,
        json={"nickname": "外婆改"},
    )
    assert renamed.status_code == 200
    assert renamed.json()["nickname"] == "外婆改"
    assert renamed.json()["pin_code"] is None

    elder_login = client.post(
        "/api/v1/auth/member-login", json={"invite_code": invite_a, "pin_code": "3434"}
    )
    assert elder_login.status_code == 200
    elder_headers = {"Authorization": f"Bearer {elder_login.json()['access_token']}"}
    assert client.get("/api/v1/auth/me", headers=elder_headers).status_code == 200

    removed = client.delete(f"/api/v1/family/members/{elder_id}", headers=headers_a)
    assert removed.status_code == 204
    assert client.get("/api/v1/auth/me", headers=elder_headers).status_code == 401


def test_age_must_match_role_and_phone_must_be_digits(client):
    headers, _, _ = _register(client, "13900001116")
    too_young = _add_member(
        client, headers, nickname="小老人", role="elder", birth_date="2020-05-05", pin_code="1010"
    )
    assert too_young.status_code == 422

    too_old = _add_member(
        client, headers, nickname="大孩子", role="child", birth_date="1990-05-05", pin_code="2020"
    )
    assert too_old.status_code == 422

    bad_phone = client.post(
        "/api/v1/auth/register",
        json={
            "phone": "abcdefghijk",
            "password": "Password123!",
            "family_name": "非法号",
            "nickname": "甲",
            "birth_date": "1990-01-01",
        },
    )
    assert bad_phone.status_code == 422


def test_pin_is_hashed_and_invite_errors_do_not_enumerate(client):
    headers, invite, family_id = _register(client, "13900001117")
    created = _add_member(
        client, headers, nickname="外婆", role="elder", birth_date="1952-04-04", pin_code="8888"
    )
    assert created.status_code == 201
    stored = next(user for user in store.list_users() if user["nickname"] == "外婆" and user["family_id"] == family_id)
    assert stored.get("pin_code") in (None, "")
    assert stored.get("pin_hash")
    assert stored["pin_hash"] != "8888"
    assert verify_password("8888", stored["pin_hash"]) is True

    listed = client.get("/api/v1/family/members", headers=headers).json()
    elder = next(item for item in listed if item["nickname"] == "外婆")
    assert elder["pin_code"] is None

    wrong_pin = client.post(
        "/api/v1/auth/member-login", json={"invite_code": invite, "pin_code": "0000"}
    )
    wrong_invite = client.post(
        "/api/v1/auth/member-login", json={"invite_code": "ZZZZZZ", "pin_code": "8888"}
    )
    assert wrong_pin.status_code == 401
    assert wrong_invite.status_code == 401
    assert wrong_pin.json()["detail"] == wrong_invite.json()["detail"]

    duplicate = _add_member(
        client, headers, nickname="另一位", role="child", birth_date=date(date.today().year - 8, 1, 1).isoformat(), pin_code="8888"
    )
    assert duplicate.status_code == 409


def test_settings_merge_keeps_plugin_toggles_and_off_blocks_action(client):
    headers, invite, _ = _register(client, "13900001118")
    assert _add_member(
        client, headers, nickname="外婆", role="elder", birth_date="1950-01-01", pin_code="4545"
    ).status_code == 201
    toggle = client.put(
        "/api/v1/family/plugins/elder_opera/toggle",
        headers=headers,
        json={"enabled": False},
    )
    assert toggle.status_code == 200
    saved = client.put(
        "/api/v1/family/settings",
        headers=headers,
        json={"medication_reminder_enabled": True, "kids_curfew_enabled": False},
    )
    assert saved.status_code == 200
    assert saved.json()["settings"]["plugins"]["elder_opera"] is False
    assert saved.json()["settings"]["kids_curfew_enabled"] is False

    elder_token = client.post(
        "/api/v1/auth/member-login", json={"invite_code": invite, "pin_code": "4545"}
    ).json()["access_token"]
    elder_headers = {"Authorization": f"Bearer {elder_token}"}
    cards = client.get("/api/v1/plugins/active", headers=elder_headers).json()
    assert "elder_opera" not in [card["plugin_id"] for card in cards]
    blocked = client.post(
        "/api/v1/plugins/elder_opera/action",
        headers=elder_headers,
        json={"action_name": "next_track", "payload": {}},
    )
    assert blocked.status_code == 403


def test_member_login_locks_out_after_repeated_failures(client):
    headers, invite, _ = _register(client, "13900001119")
    assert _add_member(
        client, headers, nickname="外婆", role="elder", birth_date="1948-02-02", pin_code="5656"
    ).status_code == 201
    for _ in range(5):
        failed = client.post(
            "/api/v1/auth/member-login", json={"invite_code": invite, "pin_code": "0000"}
        )
        assert failed.status_code == 401
    locked = client.post(
        "/api/v1/auth/member-login", json={"invite_code": invite, "pin_code": "5656"}
    )
    assert locked.status_code == 429


def test_create_app_refuses_missing_or_published_secrets(monkeypatch):
    monkeypatch.setenv("ADMIN_PASSWORD", "TestAdminPassword!")
    monkeypatch.setenv("JWT_SECRET", "tiancode-family-secret-32-bytes-min!")
    with pytest.raises(RuntimeError, match="JWT_SECRET"):
        create_app()

    monkeypatch.setenv("JWT_SECRET", "test-jwt-secret-for-local-suite-32bytes!")
    monkeypatch.setenv("ADMIN_PASSWORD", "AdminPassword123!")
    with pytest.raises(RuntimeError, match="ADMIN_PASSWORD"):
        create_app()

    monkeypatch.delenv("JWT_SECRET", raising=False)
    monkeypatch.setenv("ADMIN_PASSWORD", "TestAdminPassword!")
    with pytest.raises(RuntimeError, match="JWT_SECRET"):
        create_app()
