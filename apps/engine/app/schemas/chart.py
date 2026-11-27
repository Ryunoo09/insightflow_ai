"""ChartSpec contracts (Plan 06 / TASK-605)."""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, ConfigDict, Field


class ChartSpec(BaseModel):
    """Deterministic visualization choice for a ResultEnvelope."""

    model_config = ConfigDict(extra="forbid")

    kind: Literal["bar", "line", "scatter", "pie", "table"]
    x: str | None = None
    y: str | None = None
    series: list[str] = Field(default_factory=list)
    title: str = ""
    reason: str = ""
