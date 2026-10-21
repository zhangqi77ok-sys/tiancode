"""插件抽象基类规范模块 (Base Plugin Interface)

所有伴暖业务插件均须继承 BasePlugin，实现标准的卡片渲染与动作执行协议。
"""
from __future__ import annotations

from abc import ABC, abstractmethod
from typing import Any, Dict

from app.schemas.auth import UserContext, UserRole
from app.schemas.plugin import PluginActionResult, PluginCardData, PluginManifest


class BasePlugin(ABC):
    """伴暖插件抽象基类

    核心规范：
    1. 声明并注入 manifest 元数据清单。
    2. 实现 render_card 契约，供前端动态插槽挂载。
    3. 实现 execute_action 契约，处理业务交互与状态流转。
    """

    def __init__(self, manifest: PluginManifest) -> None:
        self.manifest = manifest

    @property
    def id(self) -> str:
        """插件唯一 ID"""
        return self.manifest.id

    @property
    def name(self) -> str:
        """插件中文名"""
        return self.manifest.name

    def can_access(self, role: UserRole) -> bool:
        """检查指定角色是否具备访问/执行本插件的权限

        严格根据 manifest.allowed_roles 判定，防止角色越权。
        """
        return role in self.manifest.allowed_roles

    @abstractmethod
    async def render_card(self, context: UserContext, family_state: Dict[str, Any]) -> PluginCardData:
        """渲染当前家庭和使用者视角的卡片数据

        :param context: 当前调用者的用户认证上下文 (user_id, family_id, role, etc.)
        :param family_state: 当前家庭全局配置与共享状态字典
        :return: 包含最新业务数据的 PluginCardData 对象
        """
        pass

    @abstractmethod
    async def execute_action(
        self,
        action_name: str,
        payload: Dict[str, Any],
        context: UserContext,
    ) -> PluginActionResult:
        """执行插件动作（如切歌、做剧情分支决策、打卡星星等）

        :param action_name: 动作标识
        :param payload: 动作动态载荷
        :param context: 当前调用者的用户认证上下文
        :return: 执行结果 PluginActionResult 对象
        """
        pass
