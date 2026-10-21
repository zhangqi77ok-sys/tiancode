"""FastAPI 认证、家庭多租户与 RBAC 权限端到端测试 (E2E Integration Test)

严格遵循 TDD 规范，验证：
1. 守护者注册建家与密码登录
2. 守护者纳管长辈与幼童成员并分配 Pin 码
3. 长辈/幼童使用家庭邀请码 + Pin 码免密极速登录
4. 角色权限拦截（长辈/儿童修改家庭设置被 403 拦截）
5. 守护者成功修改家庭看护设置
6. 缺失令牌/伪造令牌的 401 安全拦截
7. 错误邀请码与错误 Pin 码的安全拦截
"""
import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from app.api.v1.auth import router as auth_router


@pytest.fixture
def client():
    app = FastAPI(title="WarmCompanion Test API")
    app.include_router(auth_router)
    return TestClient(app)


def test_auth_full_family_lifecycle_and_rbac(client):
    # 1. 守护者建家注册
    reg_resp = client.post(
        "/api/v1/auth/register",
        json={
            "phone": "13900139001",
            "password": "Password123!",
            "family_name": "常伴一家人",
            "nickname": "女儿小敏",
            "birth_date": "1992-05-15",
        },
    )
    assert reg_resp.status_code == 201
    guardian_token_data = reg_resp.json()
    assert guardian_token_data["role"] == "guardian"
    guardian_token = guardian_token_data["access_token"]
    guardian_headers = {"Authorization": f"Bearer {guardian_token}"}

    # 2. 守护者查看家庭详情与邀请码
    me_resp = client.get("/api/v1/auth/me", headers=guardian_headers)
    assert me_resp.status_code == 200
    family_info = me_resp.json()
    invite_code = family_info["invite_code"]
    assert len(invite_code) == 6

    # 3. 守护者为家庭添加银发长辈（外婆）
    elder_add_resp = client.post(
        "/api/v1/family/members",
        headers=guardian_headers,
        json={
            "nickname": "外婆",
            "role": "elder",
            "birth_date": "1958-06-18",
            "pin_code": "8888",
        },
    )
    assert elder_add_resp.status_code == 201
    elder_data = elder_add_resp.json()
    assert elder_data["nickname"] == "外婆"
    assert elder_data["age"] >= 65
    assert elder_data["pin_code"] == "8888"

    # 4. 守护者为家庭添加幼童宝贝（豆豆）
    child_add_resp = client.post(
        "/api/v1/family/members",
        headers=guardian_headers,
        json={
            "nickname": "豆豆",
            "role": "child",
            "birth_date": "2018-09-01",
            "pin_code": "6666",
        },
    )
    assert child_add_resp.status_code == 201
    child_data = child_add_resp.json()
    assert child_data["nickname"] == "豆豆"
    assert child_data["age"] <= 10

    # 5. 长辈在专属老人机上使用【邀请码 + 4位Pin码】免密登录
    elder_login_resp = client.post(
        "/api/v1/auth/member-login",
        json={"invite_code": invite_code, "pin_code": "8888"},
    )
    assert elder_login_resp.status_code == 200
    elder_auth = elder_login_resp.json()
    assert elder_auth["role"] == "elder"
    elder_token = elder_auth["access_token"]
    elder_headers = {"Authorization": f"Bearer {elder_token}"}

    # 6. 长辈查询个人主页身份
    elder_me = client.get("/api/v1/auth/me", headers=elder_headers)
    assert elder_me.status_code == 200
    assert elder_me.json()["role"] == "elder"
    assert elder_me.json()["nickname"] == "外婆"

    # 7. RBAC 越权拦截：长辈试图修改家庭系统设置，必须被 403 拦截！
    elder_hack_resp = client.put(
        "/api/v1/family/settings",
        headers=elder_headers,
        json={"medication_reminder_enabled": False, "kids_curfew_enabled": False},
    )
    assert elder_hack_resp.status_code == 403
    assert "权限不足" in elder_hack_resp.json()["detail"]

    # 8. 守护者合法修改家庭系统设置，必须 200 成功
    guardian_update_resp = client.put(
        "/api/v1/family/settings",
        headers=guardian_headers,
        json={"medication_reminder_enabled": True, "kids_curfew_enabled": True},
    )
    assert guardian_update_resp.status_code == 200
    assert guardian_update_resp.json()["status"] == "ok"

    # 9. 安全防御：无 Token 请求受保护接口必须返回 401
    unauth_resp = client.get("/api/v1/auth/me")
    assert unauth_resp.status_code == 401

    # 10. 安全防御：伪造或损坏的 Token 必须返回 401
    bad_token_resp = client.get("/api/v1/auth/me", headers={"Authorization": "Bearer bad.token.here"})
    assert bad_token_resp.status_code == 401

    # 11. 安全防御：错误的 Pin 码必须返回 401
    wrong_pin_resp = client.post(
        "/api/v1/auth/member-login",
        json={"invite_code": invite_code, "pin_code": "0000"},
    )
    assert wrong_pin_resp.status_code == 401

    # 12. 安全防御：不存在的家庭邀请码必须返回 404
    wrong_invite_resp = client.post(
        "/api/v1/auth/member-login",
        json={"invite_code": "ZZZZZZ", "pin_code": "8888"},
    )
    assert wrong_invite_resp.status_code == 404
