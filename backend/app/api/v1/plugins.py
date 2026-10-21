"""伴暖插件化前端交互与调度路由模块 (FastAPI Router)

包含：
1. GET  /api/v1/plugins/active: 获取当前用户激活的动态插件卡片流
2. POST /api/v1/plugins/{plugin_id}/action: 触发指定插件业务动作
3. GET  /api/v1/family/plugins: 守护者查看家庭插件库及其开启状态
4. PUT  /api/v1/family/plugins/{plugin_id}/toggle: 守护者开启/停用家庭插件
"""
from __future__ import annotations

from typing import List
from fastapi import APIRouter, Depends, HTTPException, status
from pydantic import BaseModel, Field

from app.api.deps import get_current_user, require_roles
from app.api.v1.auth import get_family_by_id, save_family
from app.plugins.loader import load_builtin_plugins
from app.plugins.registry import plugin_registry
from app.schemas.auth import UserContext, UserRole
from app.schemas.plugin import (
    PluginActionRequest,
    PluginActionResult,
    PluginCardData,
    PluginManifest,
)

# 确保预置插件均已初始化载入
load_builtin_plugins()

router = APIRouter(prefix="/api/v1")


class FamilyPluginStatusOut(BaseModel):
    """家庭插件状态视图模型"""
    manifest: PluginManifest
    enabled: bool = Field(..., description="在当前家庭中是否开启")


class FamilyPluginToggleRequest(BaseModel):
    """家庭插件开关请求"""
    enabled: bool = Field(..., description="是否开启")


@router.get("/plugins/active", response_model=List[PluginCardData])
async def get_active_plugin_cards(u: UserContext = Depends(get_current_user)):
    """获取当前登录使用者对应角色的已激活卡片流。

    支持多租户隔离、家庭开关过滤与管理员全局熔断拦截。
    """
    fam = get_family_by_id(u.family_id) or {}
    settings = fam.get("settings", {})
    return await plugin_registry.get_active_cards(u, settings)


@router.post("/plugins/{plugin_id}/action", response_model=PluginActionResult)
async def dispatch_plugin_action(
    plugin_id: str,
    req: PluginActionRequest,
    u: UserContext = Depends(get_current_user),
):
    """触发指定插件的业务动作（如切换戏曲、做出故事分支决策、打卡星星）。

    内置 RBAC 防越权守卫与全局熔断拦截。
    """
    fam = get_family_by_id(u.family_id) or {}
    settings = fam.get("settings", {})
    try:
        return await plugin_registry.dispatch_action(
            plugin_id=plugin_id,
            action_name=req.action_name,
            payload=req.payload,
            context=u,
            family_settings=settings,
        )
    except ValueError as e:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail=str(e))
    except PermissionError as e:
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail=str(e))


@router.get("/family/plugins", response_model=List[FamilyPluginStatusOut])
def list_family_plugins(u: UserContext = Depends(require_roles([UserRole.GUARDIAN]))):
    """获取当前家庭的所有可用插件及开关状态（仅家庭守护者 Guardian 可访问）。"""
    fam = get_family_by_id(u.family_id)
    if not fam:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="家庭空间不存在")

    fam_plugins = fam.get("settings", {}).setdefault("plugins", {})
    manifests = plugin_registry.list_manifests()

    results = []
    for m in manifests:
        is_enabled = fam_plugins.get(m.id, m.default_enabled)
        results.append(FamilyPluginStatusOut(manifest=m, enabled=is_enabled))
    return results


@router.put("/family/plugins/{plugin_id}/toggle")
def toggle_family_plugin(
    plugin_id: str,
    req: FamilyPluginToggleRequest,
    u: UserContext = Depends(require_roles([UserRole.GUARDIAN])),
):
    """守护者为家庭开启或停用特定插件。"""
    fam = get_family_by_id(u.family_id)
    if not fam:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="家庭空间不存在")

    plugin = plugin_registry.get_plugin(plugin_id)
    if not plugin:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail=f"未找到插件: {plugin_id}")

    fam_plugins = fam.setdefault("settings", {}).setdefault("plugins", {})
    fam_plugins[plugin_id] = req.enabled
    save_family(fam)
    return {"status": "ok", "plugin_id": plugin_id, "enabled": req.enabled}
