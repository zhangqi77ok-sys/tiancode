"""核心安全与鉴权工具模块：提供标准 HS256 JWT 签发与解码、密码哈希与防时序攻击验证。

设计理念（Code Judo）：
采用 Python 标准库 hmac, hashlib, base64 实现无第三方依赖的 RFC 7519 规范 JWT，
杜绝环境依赖飘移与漏洞风险。
"""
from __future__ import annotations

import base64
import hashlib
import hmac
import json
import time
from typing import Any, Dict


def b64url_encode(data: bytes) -> str:
    """Base64Url 编码（去除末尾填充符）"""
    return base64.urlsafe_b64encode(data).decode("utf-8").rstrip("=")


def b64url_decode(s: str) -> bytes:
    """Base64Url 解码（自动补全末尾填充符）"""
    padding = "=" * (-len(s) % 4)
    return base64.urlsafe_b64decode(s + padding)


def encode_jwt(payload: Dict[str, Any], secret: str, expires_in: int = 86400 * 7) -> str:
    """签发标准 HS256 JWT 令牌。

    :param payload: 载荷数据（必须包含 sub 与 fam）
    :param secret: 签名密钥（建议至少 32 字节）
    :param expires_in: 有效期（秒），默认 7 天
    :return: JWT 字符串
    """
    header = {"alg": "HS256", "typ": "JWT"}
    now = int(time.time())
    data = dict(payload)
    data.setdefault("iat", now)
    data.setdefault("exp", now + expires_in)

    h_str = b64url_encode(json.dumps(header, separators=(",", ":")).encode("utf-8"))
    p_str = b64url_encode(json.dumps(data, separators=(",", ":")).encode("utf-8"))
    signing_input = f"{h_str}.{p_str}".encode("utf-8")

    signature = hmac.new(secret.encode("utf-8"), signing_input, hashlib.sha256).digest()
    sig_str = b64url_encode(signature)

    return f"{h_str}.{p_str}.{sig_str}"


def decode_jwt(token: str, secret: str) -> Dict[str, Any]:
    """验证并解码 HS256 JWT 令牌。

    :param token: 待验证的 JWT 字符串
    :param secret: 签名密钥
    :return: 解密后的载荷字典
    :raises ValueError: 格式错误、签名不匹配或已过期
    """
    parts = token.split(".")
    if len(parts) != 3:
        raise ValueError("Invalid JWT token format")

    h_str, p_str, sig_str = parts
    signing_input = f"{h_str}.{p_str}".encode("utf-8")
    expected_sig = hmac.new(secret.encode("utf-8"), signing_input, hashlib.sha256).digest()

    actual_sig = b64url_decode(sig_str)
    if not hmac.compare_digest(actual_sig, expected_sig):
        raise ValueError("JWT signature verification failed")

    payload = json.loads(b64url_decode(p_str).decode("utf-8"))
    now = time.time()

    if "exp" in payload and payload["exp"] < now:
        raise ValueError("JWT token has expired")

    return payload


def hash_password(password: str, salt: str | None = None) -> str:
    """使用 PBKDF2-HMAC-SHA256 生成安全的密码哈希。"""
    if salt is None:
        salt = base64.b64encode(hashlib.sha256(str(time.time()).encode("utf-8")).digest()[:16]).decode("utf-8")
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
