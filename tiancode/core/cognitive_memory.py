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
from typing import Any, Dict, List, Optional, Sequence, Set, Tuple, Union

from langchain_core.messages import BaseMessage, HumanMessage

from tiancode.domain.memory import EpisodeRecord, MemoryScope, RecallHit, SemanticFact, WorkspaceEntity

MAX_WORKSPACES, MAX_EPISODES, MAX_FACTS = 12, 50, 100
MEMORY_SCHEMA_VERSION = 1
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


def _memory_schema_readable(data: Dict[str, Any]) -> bool:
    """True when this process understands the memory document version.

    Missing schema_version is version 0 and uses the current field names.
    """
    if "schema_version" not in data:
        return True
    version = data["schema_version"]
    if isinstance(version, bool) or not isinstance(version, int):
        return False
    return version <= MEMORY_SCHEMA_VERSION


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


_PITFALL_MARKERS = ("❌", "Error:", "Traceback", "Exit ", "异常", "rollback_turn")


def _extract_pitfalls(messages: Sequence[Any], max_items: int = 2) -> List[str]:
    """Distill concise pitfall lessons from tool errors and rollbacks without LLM calls."""
    pitfalls: List[str] = []
    seen: set[str] = set()
    for msg in messages:
        role, text = _msg_role_and_text(msg)
        if role != "tool" or not text:
            continue
        if not any(m in text for m in _PITFALL_MARKERS):
            continue
        first_line = next((ln.strip() for ln in text.splitlines() if ln.strip()), "")
        snippet = first_line[:120].replace("\n", " ")
        if snippet and snippet not in seen:
            seen.add(snippet)
            pitfalls.append(snippet)
            if len(pitfalls) >= max_items:
                break
    return pitfalls


def _jaccard_overlap(tokens_a: Sequence[str], tokens_b: Sequence[str]) -> float:
    """Compute token-set Jaccard similarity for lightweight Mem0-style conflict detection."""
    if not tokens_a or not tokens_b:
        return 0.0
    sa, sb = set(tokens_a), set(tokens_b)
    return len(sa & sb) / len(sa | sb)


def _smart_clip(text: str, limit: int) -> str:
    """Clip text cleanly near punctuation or word boundaries to avoid mid-word cuts."""
    clean = text.replace("\n", " ").strip()
    if len(clean) <= limit:
        return clean
    window = clean[:limit]
    for sep in ("。", "；", "！", "？", ". ", "; ", " "):
        pos = window.rfind(sep)
        if pos >= int(limit * 0.6):
            return window[: pos + (0 if sep == " " else 1)].rstrip()
    return window.rstrip()


class CognitiveMemoryStore:
    """Thread-safe atomic JSON store for cross-session Workspace, Episodic, and Semantic memories."""

    def __init__(self, memory_path: Optional[Path] = None) -> None:
        self.memory_path = _memory_file_path(memory_path) if memory_path is not None else get_default_memory_path()
        self._lock = threading.Lock()
        self.workspaces: Dict[str, WorkspaceEntity] = {}
        self.episodes: List[EpisodeRecord] = []
        self.facts: List[SemanticFact] = []
        self._persist_blocked = False
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
        if not isinstance(data, dict) or not _memory_schema_readable(data):
            # A newer or unreadable schema must not be parsed and then saved
            # back as version 1, which would drop fields this process does not know.
            self._persist_blocked = True
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
        if self._persist_blocked:
            return
        payload = {
            "schema_version": MEMORY_SCHEMA_VERSION,
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
        """Store or evolve a semantic fact; matching keys update in-place, conflicting facts supersede gracefully."""
        clean = content.strip()
        if not clean:
            raise ValueError("记忆内容不能为空")
        scope_enum = scope if isinstance(scope, MemoryScope) else MemoryScope.from_value(scope)
        norm_ws = _norm_path(workspace_path) if (scope_enum is MemoryScope.PROJECT and workspace_path) else ""
        norm_key, now = key.strip().lower() or _hash_id(clean), _now_iso()
        new_tokens = tokenize_search_terms(clean)

        with self._lock:
            for idx, f in enumerate(self.facts):
                if f.is_active and f.scope is scope_enum and f.workspace_path == norm_ws and f.key == norm_key:
                    updated = SemanticFact(
                        f.fact_id, norm_key, clean, scope_enum, norm_ws,
                        source_session_id or f.source_session_id, max(f.importance, importance),
                        now, f.access_count + 1, valid_from=f.valid_from or now,
                    )
                    self.facts[idx] = updated
                    self._save()
                    return updated

            new_id = f"fact_{_hash_id(norm_ws + norm_key + now)[:10]}"
            # Auto-supersede conflicting active facts in the same scope/workspace when no explicit key matched
            if not key.strip():
                for idx, f in enumerate(self.facts):
                    if f.is_active and f.scope is scope_enum and f.workspace_path == norm_ws:
                        if _jaccard_overlap(new_tokens, tokenize_search_terms(f.content)) >= 0.55:
                            self.facts[idx] = SemanticFact(
                                f.fact_id, f.key, f.content, f.scope, f.workspace_path,
                                f.source_session_id, f.importance, f.updated_at, f.access_count,
                                valid_from=f.valid_from, invalid_at=now, superseded_by=new_id,
                            )

            fact = SemanticFact(new_id, norm_key, clean, scope_enum, norm_ws, source_session_id, importance, now, 1, valid_from=now)
            self.facts = sorted(self.facts + [fact], key=lambda x: (x.is_active, x.importance, x.updated_at), reverse=True)[:MAX_FACTS]
            self._save()
            return fact

    def invalidate_fact(
        self, query_or_key: str, *, scope: Optional[MemoryScope] = None, workspace_path: Optional[str] = None,
    ) -> int:
        """Bi-temporally invalidate matching active facts without erasing audit history."""
        target = query_or_key.strip().lower()
        if not target:
            return 0
        q_tokens = tokenize_search_terms(target)
        norm_ws = _norm_path(workspace_path) if workspace_path else None
        now, count = _now_iso(), 0
        with self._lock:
            for idx, f in enumerate(self.facts):
                if not f.is_active:
                    continue
                if scope is not None and f.scope is not scope:
                    continue
                if norm_ws is not None and f.workspace_path and f.workspace_path != norm_ws:
                    continue
                f_tokens = set(tokenize_search_terms(f.content))
                q_set = set(q_tokens)
                q_coverage = (len(q_set & f_tokens) / len(q_set)) if q_set else 0.0
                matched = f.key == target or (target in f.content.lower()) or q_coverage >= 0.6
                if matched:
                    self.facts[idx] = SemanticFact(
                        f.fact_id, f.key, f.content, f.scope, f.workspace_path,
                        f.source_session_id, f.importance, f.updated_at, f.access_count,
                        valid_from=f.valid_from, invalid_at=now, superseded_by="user_invalidated",
                    )
                    count += 1
            if count:
                self._save()
        return count

    def list_facts(
        self, scope: Optional[MemoryScope] = None, workspace_path: Optional[str] = None, *, include_inactive: bool = False,
    ) -> List[SemanticFact]:
        """List stored semantic facts optionally filtered by scope, workspace, and active status."""
        norm_ws = _norm_path(workspace_path) if workspace_path else None
        with self._lock:
            return [
                f for f in self.facts
                if (include_inactive or f.is_active)
                and (scope is None or f.scope is scope)
                and (norm_ws is None or f.workspace_path == norm_ws)
            ]

    def consolidate_session(
        self, session_id: str, work_dir: Union[Path, str], messages: Sequence[Any],
        *, plan: Any = None, modified_files: Optional[Sequence[str]] = None,
    ) -> EpisodeRecord:
        """Deterministically distill an EpisodeRecord from session messages (0 LLM calls)."""
        ws = self.touch_workspace(work_dir, session_id=session_id)
        parsed = [_msg_role_and_text(m) for m in messages]
        human_texts = [txt for role, txt in parsed if role == "human" and txt]
        ai_texts = [txt for role, txt in parsed if role == "ai" and txt]

        intent = _smart_clip(human_texts[-1] if human_texts else (getattr(plan, "task_title", "") or "常规工作会话"), 180)
        tail_ai = _smart_clip(ai_texts[-1], 220) if ai_texts else ""
        files = list(dict.fromkeys(modified_files or []))
        pitfalls = _extract_pitfalls(messages)
        summary = " | ".join(p for p in (f"涉及文件: {', '.join(files[:8])}" if files else "", tail_ai) if p) or "已完成对话交互"
        entities = list(dict.fromkeys(files + re.findall(r"\b[A-Za-z_][A-Za-z0-9_.-]{3,}\b", intent + " " + tail_ai)[:12]))

        now = _now_iso()
        with self._lock:
            created = next((e.created_at for e in self.episodes if e.session_id == session_id), now)
            ep = EpisodeRecord(f"ep_{_hash_id(session_id)}", session_id, ws.path, ws.name, created, now, intent, summary, entities, pitfalls)
            self.episodes = sorted([e for e in self.episodes if e.session_id != session_id] + [ep], key=lambda e: e.updated_at, reverse=True)[:MAX_EPISODES]
            self._save()
            return ep

    def _expand_entity_hops(
        self, direct_entities: Sequence[str], seen_snippets: Set[str], norm_curr: str,
    ) -> List[RecallHit]:
        """Perform zero-DB 1-hop associative expansion across shared entities and files."""
        if not direct_entities:
            return []
        ent_low = [e.lower() for e in direct_entities[:6] if len(e) >= 3]
        expanded: List[RecallHit] = []
        for f in self.facts:
            if not f.is_active or f.content in seen_snippets:
                continue
            blob = f"{f.key} {f.content}".lower()
            if any(e in blob for e in ent_low):
                boost = 1.25 if (norm_curr and f.workspace_path == norm_curr) else 1.0
                label = Path(f.workspace_path).name if f.workspace_path else "全局关联"
                expanded.append(RecallHit("fact", round(0.55 * f.importance * boost, 4), label, f.content, f.updated_at, f.source_session_id, f.workspace_path))
                seen_snippets.add(f.content)
        return expanded

    def _has_invalidated_fact_overlap(self, text: str, inactive_tokens: Sequence[Set[str]]) -> bool:
        """Detect when historical dialogue snippets reference a superseded or forgotten fact."""
        if not inactive_tokens:
            return False
        snippet_tokens = set(tokenize_search_terms(text))
        if not snippet_tokens:
            return False
        return any(len(ft & snippet_tokens) / len(ft) >= 0.65 for ft in inactive_tokens if ft)

    def recall(self, query: str, *, current_work_dir: Optional[Union[Path, str]] = None, session_store: Any = None, top_k: int = 6) -> List[RecallHit]:
        """Associatively rank active facts, episodes (with 1-hop entity expansion & workspace affinity), and history."""
        q_tokens = tokenize_search_terms(query)
        norm_curr = _norm_path(current_work_dir) if current_work_dir else ""
        hits: List[RecallHit] = []
        direct_entities: List[str] = []
        seen_snippets: Set[str] = set()

        with self._lock:
            inactive_tokens = [set(tokenize_search_terms(f.content)) for f in self.facts if not f.is_active]
            for idx, f in enumerate(self.facts):
                if not f.is_active:
                    continue
                sc = _score_match(q_tokens, f"{f.key} {f.content} {f.workspace_path}", f.updated_at, f.importance)
                if sc > 0:
                    affinity = 1.35 if (norm_curr and f.workspace_path == norm_curr) else 1.0
                    label = Path(f.workspace_path).name if f.workspace_path else "全局记忆"
                    hits.append(RecallHit("fact", round(sc * affinity, 4), label, f.content, f.updated_at, f.source_session_id, f.workspace_path))
                    seen_snippets.add(f.content)
                    self.facts[idx].access_count += 1
            for ep in self.episodes:
                blob = f"{ep.project_name} {ep.workspace_path} {ep.intent} {ep.summary} {' '.join(ep.entities)} {' '.join(ep.pitfalls)}"
                sc = _score_match(q_tokens, blob, ep.updated_at, 0.85)
                if sc > 0:
                    affinity = 1.35 if (norm_curr and ep.workspace_path == norm_curr) else 1.0
                    pitfall_note = f" | 教训: {ep.pitfalls[0]}" if ep.pitfalls else ""
                    snippet = f"意图: {ep.intent} | 结论: {ep.summary}{pitfall_note}"
                    stale_factor = 0.4 if self._has_invalidated_fact_overlap(snippet, inactive_tokens) else 1.0
                    if stale_factor < 1.0:
                        snippet = f"[含已废止旧规则] {snippet}"
                    hits.append(RecallHit("episode", round(sc * affinity * stale_factor, 4), f"{ep.project_name} [{ep.session_id}]", snippet, ep.updated_at, ep.session_id, ep.workspace_path))
                    direct_entities.extend(ep.entities)
            hits.extend(self._expand_entity_hops(list(dict.fromkeys(direct_entities)), seen_snippets, norm_curr))

        if session_store is not None and hasattr(session_store, "search_history"):
            for item in session_store.search_history(query, max_hits=top_k):
                sc = _score_match(q_tokens, f"{item['project']} {item['snippet']}", item["updated_at"], 0.65)
                if sc > 0:
                    same_ws = norm_curr and _norm_path(item["work_dir"]) == norm_curr
                    affinity = 1.25 if same_ws else (0.65 if norm_curr else 1.0)
                    snippet = item["snippet"]
                    stale_factor = 0.4 if self._has_invalidated_fact_overlap(snippet, inactive_tokens) else 1.0
                    if stale_factor < 1.0:
                        snippet = f"[含已废止旧规则] {snippet}"
                    hits.append(RecallHit("session_message", round(sc * affinity * stale_factor, 4), f"{item['project']} [{item['session_id']}]", snippet, item["updated_at"], item["session_id"], item["work_dir"]))

        hits.sort(key=lambda h: h.score, reverse=True)
        return hits[:max(1, top_k)]

    def format_priming_context(self, current_work_dir: Union[Path, str], *, max_chars: int = 1200) -> str:
        """Synthesize budget-aware subconscious priming index for SystemMessage injection."""
        norm_curr = _norm_path(current_work_dir)
        with self._lock:
            ws_list = sorted(self.workspaces.values(), key=lambda w: w.last_active_at, reverse=True)[:4]
            ep_list = self.episodes[:4]
            fact_list = [f for f in self.facts if f.is_active and (f.scope is MemoryScope.GLOBAL or f.workspace_path == norm_curr)][:5]
            curr_pitfalls = [p for ep in self.episodes if ep.workspace_path == norm_curr for p in ep.pitfalls][:1]

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
        for p in curr_pitfalls:
            lines.append(f"- ⚠️ 历史避坑提示: {p[:100]}")

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
