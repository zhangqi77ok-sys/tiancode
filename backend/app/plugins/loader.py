"""伴暖插件装载初始化模块 (Plugin Loader)

在系统启动时将内置长辈与儿童插件注册入 PluginRegistry。
"""
from __future__ import annotations

from app.plugins.builtin_child import (
    HabitChallengePlugin,
    InteractiveStoryPlugin,
    WonderEncyclopediaPlugin,
)
from app.plugins.builtin_elder import (
    MedicationPlugin,
    MemoryKeeperPlugin,
    OperaPlugin,
)
from app.plugins.registry import plugin_registry


def load_builtin_plugins() -> None:
    """载入所有官方伴暖预置插件（幂等安全）"""
    plugins = [
        # 长辈专属
        OperaPlugin(),
        MemoryKeeperPlugin(),
        MedicationPlugin(),
        # 儿童专属
        InteractiveStoryPlugin(),
        HabitChallengePlugin(),
        WonderEncyclopediaPlugin(),
    ]

    for p in plugins:
        if not plugin_registry.get_plugin(p.id):
            plugin_registry.register(p)
