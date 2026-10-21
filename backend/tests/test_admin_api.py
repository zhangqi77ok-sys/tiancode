"""平台管理员登录、大盘计数和插件熔断。

没有模型配置接口。大盘只返回家庭、成员和未熔断插件数。
"""
import os
from datetime import date

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from app.api.v1.admin import router as admin_router
from app.api.v1.auth import router as auth_router
from app.api.v1.plugins import router as plugins_router


@pytest.fixture
def client():
    app = FastAPI(title="WarmCompanion Admin Test API")
    app.include_router(auth_router)
    app.include_router(plugins_router)
    app.include_router(admin_router)
    return TestClient(app)


def test_admin_rbac_ai_config_and_global_killswitch(client):
    # 1. 注册普通家庭守护者
    reg = client.post(
        "/api/v1/auth/register",
        json={
            "phone": "13700137000",
            "password": "Password123!",
            "family_name": "常青之家",
            "nickname": "阿强",
            "birth_date": "1988-08-08",
        },
    )
    assert reg.status_code == 201
    guardian_headers = {"Authorization": f"Bearer {reg.json()['access_token']}"}
    invite_code = client.get("/api/v1/auth/me", headers=guardian_headers).json()["invite_code"]

    # 纳管一名儿童
    client.post(
        "/api/v1/family/members",
        headers=guardian_headers,
        json={"nickname": "乐乐", "role": "child", "birth_date": date(date.today().year - 8, 1, 1).isoformat(), "pin_code": "3333"},
    )
    child_login = client.post(
        "/api/v1/auth/member-login",
        json={"invite_code": invite_code, "pin_code": "3333"},
    )
    child_headers = {"Authorization": f"Bearer {child_login.json()['access_token']}"}

    # 2. RBAC 越权拦截：普通守护者试图访问管理员大盘，必须被 403 拦截！
    hack_ai = client.get("/api/v1/admin/dashboard/stats", headers=guardian_headers)
    assert hack_ai.status_code == 403
    assert "权限不足" in hack_ai.json()["detail"]

    # 3. 管理员错误密码登录拦截
    bad_login = client.post(
        "/api/v1/admin/login",
        json={"username": "admin", "password": "WrongPassword!"},
    )
    assert bad_login.status_code == 401

    # 4. 管理员正确登录
    admin_login_resp = client.post(
        "/api/v1/admin/login",
        json={"username": "admin", "password": os.environ["ADMIN_PASSWORD"]},
    )
    assert admin_login_resp.status_code == 200
    admin_token = admin_login_resp.json()["access_token"]
    admin_headers = {"Authorization": f"Bearer {admin_token}"}

    # 5. 管理员查看全平台插件列表
    plugins_resp = client.get("/api/v1/admin/plugins", headers=admin_headers)
    assert plugins_resp.status_code == 200
    plugins_list = plugins_resp.json()
    assert len(plugins_list) >= 6

    # 8. 全局紧急熔断 (Kill-Switch)：管理员将儿童故事插件 child_story 全局关停
    kill_resp = client.put(
        "/api/v1/admin/plugins/child_story/toggle",
        headers=admin_headers,
        json={"global_enabled": False},
    )
    assert kill_resp.status_code == 200
    assert kill_resp.json()["global_enabled"] is False

    # 9. 儿童端受到全局熔断影响，主页卡片流中已无 child_story
    child_cards = client.get("/api/v1/plugins/active", headers=child_headers).json()
    child_pids = [c["plugin_id"] for c in child_cards]
    assert "child_story" not in child_pids

    # 10. 儿童若强行调用已熔断的插件动作，必须返回 403 明确告知已熔断！
    child_blocked = client.post(
        "/api/v1/plugins/child_story/action",
        headers=child_headers,
        json={"action_name": "choose_branch", "payload": {}},
    )
    assert child_blocked.status_code == 403
    assert "熔断" in child_blocked.json()["detail"]

    # 11. 管理员恢复插件正常运行
    restore_resp = client.put(
        "/api/v1/admin/plugins/child_story/toggle",
        headers=admin_headers,
        json={"global_enabled": True},
    )
    assert restore_resp.status_code == 200
    assert restore_resp.json()["global_enabled"] is True

    # 12. 查看大盘监控指标
    stats_resp = client.get("/api/v1/admin/dashboard/stats", headers=admin_headers)
    assert stats_resp.status_code == 200
    stats = stats_resp.json()
    assert stats["total_families"] >= 1
    assert stats["total_children"] >= 1
    assert stats["active_plugins_count"] >= 6
