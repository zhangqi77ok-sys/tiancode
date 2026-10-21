"""注销只作废当前令牌。儿童防沉迷按北京时间拒绝插件动作。"""
from __future__ import annotations

import os
from datetime import date, datetime

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from app.api.v1.admin import router as admin_router
from app.api.v1.auth import router as auth_router
from app.api.v1.plugins import router as plugins_router
from app.core.curfew import SHANGHAI, set_now


@pytest.fixture
def client():
    app = FastAPI()
    app.include_router(auth_router)
    app.include_router(plugins_router)
    app.include_router(admin_router)
    return TestClient(app)


def _child_birth() -> str:
    return date(date.today().year - 8, 1, 1).isoformat()


def _register_family(client: TestClient) -> tuple[dict, str]:
    reg = client.post(
        "/api/v1/auth/register",
        json={
            "phone": "13600136000",
            "password": "Password123!",
            "family_name": "夜读之家",
            "nickname": "小周",
            "birth_date": "1994-04-04",
        },
    )
    assert reg.status_code == 201
    guardian_headers = {"Authorization": f"Bearer {reg.json()['access_token']}"}
    invite = client.get("/api/v1/auth/me", headers=guardian_headers).json()["invite_code"]
    return guardian_headers, invite


def test_logout_revokes_only_the_current_token(client):
    reg = client.post(
        "/api/v1/auth/register",
        json={
            "phone": "13600136001",
            "password": "Password123!",
            "family_name": "两枚令牌",
            "nickname": "小周",
            "birth_date": "1994-04-04",
        },
    )
    assert reg.status_code == 201
    token_a = reg.json()["access_token"]
    again = client.post(
        "/api/v1/auth/login",
        json={"phone": "13600136001", "password": "Password123!"},
    )
    assert again.status_code == 200
    token_b = again.json()["access_token"]
    assert token_a != token_b

    headers_a = {"Authorization": f"Bearer {token_a}"}
    headers_b = {"Authorization": f"Bearer {token_b}"}
    assert client.get("/api/v1/auth/me", headers=headers_a).status_code == 200
    assert client.get("/api/v1/auth/me", headers=headers_b).status_code == 200

    logged_out = client.post("/api/v1/auth/logout", headers=headers_a)
    assert logged_out.status_code == 204
    assert client.get("/api/v1/auth/me", headers=headers_a).status_code == 401
    assert client.get("/api/v1/auth/me", headers=headers_b).status_code == 200

    fresh = client.post(
        "/api/v1/auth/login",
        json={"phone": "13600136001", "password": "Password123!"},
    )
    assert fresh.status_code == 200
    assert client.get(
        "/api/v1/auth/me",
        headers={"Authorization": f"Bearer {fresh.json()['access_token']}"},
    ).status_code == 200
    assert client.post("/api/v1/auth/logout").status_code == 401


def test_admin_logout_revokes_that_admin_token(client):
    first = client.post(
        "/api/v1/admin/login",
        json={"username": "admin", "password": os.environ["ADMIN_PASSWORD"]},
    )
    second = client.post(
        "/api/v1/admin/login",
        json={"username": "admin", "password": os.environ["ADMIN_PASSWORD"]},
    )
    assert first.status_code == 200
    assert second.status_code == 200
    headers_a = {"Authorization": f"Bearer {first.json()['access_token']}"}
    headers_b = {"Authorization": f"Bearer {second.json()['access_token']}"}
    assert client.post("/api/v1/auth/logout", headers=headers_a).status_code == 204
    assert client.get("/api/v1/admin/dashboard/stats", headers=headers_a).status_code == 401
    assert client.get("/api/v1/admin/dashboard/stats", headers=headers_b).status_code == 200


def _member(client, headers, nickname, role, pin):
    birth = "1950-01-01" if role == "elder" else _child_birth()
    resp = client.post(
        "/api/v1/family/members",
        headers=headers,
        json={"nickname": nickname, "role": role, "birth_date": birth, "pin_code": pin},
    )
    assert resp.status_code == 201


def _pin_headers(client, invite, pin):
    login = client.post(
        "/api/v1/auth/member-login",
        json={"invite_code": invite, "pin_code": pin},
    )
    assert login.status_code == 200
    return {"Authorization": f"Bearer {login.json()['access_token']}"}


def test_curfew_blocks_child_plugin_actions_in_shanghai_quiet_hours(client):
    guardian_headers, invite = _register_family(client)
    _member(client, guardian_headers, "外婆", "elder", "1111")
    _member(client, guardian_headers, "豆豆", "child", "2222")
    child_headers = _pin_headers(client, invite, "2222")
    elder_headers = _pin_headers(client, invite, "1111")

    def story():
        return client.post(
            "/api/v1/plugins/child_story/action",
            headers=child_headers,
            json={"action_name": "choose_branch", "payload": {"branch_id": "branch_a"}},
        )

    set_now(datetime(2026, 6, 15, 21, 30, tzinfo=SHANGHAI))
    blocked = story()
    assert blocked.status_code == 403
    assert "21:00" in blocked.json()["detail"]
    cards = client.get("/api/v1/plugins/active", headers=child_headers).json()
    assert "child_story" in [card["plugin_id"] for card in cards]

    set_now(datetime(2026, 6, 15, 5, 30, tzinfo=SHANGHAI))
    assert story().status_code == 403

    set_now(datetime(2026, 6, 15, 6, 0, tzinfo=SHANGHAI))
    assert story().status_code == 200

    set_now(datetime(2026, 6, 15, 20, 0, tzinfo=SHANGHAI))
    assert story().status_code == 200

    set_now(datetime(2026, 6, 15, 21, 30, tzinfo=SHANGHAI))
    turned_off = client.put(
        "/api/v1/family/settings",
        headers=guardian_headers,
        json={"medication_reminder_enabled": True, "kids_curfew_enabled": False},
    )
    assert turned_off.status_code == 200
    assert story().status_code == 200

    opera = client.post(
        "/api/v1/plugins/elder_opera/action",
        headers=elder_headers,
        json={"action_name": "next_track", "payload": {}},
    )
    assert opera.status_code == 200
