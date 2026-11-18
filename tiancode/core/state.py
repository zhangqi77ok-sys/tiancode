"""State helpers and context window trimming for TianCode."""

from typing import List
from langchain_core.messages import BaseMessage
from tiancode.core.compactor import ContextCompactor


def prune_messages(
    messages: List[BaseMessage],
    max_retained: int = 40,
    max_context_chars: int = 60000,
) -> List[BaseMessage]:
    """Prune history safely while preserving recent reads until budget pressure rises."""
    total_chars = sum(len(str(getattr(m, "content", ""))) for m in messages)
    if max_context_chars < 4000:
        recent_tools, tool_char_cap = 2, 300
    elif total_chars < int(max_context_chars * 0.65):
        recent_tools, tool_char_cap = 20, 12000
    else:
        recent_tools, tool_char_cap = 10, 800

    compactor = ContextCompactor(
        max_retained_messages=max_retained,
        active_recent_tools=recent_tools,
        max_tool_output_chars=tool_char_cap,
        max_context_chars=max_context_chars,
    )
    return compactor.compact(messages)
