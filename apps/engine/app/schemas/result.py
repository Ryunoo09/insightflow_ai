"""ResultEnvelope: the only shape the narrator is allowed to describe (Plan 06).

Every value in an envelope was computed by DuckDB, so the grounding checker can
treat these numbers as the complete truth for a run.
"""

from __future__ import annotations

import math
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator


class ResultColumn(BaseModel):
    name: str
    type: Literal["text", "number", "date", "boolean"] = "text"


class ResultEnvelope(BaseModel):
    """Deterministic tool output. Every value here is computed by DuckDB."""

    model_config = ConfigDict(extra="forbid")

    tool: str
    columns: list[ResultColumn]
    rows: list[dict[str, Any]] = Field(default_factory=list)
    row_count: int = 0
    execution_time_ms: int = 0
    summary_stats: dict[str, Any] = Field(default_factory=dict)
    cache_hit: bool = False

    @field_validator("rows")
    @classmethod
    def _reject_non_finite(cls, rows: list[dict[str, Any]]) -> list[dict[str, Any]]:
        """NaN and Infinity are not valid JSON: a client would see null or crash."""
        for row in rows:
            for key, value in row.items():
                if isinstance(value, float) and not math.isfinite(value):
                    raise ValueError(f"non-finite value in result column {key!r}")
        return rows

    def numeric_values(self) -> list[float]:
        """Every number the narrator is allowed to mention."""
        found: list[float] = []
        for row in self.rows:
            for value in row.values():
                if isinstance(value, bool):
                    continue
                if isinstance(value, (int, float)) and math.isfinite(float(value)):
                    found.append(float(value))
        for value in self.summary_stats.values():
            if isinstance(value, bool):
                continue
            if isinstance(value, (int, float)) and math.isfinite(float(value)):
                found.append(float(value))
        return found
