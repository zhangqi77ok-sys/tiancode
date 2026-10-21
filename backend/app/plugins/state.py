"""插件进度按 (plugin_id, family_id, user_id) 写入 SQLite。"""
from __future__ import annotations

from typing import Any, Dict

from app.core.store import store
from app.schemas.auth import UserContext


def scope_key(context: UserContext) -> str:
    return f"{context.family_id}:{context.user_id}"


def load_state(plugin_id: str, context: UserContext, default: Dict[str, Any]) -> Dict[str, Any]:
    saved = store.get_plugin_state(plugin_id, scope_key(context))
    if saved is None:
        return dict(default)
    merged = dict(default)
    merged.update(saved)
    return merged


def save_state(plugin_id: str, context: UserContext, state: Dict[str, Any]) -> None:
    store.set_plugin_state(plugin_id, scope_key(context), state)
