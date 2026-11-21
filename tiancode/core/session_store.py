"""Session persistence manager for saving, listing, and resuming conversation state."""

import json
import os
import re
import urllib.parse
from dataclasses import asdict
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Dict, List, Optional
from langchain_core.messages import BaseMessage, message_to_dict
from tiancode.domain.models import (
    PersistedSession,
    PlanStep,
    SessionMetadata,
    StepStatus,
    TaskPlan,
)


def _replace_private_json(path: Path, payload: Dict[str, Any]) -> None:
    """Write JSON as a private regular file. Do not follow a symlink at the temp name."""
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


def get_default_sessions_dir() -> Path:
    """Return ~/.tiancode/sessions directory path."""
    env_override = os.environ.get("TIANCODE_SESSIONS_PATH")
    if env_override:
        return Path(env_override).resolve()
    return Path.home() / ".tiancode" / "sessions"


def encode_session_id(session_id: str) -> str:
    """Lossless, collision-free encoding for session IDs onto filesystem-safe names."""
    if not session_id:
        return "session_default"
    res = []
    for ch in session_id.strip():
        if ch.isalnum() or ch in ("-", "_"):
            res.append(ch)
        else:
            res.append(f"%{ord(ch):02X}")
    return "".join(res) or "session_default"


def decode_session_id(encoded: str) -> str:
    """Decode encoded filesystem session name back to original session ID."""
    return urllib.parse.unquote(encoded)


def sanitize_session_id(session_id: str) -> str:
    """Backward-compatible wrapper routing to lossless encode_session_id."""
    return encode_session_id(session_id)


class SessionStore:
    """Manages saving, loading, listing, and rehydrating TianCode sessions."""

    def __init__(self, storage_dir: Optional[Path] = None, sessions_dir: Optional[Path] = None) -> None:
        chosen = sessions_dir or storage_dir or get_default_sessions_dir()
        self.storage_dir = Path(chosen).resolve()
        self.sessions_dir = self.storage_dir
        self.storage_dir.mkdir(parents=True, exist_ok=True)

    def _file_path(self, session_id: str) -> Path:
        safe_id = encode_session_id(session_id)
        file_path = (self.storage_dir / f"{safe_id}.json").resolve()
        if not file_path.is_relative_to(self.storage_dir):
            raise PermissionError(f"会话路径越界拦截: {session_id}")
        if not file_path.exists():
            direct = (self.storage_dir / f"{session_id}.json").resolve()
            if direct.is_relative_to(self.storage_dir) and direct.exists():
                return direct
        return file_path

    def save(
        self,
        session_id: str,
        work_dir: str,
        messages: List[Any],
        plan: Optional[TaskPlan] = None,
        snapshots: Optional[List[Dict[str, Any]]] = None,
        total_tokens: int = 0,
        estimated_cost: float = 0.0,
        usage: Optional[Dict[str, Any]] = None,
    ) -> SessionMetadata:
        """Serialize session state, atomic task plan, and file snapshots to disk atomically."""
        now_iso = datetime.now(timezone.utc).isoformat()
        safe_id = encode_session_id(session_id)
        file_path = self._file_path(session_id)

        created_at = now_iso
        if file_path.exists():
            try:
                with open(file_path, "r", encoding="utf-8") as f:
                    old_data = json.load(f)
                    created_at = old_data.get("metadata", {}).get("created_at", now_iso)
            except Exception:
                pass

        serialized_msgs = []
        for m in messages:
            if isinstance(m, BaseMessage):
                serialized_msgs.append(message_to_dict(m))
            elif isinstance(m, dict):
                serialized_msgs.append(m)

        plan_dict = asdict(plan) if plan else None
        steps_total = len(plan.steps) if plan else 0
        steps_completed = sum(1 for s in plan.steps if s.status == StepStatus.COMPLETED) if plan else 0
        task_title = plan.task_title if plan else ""

        metadata = SessionMetadata(
            session_id=safe_id,
            work_dir=str(work_dir),
            created_at=created_at,
            updated_at=now_iso,
            task_title=task_title,
            steps_total=steps_total,
            steps_completed=steps_completed,
            message_count=len(serialized_msgs),
            total_tokens=total_tokens,
            estimated_cost=estimated_cost,
        )

        payload = {
            "metadata": asdict(metadata),
            "messages": serialized_msgs,
            "plan": plan_dict,
            "snapshots": snapshots or [],
            "usage": usage,
        }

        try:
            _replace_private_json(file_path, payload)
        except OSError as exc:
            raise IOError(f"无法保存会话文件 [{file_path}]: {exc}") from exc

        return metadata

    def load(self, session_id: str) -> Optional[PersistedSession]:
        """Load session state from disk safely."""
        try:
            file_path = self._file_path(session_id)
        except Exception:
            return None

        if not file_path.exists():
            return None

        try:
            with open(file_path, "r", encoding="utf-8") as f:
                data = json.load(f)

            meta_data = data.get("metadata", {})
            metadata = SessionMetadata(
                session_id=meta_data.get("session_id", sanitize_session_id(session_id)),
                work_dir=meta_data.get("work_dir", "."),
                created_at=meta_data.get("created_at", ""),
                updated_at=meta_data.get("updated_at", ""),
                task_title=meta_data.get("task_title", ""),
                steps_total=meta_data.get("steps_total", 0),
                steps_completed=meta_data.get("steps_completed", 0),
                message_count=meta_data.get("message_count", 0),
                total_tokens=meta_data.get("total_tokens", 0),
                estimated_cost=meta_data.get("estimated_cost", 0.0),
            )

            raw_plan = data.get("plan")
            plan_obj = self.rehydrate_plan(raw_plan) if raw_plan else None

            return PersistedSession(
                metadata=metadata,
                messages=data.get("messages", []),
                plan=raw_plan,
                snapshots=data.get("snapshots", []),
                usage=data.get("usage"),
            )
        except Exception:
            return None

    def list_sessions(self) -> List[SessionMetadata]:
        """List all valid persisted sessions sorted by updated_at descending."""
        results: List[SessionMetadata] = []
        for p in self.storage_dir.glob("*.json"):
            try:
                with open(p, "r", encoding="utf-8") as f:
                    data = json.load(f)
                meta_data = data.get("metadata", {})
                results.append(
                    SessionMetadata(
                        session_id=meta_data.get("session_id", decode_session_id(p.stem)),
                        work_dir=meta_data.get("work_dir", "."),
                        created_at=meta_data.get("created_at", ""),
                        updated_at=meta_data.get("updated_at", ""),
                        task_title=meta_data.get("task_title", ""),
                        steps_total=meta_data.get("steps_total", 0),
                        steps_completed=meta_data.get("steps_completed", 0),
                        message_count=meta_data.get("message_count", 0),
                        total_tokens=meta_data.get("total_tokens", 0),
                        estimated_cost=meta_data.get("estimated_cost", 0.0),
                    )
                )
            except Exception:
                continue

        results.sort(key=lambda s: s.updated_at, reverse=True)
        return results

    def get_latest_session_id(self) -> Optional[str]:
        """Return the most recently updated session ID or None."""
        sessions = self.list_sessions()
        return sessions[0].session_id if sessions else None

    def search_history(self, query: str, *, max_sessions: int = 12, max_hits: int = 5) -> List[Dict[str, str]]:
        """Scan persisted session transcripts using CJK bigrams & ASCII terms for associative recall."""
        from tiancode.core.cognitive_memory import tokenize_search_terms

        terms = tokenize_search_terms(query)
        if not terms:
            return []
        scored: List[tuple[int, Dict[str, str]]] = []
        for meta in self.list_sessions()[:max_sessions]:
            sess = self.load(meta.session_id)
            if not sess:
                continue
            for raw_msg in reversed(sess.messages):
                data = raw_msg.get("data", {}) if isinstance(raw_msg.get("data"), dict) else raw_msg
                content = str(data.get("content", "")).strip()
                if not content:
                    continue
                low = f"{meta.work_dir} {content}".lower()
                hit_count = sum(1 for t in terms if t in low)
                if hit_count > 0:
                    scored.append((hit_count, {
                        "session_id": meta.session_id,
                        "work_dir": meta.work_dir,
                        "project": Path(meta.work_dir).name or meta.work_dir,
                        "updated_at": meta.updated_at,
                        "snippet": content[:200].replace("\n", " "),
                    }))
                    break
        scored.sort(key=lambda item: item[0], reverse=True)
        return [item for _, item in scored[:max_hits]]

    def delete(self, session_id: str) -> bool:
        """Remove a session file from disk."""
        try:
            file_path = self._file_path(session_id)
            if file_path.exists():
                file_path.unlink()
                return True
        except Exception:
            pass
        return False

    @staticmethod
    def rehydrate_plan(raw_plan: Dict[str, Any]) -> Optional[TaskPlan]:
        """Reconstruct a TaskPlan dataclass instance from raw dictionary."""
        if not raw_plan:
            return None
        steps = []
        for s in raw_plan.get("steps", []):
            status_val = s.get("status", "todo")
            try:
                status_enum = StepStatus(status_val)
            except ValueError:
                status_enum = StepStatus.TODO

            step_id_val = s.get("step_id") or s.get("id") or (len(steps) + 1)
            try:
                step_id_int = int(step_id_val)
            except (ValueError, TypeError):
                step_id_int = len(steps) + 1

            steps.append(
                PlanStep(
                    step_id=step_id_int,
                    title=s.get("title", ""),
                    description=s.get("description", ""),
                    status=status_enum,
                    verification=s.get("verification", ""),
                    result_summary=s.get("result_summary") or s.get("summary", ""),
                )
            )
        return TaskPlan(
            task_title=raw_plan.get("task_title", ""),
            steps=steps,
            current_step_index=raw_plan.get("current_step_index", 0),
        )
