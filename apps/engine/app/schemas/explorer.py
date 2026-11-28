"""Pydantic contracts for the internal data-explorer query IPC (Plan 05)."""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, ConfigDict, Field

from app.schemas.profile import DatasetRef

Operator = Literal["eq", "neq", "gt", "gte", "lt", "lte", "contains"]


class ExplorerSort(BaseModel):
    model_config = ConfigDict(extra="ignore")

    column: str
    direction: Literal["asc", "desc"]


class ExplorerFilter(BaseModel):
    model_config = ConfigDict(extra="ignore")

    column: str
    operator: Operator
    value: str | float | bool | None = None


class RowsQueryRequest(BaseModel):
    model_config = ConfigDict(extra="ignore")

    dataset_ref: DatasetRef
    offset: int = Field(default=0, ge=0)
    limit: int = Field(default=100, ge=1, le=500)
    sort: list[ExplorerSort] = Field(default_factory=list)
    filters: list[ExplorerFilter] = Field(default_factory=list)
    search: str | None = None


class ExplorerColumn(BaseModel):
    name: str
    type: str


class RowsQueryResponse(BaseModel):
    columns: list[ExplorerColumn]
    rows: list[dict]
    total: int
