"""Pydantic contracts for the analysis plan (Plan 06 / TASK-603).

``AnalysisPlan`` is what the LLM may produce: a single deterministic tool call,
never free-form SQL or code. The shape is intentionally narrow, because the
provider probe (§9.1) showed that anything wider invites invented columns.
"""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator

ToolName = Literal[
    "aggregate_metric",
    "group_by_metric",
    "time_series_metric",
    "distribution_metric",
    "correlation_matrix",
]
Aggregation = Literal["sum", "avg", "min", "max", "count"]
Bucket = Literal["day", "week", "month", "quarter", "year"]

TOOL_NAMES: tuple[str, ...] = (
    "aggregate_metric",
    "group_by_metric",
    "time_series_metric",
    "distribution_metric",
    "correlation_matrix",
)
AGGREGATIONS: tuple[str, ...] = ("sum", "avg", "min", "max", "count")
BUCKETS: tuple[str, ...] = ("day", "week", "month", "quarter", "year")

# Identifiers are matched against the Parquet schema; anything that could carry
# SQL syntax is rejected before it reaches the query compiler.
IDENTIFIER_PATTERN = r"^[A-Za-z0-9_.\-]+$"
_PLAIN_IDENTIFIER_CHARS = frozenset("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.-")


class AnalysisPlan(BaseModel):
    """One deterministic tool call derived from a natural-language question."""

    model_config = ConfigDict(extra="forbid")

    tool: ToolName
    column: str = Field(pattern=IDENTIFIER_PATTERN)
    aggregation: Aggregation
    group_by: str | None = Field(default=None, pattern=IDENTIFIER_PATTERN)
    bucket: Bucket | None = None
    # Optional equality filters: column -> value. Keys are validated like columns.
    filters: dict[str, str] | None = None
    intent: str = ""

    @field_validator("filters")
    @classmethod
    def _validate_filter_keys(cls, value: dict[str, str] | None) -> dict[str, str] | None:
        if value is None:
            return None
        for key in value:
            if not key or any(ch not in _PLAIN_IDENTIFIER_CHARS for ch in key):
                raise ValueError(f"filter column is not a plain identifier: {key!r}")
        return value
