"""FastAPI 认证与权限依赖注入模块 (Dependencies & Guards)

提供当前用户上下文解析、基于角色的访问控制（RBAC）守卫。
身份以内存用户表为准：已删除的成员，其 JWT 立即失效。
"""
from __future__ import annotations

from typing import Callable, List

from fastapi import Depends, Header, HTTPException, status

from app.core.runtime_secrets import jwt_secret
from app.core.security import decode_jwt
from app.schemas.auth import UserContext, UserRole


def get_current_user(
    authorization: str = Header(default=""),
    satoken: str = Header(default=""),
) -> UserContext:
    """提取访问令牌并解析为强类型用户上下文。

    支持两种请求头：
    1. Header `satoken: <jwt>`
    2. Header `Authorization: Bearer <jwt>`
    """
    auth_val = authorization if isinstance(authorization, str) else ""
    sa_val = satoken if isinstance(satoken, str) else ""

    token = ""
    if sa_val.strip():
        token = sa_val.strip()
    elif auth_val.strip():
        if auth_val.startswith("Bearer "):
            token = auth_val[7:].strip()
        else:
            token = auth_val.strip()

    if not token:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="缺少或无效的认证头 (支持 satoken 或 Authorization: Bearer <token>)",
        )
    secret = jwt_secret()
    try:
        claims = decode_jwt(token, secret)
    except ValueError as e:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail=f"无效或已过期的访问令牌: {str(e)}",
        ) from e

    if not claims.get("sub") or not claims.get("fam") or not claims.get("role"):
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="令牌缺失必要身份字段 (sub/fam/role)",
        )

    # 延迟导入，避免 deps ↔ auth 循环依赖。
    from app.api.v1.auth import _USERS

    user = _USERS.get(str(claims["sub"]))
    if (
        not user
        or str(user.get("family_id")) != str(claims.get("fam"))
        or str(user.get("role")) != str(claims.get("role"))
    ):
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="无效或已过期的访问令牌",
        )

    try:
        role = UserRole(str(user["role"]))
    except ValueError as e:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="无效或已过期的访问令牌",
        ) from e

    return UserContext(
        user_id=str(user["id"]),
        family_id=str(user["family_id"]),
        role=role,
        nickname=str(user.get("nickname") or ""),
        birth_date=str(user.get("birth_date") or ""),
    )


def require_roles(allowed_roles: List[UserRole]) -> Callable[[UserContext], UserContext]:
    """RBAC 角色守卫。内部依赖 get_current_user，路由用 Depends(require_roles([...]))。"""

    def role_checker(u: UserContext = Depends(get_current_user)) -> UserContext:
        if u.role not in allowed_roles:
            allowed = ", ".join(role.value for role in allowed_roles)
            raise HTTPException(
                status_code=status.HTTP_403_FORBIDDEN,
                detail=(
                    f"权限不足：当前角色 [{u.role.value}] 无权执行此操作，仅允许 [{allowed}]"
                ),
            )
        return u

    return role_checker


def require_admin(u: UserContext = Depends(require_roles([UserRole.ADMIN]))) -> UserContext:
    """平台超级管理员专用权限守卫。"""
    return u
