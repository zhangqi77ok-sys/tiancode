"""进程启动时拒绝缺失或仍等于仓库公开占位符的密钥。"""
from __future__ import annotations

import os

# 这些值出现在旧源码、README 或 deploy/.env.example 里，不能当线上密钥。
INSECURE_JWT_SECRETS = frozenset(
    {
        "",
        "tiancode-family-secret-32-bytes-min!",
        "change-me",
        "change-me-openssl-rand-hex-32",
    }
)
INSECURE_ADMIN_PASSWORDS = frozenset(
    {
        "",
        "AdminPassword123!",
        "change-me",
    }
)

MIN_JWT_SECRET_LENGTH = 32
MIN_ADMIN_PASSWORD_LENGTH = 12


def jwt_secret() -> str:
    return os.environ.get("JWT_SECRET", "")


def admin_username() -> str:
    return os.environ.get("ADMIN_USERNAME", "admin") or "admin"


def admin_password() -> str:
    return os.environ.get("ADMIN_PASSWORD", "")


def assert_runtime_secrets() -> None:
    """密钥缺失或仍是公开占位符时拒绝启动。"""
    secret = jwt_secret().strip()
    if secret in INSECURE_JWT_SECRETS or len(secret) < MIN_JWT_SECRET_LENGTH:
        raise RuntimeError(
            "JWT_SECRET is missing or insecure. Set a unique value of at least "
            "32 characters via the environment. Do not use the repository placeholder."
        )
    password = admin_password()
    if password.strip() in INSECURE_ADMIN_PASSWORDS or len(password) < MIN_ADMIN_PASSWORD_LENGTH:
        raise RuntimeError(
            "ADMIN_PASSWORD is missing or insecure. Set it via the environment. "
            "Do not use AdminPassword123! or change-me."
        )
