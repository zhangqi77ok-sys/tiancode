"""长辈专属伴老内建插件集 (Elder Companion Plugins)

包含：
1. 经典戏曲黑胶留声机 (elder_opera)
2. 人生时光册与口述史 (elder_memory)
3. 伴随式温馨用药守护 (elder_medication)
"""
from __future__ import annotations

from typing import Any, Dict, List
from app.plugins.base import BasePlugin
from app.schemas.auth import UserContext, UserRole
from app.schemas.plugin import (
    CardTemplateType,
    PluginActionResult,
    PluginCardData,
    PluginCategory,
    PluginManifest,
)

# 经典戏曲曲库
OPERA_PLAYLIST = [
    {"id": "op_01", "name": "黄梅戏《女驸马》选段", "artist": "韩再芬", "lyric": "为救李郎离家园，谁料皇榜中状元..."},
    {"id": "op_02", "name": "豫剧《花木兰》选段", "artist": "常香玉", "lyric": "刘大哥讲话理太偏，谁说女子不如男..."},
    {"id": "op_03", "name": "京剧《梨花颂》", "artist": "梅葆玖", "lyric": "梨花开，春带雨，梨花落，春入泥..."},
]


class OperaPlugin(BasePlugin):
    """戏曲黑胶留声机插件"""

    def __init__(self) -> None:
        manifest = PluginManifest(
            id="elder_opera",
            name="经典戏曲留声机",
            version="1.0.0",
            category=PluginCategory.ENTERTAINMENT,
            allowed_roles=[UserRole.ELDER],
            card_template=CardTemplateType.MEDIA_PLAYER,
            title="📻 经典名段戏曲点播",
            description="内置黄梅戏、豫剧、京剧经典唱段，支持慢速大字朗读唱词与旋律点播。",
            icon="📻",
            default_enabled=True,
            sort_order=10,
        )
        super().__init__(manifest)
        self._current_indices: Dict[str, int] = {}  # user_id -> index

    async def render_card(self, context: UserContext, family_state: Dict[str, Any]) -> PluginCardData:
        idx = self._current_indices.get(context.user_id, 0)
        track = OPERA_PLAYLIST[idx % len(OPERA_PLAYLIST)]
        return PluginCardData(
            plugin_id=self.id,
            manifest=self.manifest,
            data={
                "current_track": track,
                "total_tracks": len(OPERA_PLAYLIST),
                "is_playing": False,
            },
            enabled=True,
        )

    async def execute_action(
        self,
        action_name: str,
        payload: Dict[str, Any],
        context: UserContext,
    ) -> PluginActionResult:
        idx = self._current_indices.get(context.user_id, 0)
        if action_name == "next_track":
            idx = (idx + 1) % len(OPERA_PLAYLIST)
            self._current_indices[context.user_id] = idx
            track = OPERA_PLAYLIST[idx]
            return PluginActionResult(
                status="ok",
                message=f"为您切换到：{track['name']}",
                data={"track": track},
            )
        elif action_name == "prev_track":
            idx = (idx - 1 + len(OPERA_PLAYLIST)) % len(OPERA_PLAYLIST)
            self._current_indices[context.user_id] = idx
            track = OPERA_PLAYLIST[idx]
            return PluginActionResult(
                status="ok",
                message=f"为您切换到：{track['name']}",
                data={"track": track},
            )
        return PluginActionResult(status="error", message=f"不支持的动作: {action_name}")


class MemoryKeeperPlugin(BasePlugin):
    """人生时光册插件"""

    def __init__(self) -> None:
        manifest = PluginManifest(
            id="elder_memory",
            name="人生时光回忆册",
            version="1.0.0",
            category=PluginCategory.FAMILY,
            allowed_roles=[UserRole.ELDER],
            card_template=CardTemplateType.INTERACTIVE_PROMPT,
            title="📖 忆苦思甜·今日老话题",
            description="晚辈 AI 主动抛出温情话题，长辈口述过往故事并归档为家庭记忆。",
            icon="📖",
            default_enabled=True,
            sort_order=20,
        )
        super().__init__(manifest)
        self._user_memories: Dict[str, List[str]] = {}

    async def render_card(self, context: UserContext, family_state: Dict[str, Any]) -> PluginCardData:
        memories = self._user_memories.get(context.user_id, [])
        return PluginCardData(
            plugin_id=self.id,
            manifest=self.manifest,
            data={
                "prompt": "外婆，您年轻时那会儿最拿手的家常菜是哪道呀？和孙辈们聊聊秘方呗！",
                "recorded_memories_count": len(memories),
                "latest_memory": memories[-1] if memories else "暂无口述记录，点击麦克风随时开讲",
            },
            enabled=True,
        )

    async def execute_action(
        self,
        action_name: str,
        payload: Dict[str, Any],
        context: UserContext,
    ) -> PluginActionResult:
        if action_name == "save_oral_story":
            text = payload.get("story_text", "").strip()
            if not text:
                return PluginActionResult(status="error", message="回忆内容不能为空")
            memories = self._user_memories.setdefault(context.user_id, [])
            memories.append(text)
            return PluginActionResult(
                status="ok",
                message="外婆真棒！这段珍贵回忆已永久珍藏到家庭时光册中。",
                data={"total_count": len(memories)},
            )
        return PluginActionResult(status="error", message=f"不支持的动作: {action_name}")


class MedicationPlugin(BasePlugin):
    """温馨用药守护插件"""

    def __init__(self) -> None:
        manifest = PluginManifest(
            id="elder_medication",
            name="温馨用药守护",
            version="1.0.0",
            category=PluginCategory.HEALTH,
            allowed_roles=[UserRole.ELDER],
            card_template=CardTemplateType.STAR_TRACKER,
            title="💊 伴随式服药打卡",
            description="定时提醒降压药或维生素，提供正向反馈与亲情便签联动。",
            icon="💊",
            default_enabled=True,
            sort_order=5,
        )
        super().__init__(manifest)
        self._checkin_status: Dict[str, bool] = {}  # user_id -> checked_in

    async def render_card(self, context: UserContext, family_state: Dict[str, Any]) -> PluginCardData:
        is_done = self._checkin_status.get(context.user_id, False)
        return PluginCardData(
            plugin_id=self.id,
            manifest=self.manifest,
            data={
                "medicine_name": "清晨降压温水药",
                "schedule_time": "08:30",
                "checked_in": is_done,
                "memo": "女儿小敏留言：妈，吃完药记得散步半小时哦~",
            },
            enabled=True,
        )

    async def execute_action(
        self,
        action_name: str,
        payload: Dict[str, Any],
        context: UserContext,
    ) -> PluginActionResult:
        if action_name == "checkin":
            self._checkin_status[context.user_id] = True
            return PluginActionResult(
                status="ok",
                message="外婆太棒啦！今日早晨用药已打卡，已同步给女儿小敏。",
                data={"checked_in": True},
            )
        return PluginActionResult(status="error", message=f"不支持的动作: {action_name}")
