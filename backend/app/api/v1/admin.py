"""平台超级管理员中枢路由模块 (Admin API Router)

包含：
1. POST /api/v1/admin/login: 平台管理员认证登入
2. GET  /api/v1/admin/plugins: 获取全平台插件清单与调用量
3. PUT  /api/v1/admin/plugins/{plugin_id}/toggle: 全局紧急熔断/恢复指定插件
4. GET  /api/v1/admin/dashboard/stats: 家庭与插件计数
"""
from __future__ import annotations

import hmac
from typing import List
from fastapi import APIRouter, Depends, HTTPException, status

from app.api.deps import require_admin
from app.api.v1.auth import (
    clear_login_failures,
    enforce_login_attempt,
    ensure_platform_admin,
    get_all_families_count,
    record_login_failure,
)
from app.core.runtime_secrets import admin_password, admin_username, jwt_secret
from app.core.security import DEFAULT_EXPIRES_IN, encode_jwt
from app.core.store import store
from app.schemas.common import ErrorBody
from app.plugins.loader import load_builtin_plugins
from app.plugins.registry import plugin_registry
from app.schemas.admin import (
    AdminDashboardStats,
    AdminPluginStatus,
    AdminPluginToggleRequest,
    AdminPluginToggleResult,
)
from app.schemas.auth import AdminLoginRequest, TokenResponse, UserContext, UserRole

# 确保预置插件均已初始化
load_builtin_plugins()

router = APIRouter(
    prefix="/api/v1/admin",
    responses={
        401: {"model": ErrorBody, "description": "未登录或令牌失效"},
        403: {"model": ErrorBody, "description": "不是平台管理员"},
    },
)


def _credentials_match(left: str, right: str) -> bool:
    if len(left) != len(right):
        return False
    return hmac.compare_digest(left, right)


@router.post("/login", response_model=TokenResponse)
def admin_login(req: AdminLoginRequest):
    """平台超级管理员专用登入接口。口令只来自环境变量。"""
    key = f"admin:{req.username}"
    enforce_login_attempt(key)
    username_ok = _credentials_match(req.username, admin_username())
    password_ok = _credentials_match(req.password, admin_password())
    if not (username_ok and password_ok):
        record_login_failure(key)
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="管理员用户名或密码错误",
        )
    clear_login_failures(key)
    admin = ensure_platform_admin()
    token = encode_jwt(
        {
            "sub": admin["id"],
            "fam": admin["family_id"],
            "role": UserRole.ADMIN.value,
            "name": admin["nickname"],
            "birth": admin["birth_date"],
        },
        jwt_secret(),
    )
    return TokenResponse(
        access_token=token,
        expires_in=DEFAULT_EXPIRES_IN,
        role=UserRole.ADMIN,
        user_id=admin["id"],
        family_id=admin["family_id"],
        name=admin["nickname"],
    )


@router.get("/plugins", response_model=List[AdminPluginStatus])
def list_admin_plugins(u: UserContext = Depends(require_admin)):
    """获取全平台插件清单、全局熔断状态与历史调用量。开关和次数各查一次。"""
    manifests = plugin_registry.list_manifests()
    plugin_ids = [m.id for m in manifests]
    enabled = plugin_registry.global_enabled_map(plugin_ids)
    counts = plugin_registry.invocation_counts(plugin_ids)
    results = []
    for m in manifests:
        results.append(
            AdminPluginStatus(
                id=m.id,
                name=m.name,
                version=m.version,
                category=m.category.value,
                allowed_roles=[r.value for r in m.allowed_roles],
                card_template=m.card_template.value,
                global_enabled=enabled.get(m.id, True),
                invocations_count=counts.get(m.id, 0),
            )
        )
    return results


@router.put("/plugins/{plugin_id}/toggle", response_model=AdminPluginToggleResult)
def toggle_global_plugin(
    plugin_id: str,
    req: AdminPluginToggleRequest,
    u: UserContext = Depends(require_admin),
):
    """全局紧急熔断或恢复全平台指定插件。"""
    plugin = plugin_registry.get_plugin(plugin_id)
    if not plugin:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND,
            detail=f"未找到插件: {plugin_id}",
        )

    plugin_registry.set_global_status(plugin_id, req.global_enabled)
    return {
        "status": "ok",
        "plugin_id": plugin_id,
        "global_enabled": req.global_enabled,
    }


@router.get("/dashboard/stats", response_model=AdminDashboardStats)
def get_admin_dashboard_stats(u: UserContext = Depends(require_admin)):
    """家庭数、成员数、未熔断插件数。没有模型调用计数。成员数按角色在数据库里聚合。"""
    counts = store.count_users_by_role()
    total_elders = counts.get(UserRole.ELDER.value, 0)
    total_children = counts.get(UserRole.CHILD.value, 0)
    total_guardians = counts.get(UserRole.GUARDIAN.value, 0)

    manifests = plugin_registry.list_manifests()
    flags = plugin_registry.global_enabled_map([item.id for item in manifests])
    active_count = sum(1 for item in manifests if flags.get(item.id, True))

    return AdminDashboardStats(
        total_families=get_all_families_count(),
        total_elders=total_elders,
        total_children=total_children,
        total_guardians=total_guardians,
        active_plugins_count=active_count,
    )
