"""各路由共用的响应形状。401/403 的 detail 是字符串，不是校验错误数组。"""
from __future__ import annotations

from pydantic import BaseModel, Field


class ErrorBody(BaseModel):
    """给人看的错误说明。"""
    detail: str = Field(..., description="错误说明")


class ServiceStatus(BaseModel):
    """健康检查。/healthz 的 status 是 ok，/readyz 的 status 是 ready。"""
    status: str = Field(..., description="ok 或 ready")
