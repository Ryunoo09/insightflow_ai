"""Pydantic contracts for the internal dataset processing IPC (PRD 33.6)."""

from __future__ import annotations

from pydantic import BaseModel, ConfigDict, Field


class DatasetRef(BaseModel):
    workspace_id: str
    dataset_id: str
    version_no: int = Field(ge=1)


class Limits(BaseModel):
    max_rows: int = Field(gt=0)
    max_columns: int = Field(gt=0)


class ColumnOverride(BaseModel):
    model_config = ConfigDict(extra="ignore")

    name: str
    logical_type: str | None = None


class ProcessRequest(BaseModel):
    model_config = ConfigDict(extra="ignore")

    dataset_ref: DatasetRef
    source_format: str
    source_sha256: str | None = None
    overrides: list[ColumnOverride] = Field(default_factory=list)
    limits: Limits


class ColumnOut(BaseModel):
    ordinal: int
    name: str
    original_name: str
    logical_type: str
    physical_type: str
    role: str
    null_count: int
    distinct_count: int


class ParquetOut(BaseModel):
    key: str
    size_bytes: int
    sha256: str


class ProfileColumn(BaseModel):
    model_config = ConfigDict(extra="allow")

    name: str
    type: str
    null_count: int
    null_pct: float
    distinct_count: int
    stats: dict = Field(default_factory=dict)


class DatasetProfile(BaseModel):
    """Validated shape of the profiler output (stored as jsonb by the API)."""

    model_config = ConfigDict(extra="allow")

    profile_schema_version: int
    quality_score: float
    row_count: int
    column_count: int
    columns: list[ProfileColumn]
    dataset: dict = Field(default_factory=dict)
    warnings: list[dict] = Field(default_factory=list)


class ProcessResponse(BaseModel):
    row_count: int
    column_count: int
    columns: list[ColumnOut]
    parquet: ParquetOut
    profile: dict
    processing_report: dict
