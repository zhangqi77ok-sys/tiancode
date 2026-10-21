"""认证与家庭空间数据契约模块 (Pydantic Schemas)

严格为所有输入与输出模型定义中文说明，作为前后端契约规范。
"""
from __future__ import annotations

from enum import Enum
from typing import Optional
from pydantic import BaseModel, Field


class UserRole(str, Enum):
    """用户家庭角色枚举"""
    GUARDIAN = "guardian"  # 家庭守护者（家长/中青年）
    ELDER = "elder"        # 银发长辈
    CHILD = "child"        # 幼童宝贝


class RegisterRequest(BaseModel):
    """家庭守护者建家注册请求"""
    phone: str = Field(..., description="守护者手机号", min_length=11, max_length=11)
    password: str = Field(..., description="登录密码", min_length=6)
    family_name: str = Field(..., description="家庭空间名称", min_length=2, max_length=32)
    nickname: str = Field(..., description="守护者称呼（如：小敏）", min_length=1, max_length=16)
    birth_date: str = Field(..., description="出生年月日 (YYYY-MM-DD)")


class LoginRequest(BaseModel):
    """守护者手机号密码登录请求"""
    phone: str = Field(..., description="手机号")
    password: str = Field(..., description="登录密码")


class MemberAddRequest(BaseModel):
    """守护者为家庭添加长辈或幼童成员请求"""
    nickname: str = Field(..., description="称呼（如：外婆、小豆豆）", min_length=1, max_length=16)
    role: UserRole = Field(..., description="角色：elder(长辈) 或 child(幼童)")
    birth_date: str = Field(..., description="出生年月日 (YYYY-MM-DD)")
    pin_code: Optional[str] = Field(None, description="简易快速登录 4~6 位数字 Pin 码", min_length=4, max_length=6)


class MemberLoginRequest(BaseModel):
    """长辈/幼童使用家庭邀请码 + Pin 码快捷登录请求"""
    invite_code: str = Field(..., description="家庭 6 位邀请码", min_length=6, max_length=6)
    pin_code: str = Field(..., description="个人专属简易 Pin 码", min_length=4, max_length=6)


class TokenResponse(BaseModel):
    """JWT 令牌响应"""
    access_token: str = Field(..., description="Bearer JWT 访问令牌")
    token_type: str = Field("bearer", description="令牌类型")
    role: UserRole = Field(..., description="当前使用者角色")
    user_id: str = Field(..., description="用户唯一标识 ID")
    family_id: str = Field(..., description="所属家庭空间 ID")
    name: str = Field(..., description="用户昵称称呼")


class UserContext(BaseModel):
    """认证通过后的上下文对象（用于依赖注入）"""
    user_id: str
    family_id: str
    role: UserRole
    nickname: str
    birth_date: str


class FamilyMemberOut(BaseModel):
    """家庭成员信息输出"""
    id: str
    nickname: str
    role: UserRole
    birth_date: str
    age: int = Field(..., description="系统计算周岁")
    pin_code: Optional[str] = Field(None, description="登录Pin码（仅守护者可见）")


class FamilySettingsUpdate(BaseModel):
    """家庭看护配置更新请求（仅守护者可调）"""
    medication_reminder_enabled: bool = Field(True, description="长辈用药提醒总开关")
    kids_curfew_enabled: bool = Field(True, description="儿童 21:00 睡前故事防沉迷开关")
