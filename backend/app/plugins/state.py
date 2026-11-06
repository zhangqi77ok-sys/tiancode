"""插件进度按 (plugin_id, family_id, user_id) 写入 PostgreSQL。

scope_key 的形状是 ``{family_id}:{user_id}``。家庭标识和成员标识都不能含冒号，
所以两个长辈、两个家庭的口述、曲目和星星不会写进同一行。

读改写必须走 update_state。它在一行上 SELECT ... FOR UPDATE，追加口述时不会丢掉
另一个并发请求刚写进去的内容。这种写入不在断线后自动重试。
"""
from __future__ import annotations

from typing import Any, Callable, Dict

from app.core.store import PluginStateError, store
from app.schemas.auth import UserContext

_MAX_SCOPE_ID_CHARS = 64


def scope_key(context: UserContext) -> str:
    """生成进度行的范围键。缺标识、含冒号或过长时拒绝，而不是拼出一条会串数据的键。"""
    family_id = str(context.family_id or "").strip()
    user_id = str(context.user_id or "").strip()
    if not family_id or not user_id:
        raise PluginStateError("插件进度缺少家庭或成员标识")
    if ":" in family_id or ":" in user_id:
        raise PluginStateError("插件进度标识不能包含冒号")
    if len(family_id) > _MAX_SCOPE_ID_CHARS or len(user_id) > _MAX_SCOPE_ID_CHARS:
        raise PluginStateError("插件进度标识过长")
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
    """在行锁里把 default 和已有进度合并后交给 mutator。mutator 不能再调用存储。"""

    def apply(current: Dict[str, Any]) -> Dict[str, Any]:
        base = dict(default)
        base.update(current)
        updated = mutator(base)
        if not isinstance(updated, dict):
            raise PluginStateError("插件进度必须是对象")
        return updated

    return store.update_plugin_state(plugin_id, scope_key(context), apply)
