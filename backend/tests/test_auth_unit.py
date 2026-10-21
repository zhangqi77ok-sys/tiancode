"""认证与安全核心单元测试（TDD 第一阶段）

验证 HS256 JWT 生成/验证、密码哈希与防篡改、以及 Pydantic 契约校验。
"""
import pytest
from pydantic import ValidationError

from app.core.security import decode_jwt, encode_jwt, hash_password, verify_password
from app.schemas.auth import (
    FamilyMemberRole,
    MemberAddRequest,
    RegisterRequest,
    UserRole,
)

SECRET_KEY = "test-secret-key-for-unit-tests-32bytes-min!"


def test_jwt_encode_and_decode_success():
    """正常路径：验证 JWT 签发与解码能够完整无损还原载荷，且默认有效期严格为 30 天"""
    payload = {
        "sub": "usr_elder_1001",
        "fam": "fam_beijing_01",
        "role": UserRole.ELDER.value,
        "name": "外婆",
        "birth": "1958-06-18",
    }
    # 使用默认有效期（30 天）
    token = encode_jwt(payload, SECRET_KEY)
    assert isinstance(token, str)
    assert len(token.split(".")) == 3

    decoded = decode_jwt(token, SECRET_KEY)
    assert decoded["sub"] == "usr_elder_1001"
    assert decoded["fam"] == "fam_beijing_01"
    assert decoded["role"] == "elder"
    assert decoded["name"] == "外婆"
    assert decoded["birth"] == "1958-06-18"
    assert "exp" in decoded
    # 验证有效期恰好为 30 天 (30 * 86400 = 2,592,000 秒)
    assert decoded["exp"] - decoded["iat"] == 86400 * 30


def test_satoken_header_support():
    """支持使用 satoken 请求头提取身份，且用户必须仍在数据库中。"""
    from app.api.deps import get_current_user
    from app.core.runtime_secrets import jwt_secret
    from app.core.store import store

    store.upsert_user({
        "id": "usr_sa_01",
        "family_id": "fam_01",
        "nickname": "测试用户",
        "role": "guardian",
        "birth_date": "1990-01-01",
        "phone": None,
        "password_hash": None,
        "pin_hash": None,
        "pin_code": None,
    })
    payload = {
        "sub": "usr_sa_01",
        "fam": "fam_01",
        "role": "guardian",
        "name": "测试用户",
        "birth": "1990-01-01",
    }
    token = encode_jwt(payload, jwt_secret())

    # 1. 模拟标准 Sa-Token header: satoken: <token>
    ctx = get_current_user(satoken=token)
    assert ctx.user_id == "usr_sa_01"
    assert ctx.role == UserRole.GUARDIAN

    # 2. 模拟标准 Authorization: Bearer <token>
    ctx_bearer = get_current_user(authorization=f"Bearer {token}")
    assert ctx_bearer.user_id == "usr_sa_01"


def test_jwt_expired_token_fails():
    """异常边界：已过期的 JWT 必须抛出 ValueError 并拒绝解析"""
    payload = {"sub": "usr_test", "fam": "fam_test"}
    # 立即过期 (-10秒)
    token = encode_jwt(payload, SECRET_KEY, expires_in=-10)
    with pytest.raises(ValueError, match="expired"):
        decode_jwt(token, SECRET_KEY)


def test_jwt_tampered_signature_fails():
    """安全防御：签名被篡改的 JWT 必须被拒绝"""
    payload = {"sub": "usr_test", "fam": "fam_test"}
    token = encode_jwt(payload, SECRET_KEY, expires_in=3600)
    parts = token.split(".")
    # 篡改载荷或签名
    tampered_token = f"{parts[0]}.{parts[1]}.badsignature"
    with pytest.raises(ValueError, match="signature verification failed"):
        decode_jwt(tampered_token, SECRET_KEY)


def test_jwt_wrong_secret_fails():
    """安全防御：使用错误的密钥解码必须失败"""
    token = encode_jwt({"sub": "usr_test"}, SECRET_KEY, expires_in=3600)
    with pytest.raises(ValueError, match="signature verification failed"):
        decode_jwt(token, "wrong-secret-key-32-bytes-minimum!")


def test_password_hash_and_verify():
    """密码哈希与防时序攻击验证"""
    plain = "MySecretPass123"
    hashed = hash_password(plain)
    assert hashed != plain
    assert "$" in hashed

    # 正确密码比对
    assert verify_password(plain, hashed) is True
    # 错误密码比对
    assert verify_password("WrongPass", hashed) is False
    # 恶意篡改哈希比对
    assert verify_password(plain, "corrupted_hash") is False


def test_schema_register_validation():
    """契约验证：注册参数校验"""
    # 正常注册数据
    valid = RegisterRequest(
        phone="13800138000",
        password="secretpassword",
        family_name="幸福一家人",
        nickname="小敏",
        birth_date="1992-05-10",
    )
    assert valid.phone == "13800138000"

    # 非法手机号（非11位）应触发 ValidationError
    with pytest.raises(ValidationError):
        RegisterRequest(
            phone="13800",
            password="secretpassword",
            family_name="幸福一家人",
            nickname="小敏",
            birth_date="1992-05-10",
        )


def test_get_family_by_id_annotation_resolves():
    """回归：注解里的 Optional 必须已导入，否则 get_type_hints 会 NameError。"""
    from typing import get_type_hints

    from app.api.v1.auth import get_family_by_id

    hints = get_type_hints(get_family_by_id)
    assert hints["return"] == (dict | None)
    assert get_family_by_id("fam_missing") is None


def test_schema_member_add_validation():
    """契约验证：添加家庭成员参数校验"""
    member = MemberAddRequest(
        nickname="外婆",
        role=FamilyMemberRole.ELDER,
        birth_date="1958-06-18",
        pin_code="1234",
    )
    assert member.role == FamilyMemberRole.ELDER
    assert member.pin_code == "1234"
