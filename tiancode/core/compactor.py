"""Context compaction and schema-safe message pruning for long-running sessions."""

from typing import List, Set
from langchain_core.messages import AIMessage, BaseMessage, HumanMessage, ToolMessage


class ContextCompactor:
    """Manages context window optimization through observation masking and schema-safe trimming."""

    def __init__(
        self,
        max_retained_messages: int = 14,
        active_recent_tools: int = 2,
        max_tool_output_chars: int = 300,
    ) -> None:
        self.max_retained_messages = max_retained_messages
        self.active_recent_tools = active_recent_tools
        self.max_tool_output_chars = max_tool_output_chars

    def mask_observations(self, messages: List[BaseMessage]) -> List[BaseMessage]:
        """Fold older ToolMessage contents to prevent token explosion while preserving API contract."""
        tool_indices = [i for i, m in enumerate(messages) if isinstance(m, ToolMessage)]
        if not tool_indices:
            return list(messages)

        # Retain the most recent k tool outputs intact for immediate debugging
        protected_indices: Set[int] = set(tool_indices[-self.active_recent_tools :])

        processed: List[BaseMessage] = []
        for i, msg in enumerate(messages):
            if isinstance(msg, ToolMessage) and i not in protected_indices:
                if len(msg.content) > self.max_tool_output_chars:
                    tombstone = f"[历史输出已折叠 (原长度 {len(msg.content)} 字符，最新状态以工作区文件为准)]"
                    pruned_tool = ToolMessage(
                        content=tombstone,
                        tool_call_id=msg.tool_call_id,
                        name=msg.name,
                    )
                    processed.append(pruned_tool)
                    continue
            processed.append(msg)
        return processed

    def adjust_slice_boundary(self, messages: List[BaseMessage], start_idx: int) -> int:
        """Adjust boundary to prevent slicing an AIMessage away from its dependent ToolMessage."""
        if start_idx <= 1:
            return 1

        target = messages[start_idx]
        if isinstance(target, ToolMessage):
            # Scan backwards to locate the parent AIMessage holding the tool_call
            for back_idx in range(start_idx - 1, 0, -1):
                prev = messages[back_idx]
                if isinstance(prev, AIMessage) and any(
                    tc.get("id") == target.tool_call_id for tc in getattr(prev, "tool_calls", [])
                ):
                    return back_idx
        return start_idx

    def compact(self, messages: List[BaseMessage]) -> List[BaseMessage]:
        """Perform multi-tier compaction: observation masking followed by schema-safe trimming."""
        if len(messages) <= 2:
            return list(messages)

        # 1. Observation Masking (Fold older bloated ToolMessages)
        masked_msgs = self.mask_observations(messages)

        # 2. If message count is within threshold, return directly
        if len(masked_msgs) <= self.max_retained_messages:
            return masked_msgs

        # 3. Macro Slicing: always retain the initial user prompt (messages[0])
        first_msg = masked_msgs[0]
        tail_count = self.max_retained_messages - 1
        raw_start_idx = len(masked_msgs) - tail_count

        safe_start_idx = self.adjust_slice_boundary(masked_msgs, raw_start_idx)
        tail_msgs = masked_msgs[safe_start_idx:]

        return [first_msg] + tail_msgs
