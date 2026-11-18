"""Reasoning-aware ChatOpenAI adapter and <think> stream splitter for DeepSeek-R1 / o-series."""

from typing import Any, Dict, List, Mapping, Optional, Tuple

from langchain_core.language_models import LanguageModelInput
from langchain_core.messages import AIMessage, HumanMessage
from langchain_core.outputs import ChatGenerationChunk, ChatResult
from langchain_openai import ChatOpenAI

OPEN_THINK_TAG = "<think>"
CLOSE_THINK_TAG = "</think>"


def _extract_reasoning_field(mapping: Optional[Mapping[str, Any]]) -> str:
    """Extract non-empty reasoning string from OpenAI-compatible delta or message dict."""
    if not isinstance(mapping, Mapping):
        return ""
    for key in ("reasoning_content", "reasoning"):
        val = mapping.get(key)
        if isinstance(val, str) and val:
            return val
    return ""


def _longest_tag_prefix_suffix(text: str, tag: str) -> int:
    """Return length of longest proper prefix of `tag` appearing as a suffix of `text`."""
    max_k = min(len(text), len(tag) - 1)
    for k in range(max_k, 0, -1):
        if text.endswith(tag[:k]):
            return k
    return 0


class ReasonerChatOpenAI(ChatOpenAI):
    """ChatOpenAI subclass preserving reasoning_content across streams and tool-call loops."""

    def _convert_chunk_to_generation_chunk(
        self,
        chunk: dict,
        default_chunk_class: type,
        base_generation_info: Optional[dict],
    ) -> Optional[ChatGenerationChunk]:
        gen_chunk = super()._convert_chunk_to_generation_chunk(
            chunk, default_chunk_class, base_generation_info
        )
        if gen_chunk is None:
            return None

        choices = chunk.get("choices") or chunk.get("chunk", {}).get("choices") or []
        if choices and isinstance(choices[0], Mapping):
            reasoning = _extract_reasoning_field(choices[0].get("delta"))
            if reasoning:
                gen_chunk.message.additional_kwargs["reasoning_content"] = reasoning
        return gen_chunk

    def _create_chat_result(
        self,
        response: Any,
        generation_info: Optional[dict] = None,
    ) -> ChatResult:
        result = super()._create_chat_result(response, generation_info)
        resp_dict = (
            response
            if isinstance(response, dict)
            else (response.model_dump(warnings=False) if hasattr(response, "model_dump") else {})
        )
        choices = resp_dict.get("choices") or []
        for idx, gen in enumerate(result.generations):
            if idx < len(choices) and isinstance(choices[idx], Mapping):
                reasoning = _extract_reasoning_field(choices[idx].get("message"))
                if reasoning:
                    gen.message.additional_kwargs["reasoning_content"] = reasoning
        return result

    def _get_request_payload(
        self,
        input_: LanguageModelInput,
        *,
        stop: Optional[List[str]] = None,
        **kwargs: Any,
    ) -> dict:
        payload = super()._get_request_payload(input_, stop=stop, **kwargs)
        out_msgs = payload.get("messages")
        if not isinstance(out_msgs, list):
            return payload

        src_msgs = self._convert_input(input_).to_messages()
        if len(src_msgs) != len(out_msgs):
            return payload

        last_human_idx = -1
        for idx, m in enumerate(src_msgs):
            if isinstance(m, HumanMessage):
                last_human_idx = idx

        model_lower = (self.model_name or "").lower()
        is_reasoner_model = "reasoner" in model_lower or "-r1" in model_lower

        for idx, src_m in enumerate(src_msgs):
            msg_dict: Dict[str, Any] = out_msgs[idx]
            if idx < last_human_idx:
                msg_dict.pop("reasoning_content", None)
                continue
            if idx > last_human_idx and isinstance(src_m, AIMessage):
                has_tools = bool(src_m.tool_calls or msg_dict.get("tool_calls"))
                if has_tools:
                    rc = (src_m.additional_kwargs or {}).get("reasoning_content")
                    if isinstance(rc, str):
                        msg_dict["reasoning_content"] = rc
                    elif is_reasoner_model:
                        msg_dict["reasoning_content"] = ""
                else:
                    msg_dict.pop("reasoning_content", None)
        return payload


class ThinkTagStreamSplitter:
    """Streaming state machine splitting inline <think>...</think> blocks from normal content."""

    def __init__(self) -> None:
        self._in_think: bool = False
        self._seen_non_ws: bool = False
        self._buf: str = ""

    def feed(self, text: str) -> List[Tuple[str, str]]:
        """Consume a content delta and return ordered ('thinking' | 'token', chunk) tuples."""
        if not text:
            return []
        self._buf += text
        emitted: List[Tuple[str, str]] = []

        while self._buf:
            target_tag = CLOSE_THINK_TAG if self._in_think else OPEN_THINK_TAG
            current_kind = "thinking" if self._in_think else "token"

            pos = self._buf.find(target_tag)
            if pos != -1:
                if pos > 0:
                    prefix_chunk = self._buf[:pos]
                    if self._in_think or self._seen_non_ws or prefix_chunk.strip():
                        if not self._in_think and prefix_chunk.strip():
                            self._seen_non_ws = True
                        emitted.append((current_kind, prefix_chunk))
                self._buf = self._buf[pos + len(target_tag):]
                self._in_think = not self._in_think
                continue

            if not self._in_think and not self._seen_non_ws and not self._buf.strip():
                # Hold leading whitespace briefly in case <think> follows immediately
                if len(self._buf) <= 8:
                    break

            keep = _longest_tag_prefix_suffix(self._buf, target_tag)
            if keep > 0:
                if len(self._buf) > keep:
                    body_chunk = self._buf[:-keep]
                    if self._in_think or self._seen_non_ws or body_chunk.strip():
                        if not self._in_think and body_chunk.strip():
                            self._seen_non_ws = True
                        emitted.append((current_kind, body_chunk))
                self._buf = self._buf[-keep:]
                break

            if not self._in_think and self._buf.strip():
                self._seen_non_ws = True
            emitted.append((current_kind, self._buf))
            self._buf = ""
            break

        return emitted

    def flush(self) -> List[Tuple[str, str]]:
        """Flush any trailing buffered characters when an AI turn ends."""
        if not self._buf:
            return []
        kind = "thinking" if self._in_think else "token"
        leftover = self._buf
        self._buf = ""
        self._in_think = False
        return [(kind, leftover)]
