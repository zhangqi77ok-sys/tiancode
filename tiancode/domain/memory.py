"""Cognitive tri-tier memory domain models (Working / Episodic / Semantic)."""

from dataclasses import dataclass, field
from enum import Enum
from typing import List


class MemoryScope(str, Enum):
    """Namespace boundary isolating global preferences from project-specific knowledge."""

    GLOBAL = "global"
    PROJECT = "project"

    @classmethod
    def from_value(cls, value: str) -> "MemoryScope":
        normalized = str(value).strip().lower()
        for member in cls:
            if member.value == normalized:
                return member
        raise ValueError(f"不支持的记忆作用域 '{value}'，可选值: global, project")


@dataclass
class WorkspaceEntity:
    """Persistent cognitive profile for a project workspace across sessions."""

    workspace_id: str
    name: str
    path: str
    tags: List[str] = field(default_factory=list)
    last_active_at: str = ""
    session_ids: List[str] = field(default_factory=list)


@dataclass
class EpisodeRecord:
    """Compressed episodic card summarizing a session's intent, artifacts, and outcome."""

    episode_id: str
    session_id: str
    workspace_path: str
    project_name: str
    created_at: str
    updated_at: str
    intent: str
    summary: str
    entities: List[str] = field(default_factory=list)


@dataclass
class SemanticFact:
    """Evolving long-term fact, convention, or alias with conflict-free key replacement."""

    fact_id: str
    key: str
    content: str
    scope: MemoryScope
    workspace_path: str = ""
    source_session_id: str = ""
    importance: float = 0.8
    updated_at: str = ""
    access_count: int = 1


@dataclass(frozen=True)
class RecallHit:
    """Ranked associative memory match surfaced to Working Memory."""

    source_type: str
    score: float
    context_label: str
    snippet: str
    timestamp: str = ""
    session_id: str = ""
    workspace_path: str = ""
