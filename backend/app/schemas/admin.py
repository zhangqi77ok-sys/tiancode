"""平台管理员后台契约模型 (Pydantic Schemas)

涵盖 AI 模型与提示词编排、TTS 拟人音色配置、插件全局熔断控制及大盘统计契约。
严格补齐中文说明与字段级注释。
"""
from __future__ import annotations

from enum import Enum
from typing import List, Optional
from pydantic import BaseModel, Field


class LLMProviderType(str, Enum):
    """大语言模型通道服务商"""
    DEEPSEEK = "deepseek"
    QWEN = "qwen"
    OPENAI = "openai"
    MOCK = "mock"


class AiConfigSchema(BaseModel):
    """平台 AI 与语音核心配置契约（脱敏输出/读取）"""
    llm_provider: LLMProviderType = Field(LLMProviderType.MOCK, description="大模型服务商通道")
    api_key_masked: str = Field("sk-******", description="已脱敏的 API 密钥")
    base_url: str = Field("https://api.deepseek.com/v1", description="大模型 API Base URL")
    elder_prompt: str = Field(..., description="长辈知心邻里 Agent 系统人设提示词")
    child_prompt: str = Field(..., description="儿童萌趣玩伴 Agent 系统人设提示词")
    safety_filter_enabled: bool = Field(True, description="适老防诈与儿童内容安全护栏总开关")
    tts_elder_voice: str = Field("zh-CN-YunyangNeural", description="长辈端 Edge-TTS 温润朗读音色")
    tts_elder_rate: str = Field("-15%", description="长辈端语音放慢语速比例")
    tts_child_voice: str = Field("zh-CN-XiaoyiNeural", description="儿童端 Edge-TTS 活泼童趣音色")
    tts_child_rate: str = Field("+5%", description="儿童端语速比例")
    temperature: float = Field(0.7, description="生成发散度 (0.0~1.0)")
    max_tokens: int = Field(800, description="单次最大回复 Token 限制")


class AiConfigUpdateRequest(BaseModel):
    """AI 与语音配置热更新请求契约"""
    llm_provider: Optional[LLMProviderType] = Field(None, description="大模型服务商通道")
    api_key: Optional[str] = Field(None, description="新 API 密钥（为空则保留原密钥）")
    base_url: Optional[str] = Field(None, description="新 API Base URL")
    elder_prompt: Optional[str] = Field(None, description="长辈 Agent 系统提示词")
    child_prompt: Optional[str] = Field(None, description="儿童 Agent 系统提示词")
    safety_filter_enabled: Optional[bool] = Field(None, description="安全护栏开关")
    tts_elder_voice: Optional[str] = Field(None, description="长辈端音色")
    tts_elder_rate: Optional[str] = Field(None, description="长辈端语速")
    tts_child_voice: Optional[str] = Field(None, description="儿童端音色")
    tts_child_rate: Optional[str] = Field(None, description="儿童端语速")
    temperature: Optional[float] = Field(None, description="生成发散度")
    max_tokens: Optional[int] = Field(None, description="最大 Token 限制")


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
    """全平台中控大盘监控统计"""
    total_families: int = Field(0, description="已注册家庭空间总数")
    total_elders: int = Field(0, description="平台纳管银发长辈总数")
    total_children: int = Field(0, description="平台纳管幼童宝贝总数")
    total_guardians: int = Field(0, description="平台注册守护者总数")
    total_ai_dialogues: int = Field(0, description="今日 AI 陪伴对话总轮次")
    safety_alerts_count: int = Field(0, description="今日安全防诈与敏感词拦截预警次数")
    active_plugins_count: int = Field(0, description="当前全平台活跃运行插件数量")
