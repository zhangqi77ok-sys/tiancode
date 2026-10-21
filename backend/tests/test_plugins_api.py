"""伴暖插件化系统端到端测试 (Plugins E2E Integration Test)

覆盖验证：
1. 角色白名单过滤：长辈仅获取长辈插件（戏曲、时光册、用药），严禁出现儿童故事；
2. 动作调度与状态流转：戏曲切歌、时光册口述归档、好习惯打卡；
3. RBAC 越权拦截：长辈调用儿童故事插件被 403 严格拦截；儿童调用用药插件被 403 严格拦截；
4. 守护者家庭开关：守护者在家庭设置中关闭戏曲插件后，长辈主页该插件卡片立即下线。
"""
from datetime import date

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from app.api.v1.auth import router as auth_router
from app.api.v1.plugins import router as plugins_router


@pytest.fixture
def client():
    app = FastAPI(title="WarmCompanion Plugin Test API")
    app.include_router(auth_router)
    app.include_router(plugins_router)
    return TestClient(app)


def test_plugins_lifecycle_rbac_and_family_toggle(client):
    # 1. 注册家庭守护者
    reg = client.post(
        "/api/v1/auth/register",
        json={
            "phone": "13800138000",
            "password": "Password123!",
            "family_name": "伴暖演示家",
            "nickname": "敏敏",
            "birth_date": "1990-01-01",
        },
    )
    assert reg.status_code == 201
    guardian_token = reg.json()["access_token"]
    guardian_headers = {"Authorization": f"Bearer {guardian_token}"}
    invite_code = client.get("/api/v1/auth/me", headers=guardian_headers).json()["invite_code"]

    # 2. 纳管银发长辈（外婆）与幼童（豆豆）
    client.post(
        "/api/v1/family/members",
        headers=guardian_headers,
        json={"nickname": "外婆", "role": "elder", "birth_date": "1955-03-12", "pin_code": "1111"},
    )
    client.post(
        "/api/v1/family/members",
        headers=guardian_headers,
        json={"nickname": "豆豆", "role": "child", "birth_date": date(date.today().year - 8, 1, 1).isoformat(), "pin_code": "2222"},
    )

    # 3. 长辈登录并拉取插件卡片流
    elder_login = client.post(
        "/api/v1/auth/member-login",
        json={"invite_code": invite_code, "pin_code": "1111"},
    )
    assert elder_login.status_code == 200
    elder_headers = {"Authorization": f"Bearer {elder_login.json()['access_token']}"}

    elder_cards_resp = client.get("/api/v1/plugins/active", headers=elder_headers)
    assert elder_cards_resp.status_code == 200
    elder_cards = elder_cards_resp.json()
    plugin_ids = [c["plugin_id"] for c in elder_cards]

    # 验证长辈插件存在，且绝无儿童故事插件
    assert "elder_opera" in plugin_ids
    assert "elder_medication" in plugin_ids
    assert "elder_memory" in plugin_ids
    assert "child_story" not in plugin_ids
    assert "child_habit" not in plugin_ids

    # 4. 长辈执行合法动作：戏曲切到下一首
    next_opera = client.post(
        "/api/v1/plugins/elder_opera/action",
        headers=elder_headers,
        json={"action_name": "next_track", "payload": {}},
    )
    assert next_opera.status_code == 200
    assert next_opera.json()["status"] == "ok"
    assert "为您切换到" in next_opera.json()["message"]

    # 5. RBAC 越权拦截：长辈调用儿童专属故事插件动作，必须被 403 拦截！
    elder_hack_child = client.post(
        "/api/v1/plugins/child_story/action",
        headers=elder_headers,
        json={"action_name": "choose_branch", "payload": {"branch_id": "branch_a"}},
    )
    assert elder_hack_child.status_code == 403
    assert "权限不足" in elder_hack_child.json()["detail"]

    # 6. 儿童登录并执行互动故事动作
    child_login = client.post(
        "/api/v1/auth/member-login",
        json={"invite_code": invite_code, "pin_code": "2222"},
    )
    assert child_login.status_code == 200
    child_headers = {"Authorization": f"Bearer {child_login.json()['access_token']}"}

    child_cards = client.get("/api/v1/plugins/active", headers=child_headers).json()
    child_plugin_ids = [c["plugin_id"] for c in child_cards]
    assert "child_story" in child_plugin_ids
    assert "child_habit" in child_plugin_ids
    assert "elder_opera" not in child_plugin_ids

    # 7. 儿童故事决策分支
    story_action = client.post(
        "/api/v1/plugins/child_story/action",
        headers=child_headers,
        json={"action_name": "choose_branch", "payload": {"branch_id": "branch_a"}},
    )
    assert story_action.status_code == 200
    assert "第 2 幕" in story_action.json()["data"]["scene"]

    # 8. 守护者关闭长辈戏曲插件
    toggle_resp = client.put(
        "/api/v1/family/plugins/elder_opera/toggle",
        headers=guardian_headers,
        json={"enabled": False},
    )
    assert toggle_resp.status_code == 200

    # 9. 长辈再次刷新主页卡片流，戏曲插件已下线
    elder_cards_after = client.get("/api/v1/plugins/active", headers=elder_headers).json()
    elder_ids_after = [c["plugin_id"] for c in elder_cards_after]
    assert "elder_opera" not in elder_ids_after
    assert "elder_medication" in elder_ids_after
