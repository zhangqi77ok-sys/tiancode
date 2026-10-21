"""AI 模型与运行态管控中枢 (AI Runtime Configuration Manager)

负责管理大模型服务商通道、系统人设 Prompt、TTS/ASR 语速音色以及安全告警度量。
支持平台管理员在运行时免重启热更新。
"""
from __future__ import annotations

import logging
from typing import Any, Dict, List
from app.schemas.admin import AiConfigSchema, AiConfigUpdateRequest, LLMProviderType

logger = logging.getLogger(__name__)

DEFAULT_ELDER_PROMPT = (
    "你是一个温暖耐心的家庭伴老邻里助手。面对银发长辈，你说话温和慢速、尊重体贴，"
    "善于倾听长辈的回忆，绝不使用晦涩技术术语，主动提醒长辈按时服药与健康生活，严防诈骗诱导。"
)

DEFAULT_CHILD_PROMPT = (
    "你是活泼萌趣的陪伴小熊（豆豆熊）。面对学龄前幼童，你充满好奇心与耐心，"
    "善于用拟人生动的童话讲故事，用分幕选择引导孩子做决定，鼓励好习惯打卡，语言积极健康，绝不涉及暴力有害信息。"
)


class AiConfigManager:
    """平台 AI 运行态配置单例管理器"""

    def __init__(self) -> None:
        self._provider: LLMProviderType = LLMProviderType.MOCK
        self._api_key: str = "sk-demo-antigravity-warmcompanion-key"
        self._base_url: str = "https://api.deepseek.com/v1"
        self._elder_prompt: str = DEFAULT_ELDER_PROMPT
        self._child_prompt: str = DEFAULT_CHILD_PROMPT
        self._safety_filter_enabled: bool = True
        self._tts_elder_voice: str = "zh-CN-YunyangNeural"
        self._tts_elder_rate: str = "-15%"
        self._tts_child_voice: str = "zh-CN-XiaoyiNeural"
        self._tts_child_rate: str = "+5%"
        self._temperature: float = 0.7
        self._max_tokens: int = 800

        # 度量统计
        self._dialogue_count: int = 0
        self._safety_alerts: List[Dict[str, Any]] = []

    def get_config_schema(self) -> AiConfigSchema:
        """获取脱敏配置契约输出"""
        masked = f"{self._api_key[:3]}******" if len(self._api_key) > 6 else "sk-******"
        return AiConfigSchema(
            llm_provider=self._provider,
            api_key_masked=masked,
            base_url=self._base_url,
            elder_prompt=self._elder_prompt,
            child_prompt=self._child_prompt,
            safety_filter_enabled=self._safety_filter_enabled,
            tts_elder_voice=self._tts_elder_voice,
            tts_elder_rate=self._tts_elder_rate,
            tts_child_voice=self._tts_child_voice,
            tts_child_rate=self._tts_child_rate,
            temperature=self._temperature,
            max_tokens=self._max_tokens,
        )

    def update_config(self, req: AiConfigUpdateRequest) -> AiConfigSchema:
        """更新 AI 与语音参数"""
        if req.llm_provider is not None:
            self._provider = req.llm_provider
        if req.api_key:
            self._api_key = req.api_key
        if req.base_url:
            self._base_url = req.base_url
        if req.elder_prompt is not None:
            self._elder_prompt = req.elder_prompt
        if req.child_prompt is not None:
            self._child_prompt = req.child_prompt
        if req.safety_filter_enabled is not None:
            self._safety_filter_enabled = req.safety_filter_enabled
        if req.tts_elder_voice is not None:
            self._tts_elder_voice = req.tts_elder_voice
        if req.tts_elder_rate is not None:
            self._tts_elder_rate = req.tts_elder_rate
        if req.tts_child_voice is not None:
            self._tts_child_voice = req.tts_child_voice
        if req.tts_child_rate is not None:
            self._tts_child_rate = req.tts_child_rate
        if req.temperature is not None:
            self._temperature = req.temperature
        if req.max_tokens is not None:
            self._max_tokens = req.max_tokens

        logger.info(f"管理员热更新 AI 配置成功: provider={self._provider.value}")
        return self.get_config_schema()

    def record_dialogue(self) -> None:
        """累加一次 AI 对话调用"""
        self._dialogue_count += 1

    def record_safety_alert(self, detail: str) -> None:
        """记录一次安全拦截告警"""
        self._safety_alerts.append({"detail": detail})

    @property
    def dialogue_count(self) -> int:
        return self._dialogue_count

    @property
    def safety_alerts_count(self) -> int:
        return len(self._safety_alerts)


# 全局单例
ai_config_manager = AiConfigManager()
