"""Contracts for the internal analysis endpoint (Plan 06)."""

from __future__ import annotations

from typing import Any

from pydantic import BaseModel, ConfigDict, Field

from app.schemas.profile import DatasetRef


class ContextColumn(BaseModel):
    model_config = ConfigDict(extra="ignore")

    name: str
    original_name: str = ""
    type: str = "text"
    nullable: bool = True
    null_pct: float | None = None
    distinct_count: int | None = None
    stats: dict[str, Any] | None = None
    top_values: list[dict[str, Any]] | None = None


class ContextDataset(BaseModel):
    model_config = ConfigDict(extra="ignore")

    dataset_id: str = ""
    version_no: int = 1
    row_count: int = 0
    column_count: int = 0
    quality_score: float | None = None
    columns: list[ContextColumn] = Field(default_factory=list)


class ContextMessage(BaseModel):
    model_config = ConfigDict(extra="ignore")

    role: str
    content: str


class AnalyzeRequest(BaseModel):
    model_config = ConfigDict(extra="ignore")

    dataset_ref: DatasetRef
    question: str = Field(min_length=1, max_length=2000)
    dataset: ContextDataset
    messages: list[ContextMessage] = Field(default_factory=list)
