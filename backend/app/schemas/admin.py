"""平台管理员后台契约。只包含真实存在的家庭计数和插件熔断。"""
from __future__ import annotations

from typing import List
from pydantic import BaseModel, Field


class AdminPluginStatus(BaseModel):
    """管理员视角的插件运行状态"""
    id: str = Field(..., description="插件 ID")
    name: str = Field(..., description="插件中文名")
    version: str = Field(..., description="版本")
    category: str = Field(..., description="分类")
    allowed_roles: List[str] = Field(..., description="适用角色")
    card_template: str = Field(..., description="卡片模板类型")
    global_enabled: bool = Field(True, description="全平台全局开启状态（False 表示已紧急熔断）")
    invocations_count: int = Field(0, description="历史累计调用次数")


class AdminPluginToggleRequest(BaseModel):
    """插件全局熔断/开启切换请求"""
    global_enabled: bool = Field(..., description="是否全局开启（False 为全局熔断关停）")


class AdminDashboardStats(BaseModel):
    """全平台中控大盘。数字来自 SQLite 和插件注册表，没有模型调用计数。"""
    total_families: int = Field(0, description="已注册家庭空间总数")
    total_elders: int = Field(0, description="平台纳管银发长辈总数")
    total_children: int = Field(0, description="平台纳管幼童宝贝总数")
    total_guardians: int = Field(0, description="平台注册守护者总数")
    active_plugins_count: int = Field(0, description="当前全平台未熔断的插件数量")
