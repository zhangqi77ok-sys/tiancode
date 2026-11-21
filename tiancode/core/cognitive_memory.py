"""Tri-tier Cognitive Memory engine: priming index, episodic consolidation, and associative recall."""

from dataclasses import asdict
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import tempfile
import threading
from typing import Any, Dict, List, Optional, Sequence, Tuple, Union

from langchain_core.messages import BaseMessage, HumanMessage

from tiancode.domain.memory import EpisodeRecord, MemoryScope, RecallHit, SemanticFact, WorkspaceEntity

MAX_WORKSPACES, MAX_EPISODES, MAX_FACTS = 12, 50, 100
_STACK_MARKERS: Tuple[Tuple[str, str], ...] = (
    ("pom.xml", "Maven/Java"), ("build.gradle", "Gradle"), ("build.gradle.kts", "Gradle"),
    ("pyproject.toml", "Python"), ("requirements.txt", "Python"),
    ("package.json", "Node/JS"), ("go.mod", "Go"), ("Cargo.toml", "Rust"),
)


def _memory_file_path(path: Path) -> Path:
    """Return a memory file path without following the final symlink."""
    raw = path.expanduser()
    if not raw.is_absolute():
        raw = Path.cwd() / raw
    try:
        parent = raw.parent.resolve()
    except OSError:
        parent = raw.parent.absolute()
    return parent / raw.name


def get_default_memory_path() -> Path:
    """Resolve brain memory path with strict pytest sandbox isolation.

    The last path component is not resolved, so a symlink is not followed.
    """
    if env_override := os.environ.get("TIANCODE_MEMORY_PATH"):
        return _memory_file_path(Path(env_override))
    if os.environ.get("PYTEST_CURRENT_TEST"):
        filename = f"tiancode_pytest_{os.getpid()}_brain_memory.json"
        return _memory_file_path(Path(tempfile.gettempdir()) / filename)
    return Path.home() / ".tiancode" / "brain_memory.json"


def _replace_private_memory(path: Path, payload: Dict[str, Any]) -> None:
    """Write memory as a private regular file. Do not follow a symlink or hard link."""
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    if temporary.is_symlink() or temporary.exists():
        temporary.unlink()
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
    fd = os.open(temporary, flags, 0o600)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            json.dump(payload, handle, indent=2, ensure_ascii=False)
            handle.flush()
            os.fsync(handle.fileno())
    except Exception:
        temporary.unlink(missing_ok=True)
        raise
    os.replace(temporary, path)


def _now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def _norm_path(path: Union[Path, str]) -> str:
    try:
        return str(Path(path).resolve())
    except Exception:
        return str(path)


def _hash_id(raw: str) -> str:
    return hashlib.sha256(raw.lower().encode("utf-8")).hexdigest()[:12]


def _detect_tags(work_dir: Path) -> List[str]:
    if not work_dir.is_dir():
        return []
    return list(dict.fromkeys(tag for fname, tag in _STACK_MARKERS if (work_dir / fname).exists()))


def tokenize_search_terms(text: str) -> List[str]:
    """Extract lowercase ASCII terms and CJK bigrams for dependency-free fuzzy matching."""
    low = text.lower()
    tokens: List[str] = re.findall(r"[a-z0-9_.\-/]{2,}", low)
    for seg in re.findall(r"[\u4e00-\u9fff]{2,}", low):
        tokens.extend([seg] if len(seg) <= 3 else [seg[i : i + 2] for i in range(len(seg) - 1)])
    return list(dict.fromkeys(tokens))


def _recency_factor(iso_ts: str) -> float:
    if not iso_ts:
        return 0.3
    try:
        dt = datetime.fromisoformat(iso_ts.replace("Z", "+00:00"))
        age_hours = max(0.0, (datetime.now(timezone.utc) - dt).total_seconds() / 3600.0)
        return 1.0 / (1.0 + age_hours / 48.0)
    except Exception:
        return 0.3


def _score_match(query_tokens: Sequence[str], target_text: str, updated_at: str, weight: float) -> float:
    if not query_tokens:
        return round(0.5 * _recency_factor(updated_at) + 0.5 * weight, 4)
    low = target_text.lower()
    hits = sum(1 for tok in query_tokens if tok in low)
    if hits == 0:
        return 0.0
    coverage = hits / len(query_tokens)
    return round(0.6 * coverage + 0.25 * _recency_factor(updated_at) + 0.15 * min(1.0, weight), 4)


def _msg_role_and_text(msg: Any) -> Tuple[str, str]:
    if isinstance(msg, BaseMessage):
        role = "human" if isinstance(msg, HumanMessage) else msg.type
        return role, (msg.content if isinstance(msg.content, str) else str(msg.content)).strip()
    if isinstance(msg, dict):
        data = msg.get("data", {}) if isinstance(msg.get("data"), dict) else msg
        return str(msg.get("type", "")), str(data.get("content", "")).strip()
    return "", ""


class CognitiveMemoryStore:
    """Thread-safe atomic JSON store for cross-session Workspace, Episodic, and Semantic memories."""

    def __init__(self, memory_path: Optional[Path] = None) -> None:
        self.memory_path = _memory_file_path(memory_path) if memory_path is not None else get_default_memory_path()
        self._lock = threading.Lock()
        self.workspaces: Dict[str, WorkspaceEntity] = {}
        self.episodes: List[EpisodeRecord] = []
        self.facts: List[SemanticFact] = []
        self._load()

    def _load(self) -> None:
        path = self.memory_path
        from tiancode.core.path_safety import is_shared_hardlink

        if path.is_symlink() or is_shared_hardlink(path) or not path.is_file():
            return
        try:
            data = json.loads(path.read_text(encoding="utf-8"))
        except Exception:
            return
        for k, v in (data.get("workspaces") or {}).items():
            try:
                self.workspaces[k] = WorkspaceEntity(**v)
            except Exception:
                pass
        for item in data.get("episodes") or []:
            try:
                self.episodes.append(EpisodeRecord(**item))
            except Exception:
                pass
        for item in data.get("facts") or []:
            try:
                self.facts.append(SemanticFact(**{**item, "scope": MemoryScope.from_value(item.get("scope", "global"))}))
            except Exception:
                pass

    def _save(self) -> None:
        payload = {
            "workspaces": {k: asdict(v) for k, v in self.workspaces.items()},
            "episodes": [asdict(e) for e in self.episodes[-MAX_EPISODES:]],
            "facts": [{**asdict(f), "scope": f.scope.value} for f in self.facts[-MAX_FACTS:]],
        }
        _replace_private_memory(self.memory_path, payload)

    def touch_workspace(self, work_dir: Union[Path, str], session_id: Optional[str] = None) -> WorkspaceEntity:
        """Register or refresh a workspace entity with tech-stack tags and linked sessions."""
        norm, now = _norm_path(work_dir), _now_iso()
        wid, tags = _hash_id(norm), _detect_tags(Path(norm))
        with self._lock:
            existing = self.workspaces.get(wid)
            sessions = list(existing.session_ids) if existing else []
            if session_id and session_id not in sessions:
                sessions = (sessions + [session_id])[-10:]
            ws = WorkspaceEntity(
                workspace_id=wid, name=Path(norm).name or norm, path=norm,
                tags=tags or (existing.tags if existing else []), last_active_at=now, session_ids=sessions,
            )
            self.workspaces[wid] = ws
            if len(self.workspaces) > MAX_WORKSPACES:
                keep = sorted(self.workspaces, key=lambda k: self.workspaces[k].last_active_at, reverse=True)[:MAX_WORKSPACES]
                self.workspaces = {k: self.workspaces[k] for k in keep}
            self._save()
            return ws

    def upsert_fact(
        self, content: str, *, scope: Union[MemoryScope, str] = MemoryScope.GLOBAL,
        key: str = "", workspace_path: str = "", source_session_id: str = "", importance: float = 0.8,
    ) -> SemanticFact:
        """Store or evolve a semantic fact; matching scope+workspace+key overwrites in-place."""
        clean = content.strip()
        if not clean:
            raise ValueError("记忆内容不能为空")
        scope_enum = scope if isinstance(scope, MemoryScope) else MemoryScope.from_value(scope)
        norm_ws = _norm_path(workspace_path) if (scope_enum is MemoryScope.PROJECT and workspace_path) else ""
        norm_key, now = key.strip().lower() or _hash_id(clean), _now_iso()

        with self._lock:
            for idx, f in enumerate(self.facts):
                if f.scope is scope_enum and f.workspace_path == norm_ws and f.key == norm_key:
                    updated = SemanticFact(
                        f.fact_id, norm_key, clean, scope_enum, norm_ws,
                        source_session_id or f.source_session_id, max(f.importance, importance), now, f.access_count + 1,
                    )
                    self.facts[idx] = updated
                    self._save()
                    return updated

            fact = SemanticFact(f"fact_{_hash_id(norm_ws + norm_key + now)[:10]}", norm_key, clean, scope_enum, norm_ws, source_session_id, importance, now, 1)
            self.facts = sorted(self.facts + [fact], key=lambda x: (x.importance, x.updated_at), reverse=True)[:MAX_FACTS]
            self._save()
            return fact

    def list_facts(self, scope: Optional[MemoryScope] = None, workspace_path: Optional[str] = None) -> List[SemanticFact]:
        """List stored semantic facts optionally filtered by scope and workspace."""
        norm_ws = _norm_path(workspace_path) if workspace_path else None
        with self._lock:
            return [f for f in self.facts if (scope is None or f.scope is scope) and (norm_ws is None or f.workspace_path == norm_ws)]

    def consolidate_session(
        self, session_id: str, work_dir: Union[Path, str], messages: Sequence[Any],
        *, plan: Any = None, modified_files: Optional[Sequence[str]] = None,
    ) -> EpisodeRecord:
        """Deterministically distill an EpisodeRecord from session messages (0 LLM calls)."""
        ws = self.touch_workspace(work_dir, session_id=session_id)
        parsed = [_msg_role_and_text(m) for m in messages]
        human_texts = [txt for role, txt in parsed if role == "human" and txt]
        ai_texts = [txt for role, txt in parsed if role == "ai" and txt]

        intent = (human_texts[-1] if human_texts else (getattr(plan, "task_title", "") or "常规工作会话"))[:180].replace("\n", " ")
        tail_ai = (ai_texts[-1][:220].replace("\n", " ") if ai_texts else "")
        files = list(dict.fromkeys(modified_files or []))
        summary = " | ".join(p for p in (f"涉及文件: {', '.join(files[:8])}" if files else "", tail_ai) if p) or "已完成对话交互"
        entities = list(dict.fromkeys(files + re.findall(r"\b[A-Za-z_][A-Za-z0-9_.-]{3,}\b", intent + " " + tail_ai)[:12]))

        now = _now_iso()
        with self._lock:
            created = next((e.created_at for e in self.episodes if e.session_id == session_id), now)
            ep = EpisodeRecord(f"ep_{_hash_id(session_id)}", session_id, ws.path, ws.name, created, now, intent, summary, entities)
            self.episodes = sorted([e for e in self.episodes if e.session_id != session_id] + [ep], key=lambda e: e.updated_at, reverse=True)[:MAX_EPISODES]
            self._save()
            return ep

    def recall(self, query: str, *, current_work_dir: Optional[Union[Path, str]] = None, session_store: Any = None, top_k: int = 6) -> List[RecallHit]:
        """Associatively rank matches across Semantic Facts, Episodes, and Session History."""
        q_tokens = tokenize_search_terms(query)
        hits: List[RecallHit] = []
        with self._lock:
            for idx, f in enumerate(self.facts):
                sc = _score_match(q_tokens, f"{f.key} {f.content} {f.workspace_path}", f.updated_at, f.importance)
                if sc > 0:
                    label = Path(f.workspace_path).name if f.workspace_path else "全局记忆"
                    hits.append(RecallHit("fact", sc, label, f.content, f.updated_at, f.source_session_id, f.workspace_path))
                    self.facts[idx].access_count += 1
            for ep in self.episodes:
                blob = f"{ep.project_name} {ep.workspace_path} {ep.intent} {ep.summary} {' '.join(ep.entities)}"
                sc = _score_match(q_tokens, blob, ep.updated_at, 0.85)
                if sc > 0:
                    hits.append(RecallHit("episode", sc, f"{ep.project_name} [{ep.session_id}]", f"意图: {ep.intent} | 结论: {ep.summary}", ep.updated_at, ep.session_id, ep.workspace_path))
            if hits:
                self._save()

        if session_store is not None and hasattr(session_store, "search_history"):
            for item in session_store.search_history(query, max_hits=top_k):
                sc = _score_match(q_tokens, f"{item['project']} {item['snippet']}", item["updated_at"], 0.65)
                if sc > 0:
                    hits.append(RecallHit("session_message", sc, f"{item['project']} [{item['session_id']}]", item["snippet"], item["updated_at"], item["session_id"], item["work_dir"]))

        hits.sort(key=lambda h: h.score, reverse=True)
        return hits[:max(1, top_k)]

    def format_priming_context(self, current_work_dir: Union[Path, str], *, max_chars: int = 1200) -> str:
        """Synthesize budget-aware subconscious priming index for SystemMessage injection."""
        norm_curr = _norm_path(current_work_dir)
        with self._lock:
            ws_list = sorted(self.workspaces.values(), key=lambda w: w.last_active_at, reverse=True)[:4]
            ep_list = self.episodes[:4]
            fact_list = [f for f in self.facts if f.scope is MemoryScope.GLOBAL or f.workspace_path == norm_curr][:5]

        if not ws_list and not ep_list and not fact_list:
            return ""
        lines: List[str] = ["【跨会话认知记忆索引 (当前工作区与最近经历)】", f"- 当前沙箱目录: {norm_curr}"]
        for ws in ws_list:
            tags = f" [{'/'.join(ws.tags[:3])}]" if ws.tags else ""
            lines.append(f"- 活跃工程{' (当前)' if ws.path == norm_curr else ''}: {ws.name}{tags} -> {ws.path}")
        for ep in ep_list:
            lines.append(f"- 近期情景 [{ep.project_name} | {ep.session_id}]: {ep.intent[:60]} -> {ep.summary[:80]}")
        for f in fact_list:
            lines.append(f"- 长期认知 ({f.scope.value}): {f.content[:90]}")

        footer = "提示: 谈起其它项目或过往对话时可调用 recall_memory 联想回忆，需访问其源码时调用 switch_workspace 切目录。"
        out: List[str] = []
        used = 0
        budget = max(0, max_chars - len(footer) - 2)
        for line in lines:
            cost = len(line) + (1 if out else 0)
            if used + cost > budget:
                break
            out.append(line)
            used += cost
        if len(footer) <= max_chars - used:
            out.append(footer)
        return "\n".join(out)[:max_chars]

    def clear(self, scope: Optional[MemoryScope] = None) -> None:
        """Clear facts/episodes selectively or wipe all cognitive records."""
        with self._lock:
            if scope is None:
                self.workspaces.clear()
                self.episodes.clear()
                self.facts.clear()
            else:
                self.facts = [f for f in self.facts if f.scope is not scope]
            self._save()
