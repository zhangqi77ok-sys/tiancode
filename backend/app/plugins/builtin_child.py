"""儿童专属童趣启蒙内建插件集 (Child Companion Plugins)

包含：
1. 豆豆熊双向分幕决策故事机 (child_story)
2. 好习惯星星宝盒打卡箱 (child_habit)
3. 万物为什么奇妙百科 (child_encyclopedia)
"""
from __future__ import annotations

from typing import Any, Dict

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


class InteractiveStoryPlugin(BasePlugin):
    """双向分幕共创故事机插件"""

    def __init__(self) -> None:
        manifest = PluginManifest(
            id="child_story",
            name="双向共创故事机",
            version="1.0.0",
            category=PluginCategory.GROWTH,
            allowed_roles=[UserRole.CHILD],
            card_template=CardTemplateType.DECISION_STORY,
            title="🐻 豆豆熊魔法冒险",
            description="两幕固定文案。孩子的选择写入 PostgreSQL。不调用大模型续写。",
            icon="🐻",
            default_enabled=True,
            sort_order=10,
        )
        super().__init__(manifest)

    async def render_card(self, context: UserContext, family_state: Dict[str, Any]) -> PluginCardData:
        state = load_state(
            self.id,
            context,
            {
                "scene": "第 1 幕：豆豆熊背着心爱的小背包来到了神秘魔法森林，前方出现了两条闪光的小路...",
                "branch_options": [
                    {"id": "branch_a", "label": "🅰️ 降落棉花糖城堡找小精灵"},
                    {"id": "branch_b", "label": "🅱️ 穿过彩虹桥探秘会唱歌的小树"},
                ],
                "act": 1,
            },
        )
        return PluginCardData(
            plugin_id=self.id,
            manifest=self.manifest,
            data=state,
            enabled=True,
        )

    async def execute_action(
        self,
        action_name: str,
        payload: Dict[str, Any],
        context: UserContext,
    ) -> PluginActionResult:
        if action_name == "choose_branch":
            choice = payload.get("branch_id")
            if choice == "branch_a":
                new_state = {
                    "scene": "第 2 幕：哇！棉花糖城堡软绵绵的，小精灵送给你一颗发光的草莓糖，你们一起飞向云朵秋千！",
                    "branch_options": [
                        {"id": "branch_end", "label": "🎉 听豆豆熊唱晚安摇篮曲"}
                    ],
                    "act": 2,
                }
            else:
                new_state = {
                    "scene": "第 2 幕：彩虹桥上叮咚作响，会唱歌的小树为你弹奏起欢快的森林交响曲，小动物们都在鼓掌！",
                    "branch_options": [
                        {"id": "branch_end", "label": "🎉 听豆豆熊唱晚安摇篮曲"}
                    ],
                    "act": 2,
                }
            save_state(self.id, context, new_state)
            return PluginActionResult(
                status="ok",
                message="下一幕是写好的文案，不是大模型续写。",
                data=new_state,
            )
        return PluginActionResult(status="error", message=f"不支持的动作: {action_name}")


class HabitChallengePlugin(BasePlugin):
    """好习惯星星打卡箱插件"""

    def __init__(self) -> None:
        manifest = PluginManifest(
            id="child_habit",
            name="好习惯星星宝盒",
            version="1.0.0",
            category=PluginCategory.GROWTH,
            allowed_roles=[UserRole.CHILD],
            card_template=CardTemplateType.STAR_TRACKER,
            title="⭐ 好习惯星星挑战",
            description="将刷牙、喝牛奶、按时作息拆解为星星任务，集齐 3 颗点亮晚安小勇士勋章。",
            icon="⭐",
            default_enabled=True,
            sort_order=5,
        )
        super().__init__(manifest)

    async def render_card(self, context: UserContext, family_state: Dict[str, Any]) -> PluginCardData:
        stars = load_state(
            self.id,
            context,
            {"brush_teeth": False, "drink_milk": False, "sleep_on_time": False},
        )
        collected = sum(1 for v in stars.values() if v)
        return PluginCardData(
            plugin_id=self.id,
            manifest=self.manifest,
            data={
                "stars": stars,
                "collected_count": collected,
                "total_target": 3,
                "badge_unlocked": collected >= 3,
                "badge_name": "晚安小勇士勋章 🎖️" if collected >= 3 else "集齐3颗星解锁勋章",
            },
            enabled=True,
        )

    async def execute_action(
        self,
        action_name: str,
        payload: Dict[str, Any],
        context: UserContext,
    ) -> PluginActionResult:
        if action_name == "toggle_habit":
            habit_key = payload.get("habit_key")
            stars = load_state(
                self.id,
                context,
                {"brush_teeth": False, "drink_milk": False, "sleep_on_time": False},
            )
            if habit_key in stars:
                stars[habit_key] = True
                save_state(self.id, context, stars)
                collected = sum(1 for v in stars.values() if v)
                msg = "恭喜你点亮一颗小金星！" if collected < 3 else "太棒啦！集齐 3 颗星，荣获【晚安小勇士勋章】🎖️！"
                return PluginActionResult(
                    status="ok",
                    message=msg,
                    data={"stars": stars, "collected_count": collected, "badge_unlocked": collected >= 3},
                )
            return PluginActionResult(status="error", message="未知的习惯任务标识")
        return PluginActionResult(status="error", message=f"不支持的动作: {action_name}")


class WonderEncyclopediaPlugin(BasePlugin):
    """万物为什么奇妙百科插件"""

    def __init__(self) -> None:
        manifest = PluginManifest(
            id="child_encyclopedia",
            name="万物为什么奇妙百科",
            version="1.0.0",
            category=PluginCategory.GROWTH,
            allowed_roles=[UserRole.CHILD],
            card_template=CardTemplateType.INTERACTIVE_PROMPT,
            title="🔍 万物为什么百科",
            description="固定问答文案。不是大模型解答，也没有音频文件。",
            icon="🔍",
            default_enabled=True,
            sort_order=20,
        )
        super().__init__(manifest)

    async def render_card(self, context: UserContext, family_state: Dict[str, Any]) -> PluginCardData:
        return PluginCardData(
            plugin_id=self.id,
            manifest=self.manifest,
            data={
                "featured_question": "为什么雨后天空会有漂亮的彩虹呢？",
                "answer_summary": "因为太阳公公把阳光当成了彩色画笔，穿过空气里调皮的七彩小水滴，天空就铺开了一条彩色滑梯！",
                "listen_audio_url": "",
            },
            enabled=True,
        )

    async def execute_action(
        self,
        action_name: str,
        payload: Dict[str, Any],
        context: UserContext,
    ) -> PluginActionResult:
        if action_name == "ask_question":
            q = payload.get("question", "")
            return PluginActionResult(
                status="ok",
                message="这是写死的回复，不是大模型回答。",
                data={"reply": "页面上的百科句子是固定文案。", "question": q},
            )
        return PluginActionResult(status="error", message=f"不支持的动作: {action_name}")
