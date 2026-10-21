"""插件单例注册中心与调度引擎 (Plugin Registry & Dispatcher)

负责管理插件生命周期、发现注册、多租户/角色权限过滤及动作安全分发。
"""
from __future__ import annotations

import logging
from typing import Any, Dict, List, Optional

from app.core.store import store
from app.plugins.base import BasePlugin
from app.schemas.auth import UserContext, UserRole
from app.schemas.plugin import PluginActionResult, PluginCardData, PluginManifest

logger = logging.getLogger(__name__)


class PluginRegistry:
    """伴暖插件注册中心

    管理所有已载入插件，执行 RBAC 权限隔离、家庭开关过滤与动作调度。
    """

    def __init__(self) -> None:
        self._plugins: Dict[str, BasePlugin] = {}
        self._global_enabled: Dict[str, bool] = {}
        self._invocations: Dict[str, int] = {}

    def set_global_status(self, plugin_id: str, enabled: bool) -> None:
        """管理员设置插件全局开启/熔断状态"""
        if plugin_id not in self._plugins:
            raise ValueError(f"未找到插件: {plugin_id}")
        store.kv_set(f"plugin_global:{plugin_id}", "1" if enabled else "0")
        self._global_enabled[plugin_id] = enabled
        logger.warning(f"管理员变更插件全局状态: [{plugin_id}] -> enabled={enabled}")

    def is_global_enabled(self, plugin_id: str) -> bool:
        """检查插件是否处于全局开启状态（未显式关闭时默认为 True）。开关在 SQLite。"""
        raw = store.kv_get(f"plugin_global:{plugin_id}")
        if raw is None:
            return True
        return raw == "1"

    def get_invocation_count(self, plugin_id: str) -> int:
        """获取插件历史调用量。次数写在 SQLite，重启后还在。"""
        raw = store.kv_get(f"plugin_invocations:{plugin_id}")
        if raw is None:
            return 0
        try:
            return int(raw)
        except ValueError:
            return 0

    def register(self, plugin: BasePlugin) -> None:
        """注册插件到中心

        :param plugin: 继承自 BasePlugin 的插件实例
        :raises ValueError: 当插件 ID 重复时抛出异常
        """
        pid = plugin.id
        if pid in self._plugins:
            raise ValueError(f"插件 ID 已存在，禁止重复注册: {pid}")
        self._plugins[pid] = plugin
        logger.info(f"成功注册伴暖插件: [{pid}] {plugin.name}")

    def get_plugin(self, plugin_id: str) -> Optional[BasePlugin]:
        """按 ID 检索插件实例"""
        return self._plugins.get(plugin_id)

    def list_manifests(self, role: Optional[UserRole] = None) -> List[PluginManifest]:
        """获取所有插件清单，可按用户角色过滤，按 sort_order 升序排列"""
        manifests = []
        for plugin in self._plugins.values():
            if role is None or plugin.can_access(role):
                manifests.append(plugin.manifest)
        return sorted(manifests, key=lambda m: m.sort_order)

    async def get_active_cards(
        self,
        context: UserContext,
        family_settings: Dict[str, Any],
    ) -> List[PluginCardData]:
        """获取当前用户在当前家庭下激活的所有插件卡片流

        过滤逻辑：
        1. 角色白名单过滤：排除当前角色无权访问的插件；
        2. 家庭开关过滤：若守护者在家庭设置中关闭该插件，则跳过卡片渲染；
        3. 按权重排序：调用各插件的 render_card 生成视图数据，并按 sort_order 排序。
        """
        enabled_map = family_settings.get("plugins", {})
        cards: List[PluginCardData] = []

        for plugin in self._plugins.values():
            # 1. 管理员全局熔断拦截
            if not self.is_global_enabled(plugin.id):
                continue

            # 2. 严格角色权限校验
            if not plugin.can_access(context.role):
                continue

            # 3. 家庭级别启用/停用判定（未显式配置时采用插件默认开关）
            is_enabled = enabled_map.get(plugin.id, plugin.manifest.default_enabled)
            if not is_enabled:
                continue

            # 4. 渲染卡片
            card_data = await plugin.render_card(context, family_settings)
            cards.append(card_data)

        return sorted(cards, key=lambda c: c.manifest.sort_order)

    async def dispatch_action(
        self,
        plugin_id: str,
        action_name: str,
        payload: Dict[str, Any],
        context: UserContext,
        family_settings: Optional[Dict[str, Any]] = None,
    ) -> PluginActionResult:
        """安全调度插件动作

        安全防线：
        1. 存在性检查：插件不存在时抛出 ValueError；
        2. 全局熔断检查：若管理员全局停用该插件，抛出 PermissionError；
        3. 家庭开关：守护者停用后拒绝动作；
        4. RBAC 越权拦截：当前角色无权访问该插件时抛出 PermissionError；
        5. 委托执行：调用插件的 execute_action 处理业务逻辑并记录调用量。
        """
        plugin = self.get_plugin(plugin_id)
        if not plugin:
            raise ValueError(f"未找到指定的插件: {plugin_id}")

        if not self.is_global_enabled(plugin_id):
            raise PermissionError(f"插件 [{plugin.name}] 已被平台管理员全局紧急熔断下线")

        if family_settings is not None:
            enabled_map = family_settings.get("plugins", {})
            is_enabled = enabled_map.get(plugin.id, plugin.manifest.default_enabled)
            if not is_enabled:
                raise PermissionError(f"插件 [{plugin.name}] 已在当前家庭中停用")

        if not plugin.can_access(context.role):
            raise PermissionError(
                f"权限不足：当前角色 [{context.role.value}] 无权操作插件 [{plugin.name}]"
            )

        count = self.get_invocation_count(plugin_id) + 1
        store.kv_set(f"plugin_invocations:{plugin_id}", str(count))
        self._invocations[plugin_id] = count
        return await plugin.execute_action(action_name, payload, context)


# 全局单例注册中心实例
plugin_registry = PluginRegistry()
