"""核心安全与鉴权工具模块：基于工业级 PyJWT 提供 HS256 JWT 签发与 30 天超长会话校验。

包含：
1. RFC 7519 规范标准 HS256 JWT 签发（默认 30 天过期，支持家庭端免频密登录）
2. JWT 签名与过期完整性验证（基于 PyJWT 工业级内核）
3. PBKDF2-HMAC-SHA256 安全密码哈希与防时序攻击验证
"""
from __future__ import annotations

import base64
import hashlib
import hmac
import secrets
import time
from typing import Any, Dict
import jwt

# 默认 30 天超长有效期 (30 * 24 * 3600 秒 = 2,592,000 秒)
DEFAULT_EXPIRES_IN = 86400 * 30
JWT_ALGORITHM = "HS256"


def encode_jwt(payload: Dict[str, Any], secret: str, expires_in: int = DEFAULT_EXPIRES_IN) -> str:
    """使用 PyJWT 签发标准 HS256 JWT 令牌（默认 30 天超长有效期）。

    :param payload: 载荷数据（必须包含 sub 与 fam）
    :param secret: 签名密钥（建议至少 32 字节）
    :param expires_in: 有效期（秒），默认 30 天
    :return: JWT 字符串
    """
    now = int(time.time())
    data = dict(payload)
    data.setdefault("iat", now)
    data.setdefault("exp", now + expires_in)
    data.setdefault("jti", secrets.token_hex(16))

    return jwt.encode(data, secret, algorithm=JWT_ALGORITHM)


def decode_jwt(token: str, secret: str) -> Dict[str, Any]:
    """验证并解码 HS256 JWT 令牌。

    :param token: 待验证的 JWT 字符串
    :param secret: 签名密钥
    :return: 解密后的载荷字典
    :raises ValueError: 格式错误、签名不匹配或已过期
    """
    try:
        return jwt.decode(token, secret, algorithms=[JWT_ALGORITHM])
    except jwt.ExpiredSignatureError as e:
        raise ValueError("JWT token has expired") from e
    except jwt.InvalidTokenError as e:
        raise ValueError(f"JWT signature verification failed: {str(e)}") from e


def b64url_encode(data: bytes) -> str:
    """Base64Url 编码（去除末尾填充符）"""
    return base64.urlsafe_b64encode(data).decode("utf-8").rstrip("=")


def b64url_decode(s: str) -> bytes:
    """Base64Url 解码（自动补全末尾填充符）"""
    padding = "=" * (-len(s) % 4)
    return base64.urlsafe_b64decode(s + padding)


def hash_password(password: str, salt: str | None = None) -> str:
    """使用 PBKDF2-HMAC-SHA256 生成安全的密码哈希。"""
    if salt is None:
        salt = secrets.token_urlsafe(16)
    dk = hashlib.pbkdf2_hmac("sha256", password.encode("utf-8"), salt.encode("utf-8"), 100000)
    return f"{salt}${b64url_encode(dk)}"


def verify_password(plain_password: str, hashed_password: str) -> bool:
    """校验密码，采用恒定时间比对防御时序攻击。"""
    try:
        salt, dk_str = hashed_password.split("$", 1)
        expected_dk = b64url_decode(dk_str)
        actual_dk = hashlib.pbkdf2_hmac("sha256", plain_password.encode("utf-8"), salt.encode("utf-8"), 100000)
        return hmac.compare_digest(actual_dk, expected_dk)
    except Exception:
        return False
