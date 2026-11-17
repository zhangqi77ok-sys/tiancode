"""Session persistence manager for saving, listing, and resuming conversation state."""

import json
import os
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


def get_default_sessions_dir() -> Path:
    """Return ~/.tiancode/sessions directory path."""
    env_override = os.environ.get("TIANCODE_SESSIONS_PATH")
    if env_override:
        return Path(env_override).resolve()
    return Path.home() / ".tiancode" / "sessions"


class SessionStore:
    """Manages saving, loading, listing, and rehydrating TianCode sessions."""

    def __init__(self, storage_dir: Optional[Path] = None) -> None:
        self.storage_dir = (storage_dir or get_default_sessions_dir()).resolve()
        self.storage_dir.mkdir(parents=True, exist_ok=True)

    def _file_path(self, session_id: str) -> Path:
        return self.storage_dir / f"{session_id}.json"

    def save(
        self,
        session_id: str,
        work_dir: str,
        messages: List[Any],
        plan: Optional[TaskPlan] = None,
    ) -> SessionMetadata:
        """Serialize session state and atomic task plan to disk."""
        now_iso = datetime.now(timezone.utc).isoformat()
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
            session_id=session_id,
            work_dir=str(work_dir),
            created_at=created_at,
            updated_at=now_iso,
            task_title=task_title,
            steps_total=steps_total,
            steps_completed=steps_completed,
            message_count=len(serialized_msgs),
        )

        payload = {
            "metadata": asdict(metadata),
            "messages": serialized_msgs,
            "plan": plan_dict,
        }

        with open(file_path, "w", encoding="utf-8") as f:
            json.dump(payload, f, indent=2, ensure_ascii=False)

        return metadata

    def load(self, session_id: str) -> Optional[PersistedSession]:
        """Load session state from disk."""
        file_path = self._file_path(session_id)
        if not file_path.exists():
            return None

        try:
            with open(file_path, "r", encoding="utf-8") as f:
                data = json.load(f)

            meta_data = data.get("metadata", {})
            metadata = SessionMetadata(
                session_id=meta_data.get("session_id", session_id),
                work_dir=meta_data.get("work_dir", "."),
                created_at=meta_data.get("created_at", ""),
                updated_at=meta_data.get("updated_at", ""),
                task_title=meta_data.get("task_title", ""),
                steps_total=meta_data.get("steps_total", 0),
                steps_completed=meta_data.get("steps_completed", 0),
                message_count=meta_data.get("message_count", 0),
            )
            return PersistedSession(
                metadata=metadata,
                messages=data.get("messages", []),
                plan=data.get("plan"),
            )
        except Exception:
            return None

    def rehydrate_plan(self, plan_dict: Optional[Dict[str, Any]]) -> Optional[TaskPlan]:
        """Convert serialized plan dictionary back to TaskPlan instance."""
        if not plan_dict:
            return None
        steps = []
        for s in plan_dict.get("steps", []):
            steps.append(
                PlanStep(
                    step_id=s.get("step_id", 0),
                    title=s.get("title", ""),
                    description=s.get("description", ""),
                    status=StepStatus(s.get("status", "todo")),
                    verification=s.get("verification", ""),
                    result_summary=s.get("result_summary", ""),
                )
            )
        return TaskPlan(
            task_title=plan_dict.get("task_title", ""),
            steps=steps,
            current_step_index=plan_dict.get("current_step_index", 0),
        )

    def list_sessions(self) -> List[SessionMetadata]:
        """List all saved sessions sorted by updated_at descending."""
        sessions: List[SessionMetadata] = []
        for p in self.storage_dir.glob("*.json"):
            persisted = self.load(p.stem)
            if persisted:
                sessions.append(persisted.metadata)
        sessions.sort(key=lambda s: s.updated_at, reverse=True)
        return sessions

    def get_latest_session_id(self) -> Optional[str]:
        """Return the ID of the most recently updated session."""
        sessions = self.list_sessions()
        return sessions[0].session_id if sessions else None

    def delete(self, session_id: str) -> bool:
        """Delete a saved session file."""
        file_path = self._file_path(session_id)
        if file_path.exists():
            file_path.unlink(missing_ok=True)
            return True
        return False
