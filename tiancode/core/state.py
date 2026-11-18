"""State helpers and context window trimming for TianCode."""

from typing import List
from langchain_core.messages import BaseMessage
from tiancode.core.compactor import ContextCompactor


def prune_messages(
    messages: List[BaseMessage],
    max_retained: int = 14,
    max_context_chars: int = 40000,
) -> List[BaseMessage]:
    """Prune conversation history safely using ContextCompactor."""
    compactor = ContextCompactor(
        max_retained_messages=max_retained,
        max_context_chars=max_context_chars,
    )
    return compactor.compact(messages)
