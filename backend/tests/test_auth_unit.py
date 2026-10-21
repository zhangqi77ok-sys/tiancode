"""认证与安全核心单元测试（TDD 第一阶段）

验证 HS256 JWT 生成/验证、密码哈希与防篡改、以及 Pydantic 契约校验。
"""
import time
import pytest
from pydantic import ValidationError

from app.core.security import decode_jwt, encode_jwt, hash_password, verify_password
from app.schemas.auth import (
    MemberAddRequest,
    RegisterRequest,
    UserRole,
)

SECRET_KEY = "test-secret-key-for-unit-tests-32bytes-min!"


def test_jwt_encode_and_decode_success():
    """正常路径：验证 JWT 签发与解码能够完整无损还原载荷"""
    payload = {
        "sub": "usr_elder_1001",
        "fam": "fam_beijing_01",
        "role": UserRole.ELDER.value,
        "name": "外婆",
        "birth": "1958-06-18",
    }
    token = encode_jwt(payload, SECRET_KEY, expires_in=3600)
    assert isinstance(token, str)
    assert len(token.split(".")) == 3

    decoded = decode_jwt(token, SECRET_KEY)
    assert decoded["sub"] == "usr_elder_1001"
    assert decoded["fam"] == "fam_beijing_01"
    assert decoded["role"] == "elder"
    assert decoded["name"] == "外婆"
    assert decoded["birth"] == "1958-06-18"
    assert "exp" in decoded


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


def test_schema_member_add_validation():
    """契约验证：添加家庭成员参数校验"""
    member = MemberAddRequest(
        nickname="外婆",
        role=UserRole.ELDER,
        birth_date="1958-06-18",
        pin_code="1234",
    )
    assert member.role == UserRole.ELDER
    assert member.pin_code == "1234"
