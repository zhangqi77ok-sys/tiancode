"""State helpers and context window trimming for TianCode."""

from typing import List
from langchain_core.messages import BaseMessage, SystemMessage
from langgraph.graph import MessagesState


def prune_messages(messages: List[BaseMessage], max_retained: int = 12) -> List[BaseMessage]:
    """Prune conversation history while preserving the first task instruction and recent turns."""
    if len(messages) <= max_retained:
        return list(messages)

    # Keep the initial user instruction/context and the most recent (max_retained - 1) turns
    first_msg = messages[0]
    tail_msgs = messages[-(max_retained - 1):]
    return [first_msg] + tail_msgs
