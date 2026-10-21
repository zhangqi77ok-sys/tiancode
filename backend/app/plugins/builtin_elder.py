"""长辈专属伴老内建插件集 (Elder Companion Plugins)

包含：
1. 经典戏曲黑胶留声机 (elder_opera)
2. 人生时光册与口述史 (elder_memory)
3. 伴随式温馨用药守护 (elder_medication)
"""
from __future__ import annotations

from typing import Any, Dict

from app.api.v1.auth import get_family_by_id
from app.plugins.base import BasePlugin
from app.plugins.state import load_state, save_state
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

    async def render_card(self, context: UserContext, family_state: Dict[str, Any]) -> PluginCardData:
        idx = int(load_state(self.id, context, {"index": 0}).get("index") or 0)
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
        idx = int(load_state(self.id, context, {"index": 0}).get("index") or 0)
        if action_name == "next_track":
            idx = (idx + 1) % len(OPERA_PLAYLIST)
            save_state(self.id, context, {"index": idx})
            track = OPERA_PLAYLIST[idx]
            return PluginActionResult(
                status="ok",
                message=f"为您切换到：{track['name']}",
                data={"track": track},
            )
        elif action_name == "prev_track":
            idx = (idx - 1 + len(OPERA_PLAYLIST)) % len(OPERA_PLAYLIST)
            save_state(self.id, context, {"index": idx})
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
            description="固定话题提示。口述文字按家庭和成员写入 SQLite，重启后还在。不调用大模型。",
            icon="📖",
            default_enabled=True,
            sort_order=20,
        )
        super().__init__(manifest)

    async def render_card(self, context: UserContext, family_state: Dict[str, Any]) -> PluginCardData:
        memories = load_state(self.id, context, {"memories": []}).get("memories") or []
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
            memories = list(load_state(self.id, context, {"memories": []}).get("memories") or [])
            memories.append(text)
            save_state(self.id, context, {"memories": memories})
            return PluginActionResult(
                status="ok",
                message="这段口述已写入家庭数据库，重启后仍然在。",
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

    async def render_card(self, context: UserContext, family_state: Dict[str, Any]) -> PluginCardData:
        is_done = bool(load_state(self.id, context, {"checked_in": False}).get("checked_in"))
        reminder_on = family_state.get("medication_reminder_enabled", True) is not False
        return PluginCardData(
            plugin_id=self.id,
            manifest=self.manifest,
            data={
                "medicine_name": "清晨降压温水药",
                "schedule_time": "08:30",
                "checked_in": is_done,
                "reminder_enabled": reminder_on,
                "memo": "固定示例便签，没有发送给家人。" if reminder_on else "守护者已关闭用药提醒。",
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
            fam = get_family_by_id(context.family_id) or {}
            if fam.get("settings", {}).get("medication_reminder_enabled") is False:
                return PluginActionResult(status="error", message="守护者已关闭用药提醒")
            save_state(self.id, context, {"checked_in": True})
            return PluginActionResult(
                status="ok",
                message="今日早晨用药已打卡，并写入数据库。没有同步给其他家人。",
                data={"checked_in": True},
            )
        return PluginActionResult(status="error", message=f"不支持的动作: {action_name}")
