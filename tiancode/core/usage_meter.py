"""Token usage tracking, prompt cache efficiency, and tiered cost calculation."""

from dataclasses import asdict, dataclass
from typing import Any, Dict, Optional, Tuple


@dataclass(frozen=True)
class ModelPricing:
    """Tiered pricing per 1,000,000 tokens in CNY (¥)."""

    input_per_1m_cny: float
    cache_read_per_1m_cny: float
    output_per_1m_cny: float
    display_name: str


MODEL_PRICING_TABLE: Dict[str, ModelPricing] = {
    "deepseek-chat": ModelPricing(2.0, 0.5, 8.0, "DeepSeek-V3"),
    "deepseek-v3": ModelPricing(2.0, 0.5, 8.0, "DeepSeek-V3"),
    "deepseek-reasoner": ModelPricing(4.0, 1.0, 16.0, "DeepSeek-R1"),
    "deepseek-r1": ModelPricing(4.0, 1.0, 16.0, "DeepSeek-R1"),
    "gpt-4o": ModelPricing(18.0, 9.0, 72.0, "GPT-4o"),
    "gpt-4o-mini": ModelPricing(1.08, 0.54, 4.32, "GPT-4o-mini"),
    "claude-3-5-sonnet": ModelPricing(21.6, 2.16, 108.0, "Claude 3.5 Sonnet"),
    "claude-3-7-sonnet": ModelPricing(21.6, 2.16, 108.0, "Claude 3.7 Sonnet"),
}


def get_model_pricing(model: str) -> Optional[ModelPricing]:
    """Match model name against known pricing tiers; returns None for local/unknown models."""
    if not model or not isinstance(model, str):
        return None
    norm = model.strip().lower()
    if norm in MODEL_PRICING_TABLE:
        return MODEL_PRICING_TABLE[norm]
    # Prefix match for versioned model IDs (e.g., gpt-4o-2024-08-06)
    for key in sorted(MODEL_PRICING_TABLE.keys(), key=len, reverse=True):
        if norm.startswith(key):
            return MODEL_PRICING_TABLE[key]
    return None


@dataclass
class TokenUsageBreakdown:
    """Cumulative token breakdown across a session."""

    input_tokens: int = 0
    cache_read_tokens: int = 0
    output_tokens: int = 0
    total_tokens: int = 0
    turn_count: int = 0

    @property
    def total_input_tokens(self) -> int:
        return self.input_tokens + self.cache_read_tokens

    @property
    def cache_hit_rate(self) -> float:
        total_in = self.total_input_tokens
        return (self.cache_read_tokens / total_in * 100.0) if total_in > 0 else 0.0


@dataclass
class CostBreakdown:
    """Cumulative monetary cost breakdown in CNY (¥)."""

    input_cost: float = 0.0
    cache_cost: float = 0.0
    output_cost: float = 0.0
    total_cost: float = 0.0
    saved_by_cache: float = 0.0
    is_known_pricing: bool = True


class TokenUsageTracker:
    """Tracks per-turn token usage, prompt cache savings, and cost across a session."""

    def __init__(self) -> None:
        self._tokens = TokenUsageBreakdown()
        self._cost = CostBreakdown()
        self._last_model: str = ""

    def record_turn(self, model: str, usage_metadata: Any) -> None:
        """Record token usage from a single LLM invocation turn safely."""
        if not isinstance(usage_metadata, dict) or not usage_metadata:
            return

        raw_in = usage_metadata.get("input_tokens", 0)
        raw_out = usage_metadata.get("output_tokens", 0)
        raw_total = usage_metadata.get("total_tokens", 0)

        if not isinstance(raw_in, int) or not isinstance(raw_out, int):
            return

        # Extract cache_read from input_token_details if present
        details = usage_metadata.get("input_token_details")
        cache_read = 0
        if isinstance(details, dict):
            cr = details.get("cache_read", 0)
            if isinstance(cr, int) and cr > 0:
                cache_read = min(cr, raw_in)

        uncached_in = max(0, raw_in - cache_read)
        total = raw_total if isinstance(raw_total, int) and raw_total > 0 else (raw_in + raw_out)

        self._tokens.input_tokens += uncached_in
        self._tokens.cache_read_tokens += cache_read
        self._tokens.output_tokens += raw_out
        self._tokens.total_tokens += total
        self._tokens.turn_count += 1
        self._last_model = model or self._last_model

        pricing = get_model_pricing(model)
        if pricing is None:
            self._cost.is_known_pricing = False
            return

        self._cost.is_known_pricing = True
        in_cost = (uncached_in / 1_000_000.0) * pricing.input_per_1m_cny
        c_cost = (cache_read / 1_000_000.0) * pricing.cache_read_per_1m_cny
        out_cost = (raw_out / 1_000_000.0) * pricing.output_per_1m_cny
        saved = (cache_read / 1_000_000.0) * max(0.0, pricing.input_per_1m_cny - pricing.cache_read_per_1m_cny)

        self._cost.input_cost += in_cost
        self._cost.cache_cost += c_cost
        self._cost.output_cost += out_cost
        self._cost.total_cost += in_cost + c_cost + out_cost
        self._cost.saved_by_cache += saved

    def get_session_stats(self) -> Tuple[TokenUsageBreakdown, CostBreakdown]:
        """Return copies of current token and cost breakdowns."""
        return (
            TokenUsageBreakdown(
                input_tokens=self._tokens.input_tokens,
                cache_read_tokens=self._tokens.cache_read_tokens,
                output_tokens=self._tokens.output_tokens,
                total_tokens=self._tokens.total_tokens,
                turn_count=self._tokens.turn_count,
            ),
            CostBreakdown(
                input_cost=self._cost.input_cost,
                cache_cost=self._cost.cache_cost,
                output_cost=self._cost.output_cost,
                total_cost=self._cost.total_cost,
                saved_by_cache=self._cost.saved_by_cache,
                is_known_pricing=self._cost.is_known_pricing,
            ),
        )

    def reset(self) -> None:
        """Reset all tracked metrics for a fresh session."""
        self._tokens = TokenUsageBreakdown()
        self._cost = CostBreakdown()

    def to_dict(self) -> Dict[str, Any]:
        """Serialize tracker state for session persistence."""
        return {
            "tokens": asdict(self._tokens),
            "cost": asdict(self._cost),
            "last_model": self._last_model,
        }

    def load_from_dict(self, data: Optional[Dict[str, Any]]) -> None:
        """Rehydrate tracker state from persisted session dictionary."""
        self.reset()
        if not isinstance(data, dict):
            return

        t = data.get("tokens")
        if isinstance(t, dict):
            self._tokens.input_tokens = int(t.get("input_tokens", 0))
            self._tokens.cache_read_tokens = int(t.get("cache_read_tokens", 0))
            self._tokens.output_tokens = int(t.get("output_tokens", 0))
            self._tokens.total_tokens = int(t.get("total_tokens", 0))
            self._tokens.turn_count = int(t.get("turn_count", 0))

        c = data.get("cost")
        if isinstance(c, dict):
            self._cost.input_cost = float(c.get("input_cost", 0.0))
            self._cost.cache_cost = float(c.get("cache_cost", 0.0))
            self._cost.output_cost = float(c.get("output_cost", 0.0))
            self._cost.total_cost = float(c.get("total_cost", 0.0))
            self._cost.saved_by_cache = float(c.get("saved_by_cache", 0.0))
            self._cost.is_known_pricing = bool(c.get("is_known_pricing", True))

        self._last_model = str(data.get("last_model", ""))
