"""插件进度按 (plugin_id, family_id, user_id) 写入 PostgreSQL。

scope_key 是 ``{family_id}:{user_id}``。读改写走 update_state，在一行上
SELECT ... FOR UPDATE，避免并发追加互相覆盖。
"""
from __future__ import annotations

from typing import Any, Callable, Dict

from app.core.store import PluginStateError, store
from app.schemas.auth import UserContext


def scope_key(context: UserContext) -> str:
    family_id = str(context.family_id or "").strip()
    user_id = str(context.user_id or "").strip()
    if not family_id or not user_id or ":" in family_id or ":" in user_id:
        raise PluginStateError("插件进度标识无效")
    return f"{family_id}:{user_id}"


def load_state(plugin_id: str, context: UserContext, default: Dict[str, Any]) -> Dict[str, Any]:
    saved = store.get_plugin_state(plugin_id, scope_key(context))
    if saved is None:
        return dict(default)
    merged = dict(default)
    merged.update(saved)
    return merged


def save_state(plugin_id: str, context: UserContext, state: Dict[str, Any]) -> None:
    store.set_plugin_state(plugin_id, scope_key(context), state)


def update_state(
    plugin_id: str,
    context: UserContext,
    default: Dict[str, Any],
    mutator: Callable[[Dict[str, Any]], Dict[str, Any]],
) -> Dict[str, Any]:
    """在行锁里合并 default 与已有进度。mutator 不能再调用存储。"""

    def apply(current: Dict[str, Any]) -> Dict[str, Any]:
        base = dict(default)
        base.update(current)
        updated = mutator(base)
        if not isinstance(updated, dict):
            raise PluginStateError("插件进度必须是对象")
        return updated

    return store.update_plugin_state(plugin_id, scope_key(context), apply)
