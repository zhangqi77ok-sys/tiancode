"""FastAPI 认证与权限依赖注入模块 (Dependencies & Guards)

提供当前用户上下文解析、基于角色的访问控制（RBAC）守卫。
"""
from __future__ import annotations

import os
from typing import Callable, List
from fastapi import Depends, Header, HTTPException, status

from app.core.security import decode_jwt
from app.schemas.auth import UserContext, UserRole

JWT_SECRET = os.getenv("JWT_SECRET", "tiancode-family-secret-32-bytes-min!")


def get_current_user(authorization: str = Header(default="")) -> UserContext:
    """从请求头提取 Bearer JWT 并解析为强类型用户上下文。

    身份仅来自 JWT 载荷，绝不允许在 Body 或 Query 中伪造。
    """
    if not authorization.startswith("Bearer "):
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="缺少或无效的认证头 (Authorization: Bearer <token>)",
        )

    token = authorization[7:].strip()
    try:
        claims = decode_jwt(token, JWT_SECRET)
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

    return UserContext(
        user_id=str(claims["sub"]),
        family_id=str(claims["fam"]),
        role=UserRole(claims["role"]),
        nickname=str(claims.get("name", "")),
        birth_date=str(claims.get("birth", "")),
    )


def require_roles(allowed_roles: List[UserRole]) -> Callable[[UserContext], UserContext]:
    """RBAC 角色权限守卫装饰依赖。

    若当前调用者的角色不在允许的角色列表中，立即拦截并抛出 403 Forbidden。
    """
    def role_checker(u: UserContext = None) -> UserContext:
        if u.role not in allowed_roles:
            raise HTTPException(
                status_code=status.HTTP_403_FORBIDDEN,
                detail=f"权限不足：当前角色 [{u.role.value}] 无权执行此操作，仅允许 [{', '.join(r.value for r in allowed_roles)}]",
            )
        return u

    return role_checker


def require_admin(u: UserContext = Depends(get_current_user)) -> UserContext:
    """平台超级管理员专用权限守卫依赖。

    验证当前调用者是否具备 ADMIN 角色，若非管理员则抛出 403 Forbidden。
    """
    if u.role != UserRole.ADMIN:
        raise HTTPException(
            status_code=status.HTTP_403_FORBIDDEN,
            detail="权限不足：仅平台超级管理员可执行此管控操作",
        )
    return u
