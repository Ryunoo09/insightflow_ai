"""Internal dataset processing endpoint (Go -> Engine IPC)."""

from __future__ import annotations

import secrets
import time
from pathlib import Path

from fastapi import APIRouter, Depends, Header

from app.config import settings
from app.data_engine.errors import DatasetError
from app.data_engine.ingest import ingest
from app.profiling.profiler import profile_parquet
from app.schemas.profile import (
    ColumnOut,
    DatasetProfile,
    ParquetOut,
    ProcessRequest,
    ProcessResponse,
)
from app.storage.resolver import StorageResolver

router = APIRouter()


def require_internal_token(x_internal_token: str = Header(default="")) -> None:
    """Reject any caller that does not present the shared service token."""
    expected = settings.internal_token
    if not expected or not secrets.compare_digest(x_internal_token, expected):
        raise DatasetError(
            "INTERNAL_AUTH_REQUIRED",
            "token internal tidak valid.",
            http_status=401,
        )


@router.post("/datasets/process", response_model=ProcessResponse)
def process(req: ProcessRequest, _: None = Depends(require_internal_token)) -> ProcessResponse:
    started = time.monotonic()
    resolver = StorageResolver(settings.storage_root)
    ref = req.dataset_ref

    source_path = resolver.source_path(ref.workspace_id, ref.dataset_id, ref.version_no, req.source_format)
    parquet_path = resolver.parquet_path(ref.workspace_id, ref.dataset_id, ref.version_no)

    result = ingest(
        source_path=source_path,
        parquet_path=parquet_path,
        source_format=req.source_format,
        source_sha256=req.source_sha256,
        max_rows=req.limits.max_rows,
        max_columns=req.limits.max_columns,
    )

    profile = DatasetProfile.model_validate(
        profile_parquet(
            parquet_path,
            result.columns,
            memory_limit=settings.profile_memory_limit,
            threads=settings.profile_threads,
            timeout_seconds=settings.profile_timeout_seconds,
        )
    )

    nulls = {column.name: column.null_count for column in profile.columns}
    distinct = {column.name: column.distinct_count for column in profile.columns}
    columns = [
        ColumnOut(
            ordinal=column.ordinal,
            name=column.name,
            original_name=column.original_name,
            logical_type=column.logical_type,
            physical_type=column.physical_type,
            role=column.role,
            null_count=nulls.get(column.name, 0),
            distinct_count=distinct.get(column.name, 0),
        )
        for column in result.columns
    ]

    report = {
        "report_version": 1,
        "source": {
            "format": req.source_format,
            "encoding": result.source_encoding,
            "delimiter": result.source_delimiter,
            "rows_read": result.row_count,
            "header_detected": True,
        },
        "transformations": result.transformations,
        "warnings": result.warnings,
        "duration_ms": int((time.monotonic() - started) * 1000),
    }

    return ProcessResponse(
        row_count=result.row_count,
        column_count=len(result.columns),
        columns=columns,
        parquet=ParquetOut(
            key=_relative_key(resolver, parquet_path),
            size_bytes=result.parquet_size,
            sha256=result.parquet_sha256,
        ),
        profile=profile.model_dump(),
        processing_report=report,
    )


def _relative_key(resolver: StorageResolver, path: Path) -> str:
    try:
        return path.relative_to(resolver.root).as_posix()
    except ValueError:
        return path.name
