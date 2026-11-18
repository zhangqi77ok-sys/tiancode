"""Associative cognitive recall, semantic fact storage, and workspace switching tools."""

from typing import TYPE_CHECKING, List
from langchain_core.tools import BaseTool, tool

if TYPE_CHECKING:
    from tiancode.core.engine import TianCodeEngine


def create_memory_tools(engine: "TianCodeEngine") -> List[BaseTool]:
    """Build cross-session memory recall, fact persistence, and workspace switching tools."""

    @tool
    def recall_memory(query: str, top_k: int = 5) -> str:
        """从跨会话认知记忆与历史对话中联想回忆过往项目、设计决策与代码细节。当用户提起“刚刚那个项目”、历史会话或其它工程时调用。"""
        store = getattr(engine, "memory_store", None)
        if store is None:
            return "ℹ️ 当前引擎未挂载跨会话记忆模块。"

        safe_k = max(1, min(10, top_k))
        hits = store.recall(
            query,
            current_work_dir=engine.settings.work_dir,
            session_store=getattr(engine, "session_store", None),
            top_k=safe_k,
        )
        if not hits:
            known = [f"{w.name} ({w.path})" for w in store.workspaces.values()]
            known_hint = f"\n已知活跃项目: {', '.join(known[:6])}" if known else ""
            return f"🔍 未检索到与 '{query}' 直接匹配的历史情景或事实记忆。{known_hint}"

        lines = [f"🧠 【联想记忆召回: '{query}' (命中 {len(hits)} 条)】"]
        for i, h in enumerate(hits, start=1):
            ws_tag = f" | 目录: {h.workspace_path}" if h.workspace_path else ""
            lines.append(f"{i}. [{h.source_type}] ({h.context_label}{ws_tag})\n   {h.snippet}")
        return "\n".join(lines)

    @tool
    def memorize_fact(fact: str, scope: str = "global", key: str = "") -> str:
        """固化一条值得跨会话长期记住的项目规范、工程别名、测试命令或踩坑结论。scope 可选 'global' 或 'project'。"""
        store = getattr(engine, "memory_store", None)
        if store is None:
            return "❌ 当前引擎未启用认知记忆模块。"
        try:
            saved = store.upsert_fact(
                fact,
                scope=scope,
                key=key,
                workspace_path=str(engine.settings.work_dir),
                source_session_id=getattr(engine, "active_session_id", ""),
            )
            return f"🧠 已记住长期认知 [{saved.scope.value}/{saved.key}]: {saved.content}"
        except Exception as exc:
            return f"❌ 写入记忆失败: {exc}"

    @tool
    def switch_workspace(target_dir: str) -> str:
        """切换当前会话的工作区目录并重锚安全沙箱与 Git 快照（受权限门禁保护）。用于跨项目查代码或接续上一工程。"""
        try:
            new_path = engine.switch_workspace(target_dir)
            return f"📂 已成功将工作区沙箱切换至: {new_path}。现在可以调用 list_files、get_repo_map 或 read_file 查看该项目结构。"
        except Exception as exc:
            return f"❌ 切换工作区失败: {exc}"

    return [recall_memory, memorize_fact, switch_workspace]
