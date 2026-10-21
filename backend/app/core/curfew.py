"""儿童防沉迷时段。北京时间（UTC+8，中国无夏令时）21:00 至次日 06:00。

kids_curfew_enabled 为真时，这个窗口内拒绝儿童角色的插件动作。
测试用 set_now 固定时钟，避免夜间跑测试被误伤。
"""
from __future__ import annotations

from datetime import datetime, timedelta, timezone
from typing import Any, Dict, Optional

SHANGHAI = timezone(timedelta(hours=8))
QUIET_START_HOUR = 21
QUIET_END_HOUR = 6

_frozen: Optional[datetime] = None


def set_now(value: Optional[datetime]) -> None:
    global _frozen
    _frozen = value


def now_shanghai() -> datetime:
    if _frozen is None:
        return datetime.now(SHANGHAI)
    if _frozen.tzinfo is None:
        return _frozen.replace(tzinfo=SHANGHAI)
    return _frozen.astimezone(SHANGHAI)


def in_quiet_hours(moment: Optional[datetime] = None) -> bool:
    current = now_shanghai() if moment is None else (
        moment.replace(tzinfo=SHANGHAI) if moment.tzinfo is None else moment.astimezone(SHANGHAI)
    )
    hour = current.hour
    return hour >= QUIET_START_HOUR or hour < QUIET_END_HOUR


def child_actions_blocked(role: str, settings: Optional[Dict[str, Any]]) -> bool:
    if role != "child":
        return False
    enabled = True
    if settings is not None and settings.get("kids_curfew_enabled") is False:
        enabled = False
    return enabled and in_quiet_hours()
