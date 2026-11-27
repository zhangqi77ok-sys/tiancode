"""Context compaction, observation masking, and token budget guard for TianCode."""

from dataclasses import dataclass
from typing import List, Set
from langchain_core.messages import AIMessage, BaseMessage, HumanMessage, SystemMessage, ToolMessage


@dataclass
class CompactionResult:
    """Statistics and output messages resulting from a compaction pass."""

    messages: List[BaseMessage]
    original_chars: int
    compacted_chars: int
    folded_count: int


class ContextCompactor:
    """Manages context window optimization through observation masking, token budget guard, and schema-safe trimming."""

    def __init__(
        self,
        max_retained_messages: int = 14,
        active_recent_tools: int = 2,
        max_tool_output_chars: int = 300,
        max_context_chars: int = 40000,
    ) -> None:
        self.max_retained_messages = max_retained_messages
        self.active_recent_tools = active_recent_tools
        self.max_tool_output_chars = max_tool_output_chars
        self.max_context_chars = max_context_chars

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

    @staticmethod
    def _extract_highlights(dropped_messages: List[BaseMessage]) -> List[str]:
        """Extract up to 3 concise error lessons and AI synthesis conclusions from dropped history."""
        errors: List[str] = []
        last_ai_note = ""
        for msg in dropped_messages:
            if isinstance(msg, ToolMessage):
                txt = str(getattr(msg, "content", "") or "")
                if any(tok in txt for tok in ("❌", "Error:", "Traceback", "Exit Code:")):
                    first_line = next((ln.strip() for ln in txt.splitlines() if ln.strip()), "")
                    if first_line and first_line[:90] not in errors and len(errors) < 2:
                        errors.append(first_line[:90])
            elif isinstance(msg, AIMessage):
                txt = str(getattr(msg, "content", "") or "").strip().replace("\n", " ")
                if txt and not getattr(msg, "tool_calls", None):
                    last_ai_note = txt[:100]
        highlights = [f"避坑: {e}" for e in errors]
        if last_ai_note:
            highlights.append(f"前序结论: {last_ai_note}")
        return highlights

    def generate_compaction_card(self, dropped_messages: List[BaseMessage]) -> SystemMessage:
        """Synthesize a dense, schema-safe Compaction Card summarizing folded history."""
        touched_files: Set[str] = set()
        tool_call_count = 0
        tool_msg_count = 0

        for msg in dropped_messages:
            if isinstance(msg, AIMessage):
                tool_calls = getattr(msg, "tool_calls", None) or []
                tool_call_count += len(tool_calls)
                for tc in tool_calls:
                    args = tc.get("args") if isinstance(tc, dict) else getattr(tc, "args", {})
                    if isinstance(args, dict):
                        for k in ("filename", "path", "file", "target", "source"):
                            if k in args and args[k]:
                                touched_files.add(str(args[k]))
            elif isinstance(msg, ToolMessage):
                tool_msg_count += 1

        total_tools = max(tool_call_count, tool_msg_count)
        files_str = ", ".join(sorted(touched_files)) if touched_files else "无直接文件写入"
        highlights = self._extract_highlights(dropped_messages)
        highlight_line = "\n• 保留记忆: " + " | ".join(highlights) if highlights else ""

        card_content = (
            f"📋 【上下文智能折叠摘要】\n"
            f"• 历史交互: 已折叠前期 {len(dropped_messages)} 条交互记录 (释放字符预算)\n"
            f"• 涉及文件: {files_str}\n"
            f"• 历史动作: 执行了 {total_tools} 次工具调用"
            f"{highlight_line}\n"
            f"• 注意事项: 请基于【初始目标】与当前工作区真实文件继续推进后续任务。"
        )
        return SystemMessage(content=card_content)

    def _seal(self, messages: List[BaseMessage]) -> List[BaseMessage]:
        """Close open tool calls and drop orphan results after folding."""
        from tiancode.core.stream_events import close_pending_tool_calls

        return close_pending_tool_calls(list(messages))

    def compact(self, messages: List[BaseMessage]) -> List[BaseMessage]:
        """Perform multi-tier compaction: observation masking, budget-aware trimming, and card injection."""
        if len(messages) <= 2:
            return self._seal(messages)

        # 1. Observation Masking (Fold older bloated ToolMessages)
        masked_msgs = self.mask_observations(messages)

        # Calculate character budget
        total_chars = sum(len(str(getattr(m, "content", ""))) for m in masked_msgs)

        # 2. Check if trimming is triggered
        needs_trimming = (
            len(masked_msgs) > self.max_retained_messages
            or total_chars > self.max_context_chars
        )

        if not needs_trimming:
            return self._seal(masked_msgs)

        # 3. Macro Slicing: always retain the initial user prompt (messages[0])
        first_msg = masked_msgs[0]

        # Determine tail count
        tail_count = min(self.max_retained_messages - 1, len(masked_msgs) - 1)

        # If total characters exceed budget, shrink tail_count until within budget or tail_count == 1
        while tail_count > 1:
            current_tail = masked_msgs[len(masked_msgs) - tail_count :]
            chars_estimate = len(str(getattr(first_msg, "content", ""))) + sum(
                len(str(getattr(m, "content", ""))) for m in current_tail
            )
            if chars_estimate <= self.max_context_chars:
                break
            tail_count -= 1

        raw_start_idx = len(masked_msgs) - tail_count
        safe_start_idx = self.adjust_slice_boundary(masked_msgs, raw_start_idx)

        # If adjust_slice_boundary retreated to 1 due to a massive tool interaction, advance past it if possible
        if safe_start_idx <= 1 and raw_start_idx > 1 and len(masked_msgs) > 2:
            next_idx = raw_start_idx
            while next_idx < len(masked_msgs) and isinstance(masked_msgs[next_idx], ToolMessage):
                next_idx += 1
            if next_idx < len(masked_msgs):
                safe_start_idx = next_idx

        dropped_messages = masked_msgs[1:safe_start_idx]
        tail_msgs = masked_msgs[safe_start_idx:]

        if dropped_messages:
            card = self.generate_compaction_card(dropped_messages)
            return self._seal([first_msg, card] + tail_msgs)
        return self._seal([first_msg] + tail_msgs)

    def compact_with_stats(self, messages: List[BaseMessage]) -> CompactionResult:
        """Compact messages and return structured statistics including original/compacted character count."""
        orig_chars = sum(len(str(getattr(m, "content", ""))) for m in messages)
        compacted = self.compact(messages)
        comp_chars = sum(len(str(getattr(m, "content", ""))) for m in compacted)

        # Count messages that were actually dropped (excluding injected cards)
        retained_orig_count = len([
            m for m in compacted
            if not (isinstance(m, SystemMessage) and "上下文智能折叠摘要" in getattr(m, "content", ""))
        ])
        folded_count = max(0, len(messages) - retained_orig_count)

        return CompactionResult(
            messages=compacted,
            original_chars=orig_chars,
            compacted_chars=comp_chars,
            folded_count=folded_count,
        )
