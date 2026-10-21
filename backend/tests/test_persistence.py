"""家庭、成员、插件开关和口述记录必须在重新打开数据库后还在。"""
from __future__ import annotations

import os

from fastapi import FastAPI
from fastapi.testclient import TestClient

from app.api.v1.admin import router as admin_router
from app.api.v1.auth import router as auth_router
from app.api.v1.plugins import router as plugins_router
from app.core.store import store


def _client() -> TestClient:
    app = FastAPI()
    app.include_router(auth_router)
    app.include_router(plugins_router)
    app.include_router(admin_router)
    return TestClient(app)


def test_family_member_and_memory_survive_reconnect():
    client = _client()
    reg = client.post(
        "/api/v1/auth/register",
        json={
            "phone": "13600001111",
            "password": "Password123!",
            "family_name": "持久之家",
            "nickname": "小敏",
            "birth_date": "1991-04-04",
        },
    )
    assert reg.status_code == 201, reg.text
    guardian = {"Authorization": f"Bearer {reg.json()['access_token']}"}
    me = client.get("/api/v1/auth/me", headers=guardian).json()
    invite = me["invite_code"]

    created = client.post(
        "/api/v1/family/members",
        headers=guardian,
        json={"nickname": "外婆", "role": "elder", "birth_date": "1950-02-02", "pin_code": "4545"},
    )
    assert created.status_code == 201, created.text
    elder_login = client.post(
        "/api/v1/auth/member-login",
        json={"invite_code": invite, "pin_code": "4545"},
    )
    elder_headers = {"Authorization": f"Bearer {elder_login.json()['access_token']}"}
    saved = client.post(
        "/api/v1/plugins/elder_memory/action",
        headers=elder_headers,
        json={"action_name": "save_oral_story", "payload": {"story_text": "院子里的雪里蕻"}},
    )
    assert saved.status_code == 200, saved.text

    client.put(
        "/api/v1/family/plugins/elder_opera/toggle",
        headers=guardian,
        json={"enabled": False},
    )

    store.reconnect()

    again = client.get("/api/v1/auth/me", headers=guardian)
    assert again.status_code == 200
    assert again.json()["family_id"] == me["family_id"]
    assert again.json()["settings"]["plugins"]["elder_opera"] is False

    cards = client.get("/api/v1/plugins/active", headers=elder_headers).json()
    memory = next(card for card in cards if card["plugin_id"] == "elder_memory")
    assert memory["data"]["latest_memory"] == "院子里的雪里蕻"
    assert all(card["plugin_id"] != "elder_opera" for card in cards)

    admin = client.post(
        "/api/v1/admin/login",
        json={"username": "admin", "password": os.environ["ADMIN_PASSWORD"]},
    )
    admin_headers = {"Authorization": f"Bearer {admin.json()['access_token']}"}
    killed = client.put(
        "/api/v1/admin/plugins/child_story/toggle",
        headers=admin_headers,
        json={"global_enabled": False},
    )
    assert killed.status_code == 200
    store.reconnect()
    listed = client.get("/api/v1/admin/plugins", headers=admin_headers).json()
    story = next(item for item in listed if item["id"] == "child_story")
    assert story["global_enabled"] is False

    store.delete_user(elder_login.json()["user_id"])
    assert client.get("/api/v1/auth/me", headers=elder_headers).status_code == 401
