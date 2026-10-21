"""插件化契约与元数据定义模块 (Pydantic Schemas)

定义插件清单 (Manifest)、卡片模板枚举、动作请求与响应契约。
严格为各字段补全中文注释与类型声明。
"""
from __future__ import annotations

from enum import Enum
from typing import Any, Dict, List
from pydantic import BaseModel, Field

from app.schemas.auth import UserRole


class PluginCategory(str, Enum):
    """插件分类枚举"""
    ENTERTAINMENT = "entertainment"  # 文娱陪伴（戏曲、相声等）
    HEALTH = "health"                # 健康守护（用药、就医、睡眠等）
    GROWTH = "growth"                # 成长启蒙（故事机、百科、好习惯等）
    FAMILY = "family"                # 亲情互动（时光册、看护简报、留言等）


class CardTemplateType(str, Enum):
    """前端卡片插槽渲染模板枚举"""
    MEDIA_PLAYER = "media_player"          # 黑胶/音频播放器卡片
    DECISION_STORY = "decision_story"      # 双向分幕决策故事卡片
    STAR_TRACKER = "star_tracker"          # 好习惯星星收集器卡片
    INTERACTIVE_PROMPT = "interactive_prompt"  # 口述时光回忆问答卡片


class PluginManifest(BaseModel):
    """插件核心元数据清单 (Plugin Manifest)"""
    id: str = Field(..., description="插件全局唯一标识符，如 elder_opera")
    name: str = Field(..., description="插件中文名，如 经典戏曲留声机")
    version: str = Field("1.0.0", description="插件语义化版本号")
    category: PluginCategory = Field(..., description="插件分类")
    allowed_roles: List[UserRole] = Field(..., description="允许使用该插件的家庭角色列表")
    card_template: CardTemplateType = Field(..., description="前端渲染卡片模板类型")
    title: str = Field(..., description="卡片主标题，如 经典名段播放")
    description: str = Field(..., description="插件功能详述，帮助家庭了解其用途")
    icon: str = Field(..., description="图标标识（Emoji 或 SVG 名称）")
    default_enabled: bool = Field(True, description="新家庭创建时是否默认开启")
    sort_order: int = Field(100, description="卡片流排序权重（越小越靠前）")


class PluginActionRequest(BaseModel):
    """插件动作执行请求契约"""
    action_name: str = Field(..., description="触发的动作名称，如 next_track, select_branch")
    payload: Dict[str, Any] = Field(default_factory=dict, description="动作携带的动态参数字典")


class PluginActionResult(BaseModel):
    """插件动作执行返回结果契约"""
    status: str = Field("ok", description="执行状态：ok 成功，error 失败")
    message: str = Field("", description="面向用户的提示文本或语音念白")
    data: Dict[str, Any] = Field(default_factory=dict, description="动作附带的数据结果")


class PluginCardData(BaseModel):
    """动态卡片流输出契约（用于前端插槽渲染）"""
    plugin_id: str = Field(..., description="对应的插件 ID")
    manifest: PluginManifest = Field(..., description="插件清单元数据")
    data: Dict[str, Any] = Field(default_factory=dict, description="卡片实时渲染所需的业务数据")
    enabled: bool = Field(True, description="该插件在当前家庭中的开启状态")
