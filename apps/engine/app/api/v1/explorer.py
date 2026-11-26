"""Internal data-explorer query endpoint (Go -> Engine IPC, Plan 05)."""

from __future__ import annotations

import datetime as dt
import threading
from decimal import Decimal
from pathlib import Path

import duckdb
from fastapi import APIRouter, Depends

from app.api.v1.datasets import require_internal_token
from app.analytics.explorer_compiler import (
    CompiledQuery,
    ExplorerQueryError,
    compile_query,
    parquet_columns,
)
from app.config import settings
from app.schemas.explorer import RowsQueryRequest, RowsQueryResponse
from app.storage.resolver import StorageResolver

router = APIRouter()


@router.post("/datasets/rows/query", response_model=RowsQueryResponse)
def query_rows(req: RowsQueryRequest, _: None = Depends(require_internal_token)) -> RowsQueryResponse:
    resolver = StorageResolver(settings.storage_root)
    ref = req.dataset_ref
    path = resolver.parquet_path(ref.workspace_id, ref.dataset_id, ref.version_no)
    if not path.is_file():
        raise ExplorerQueryError("EXPLORER_NOT_READY", "data parquet belum tersedia.", http_status=409)

    columns = parquet_columns(path)
    plan = compile_query([column.name for column in columns], req)

    connection = duckdb.connect(database=":memory:")
    # Abort a runaway scan without killing the worker thread.
    timer = threading.Timer(settings.explorer_timeout_seconds, connection.interrupt)
    timer.start()
    try:
        rows, names = _fetch_page(connection, plan, path)
        total = _count(connection, plan, path)
    except duckdb.InterruptException as exc:
        raise ExplorerQueryError(
            "EXPLORER_TIMEOUT", "query melebihi batas waktu.", http_status=504
        ) from exc
    finally:
        timer.cancel()
        connection.close()

    payload = [
        {name: _json_value(value) for name, value in zip(names, row)}
        for row in rows
    ]
    return RowsQueryResponse(columns=columns, rows=payload, total=int(total))


def _fetch_page(connection: duckdb.DuckDBPyConnection, plan: CompiledQuery, path: Path) -> tuple[list[tuple], list[str]]:
    sql = f"SELECT * FROM read_parquet(?){plan.where_sql}{plan.order_sql} LIMIT ? OFFSET ?"
    cursor = connection.execute(sql, [str(path), *plan.where_params, plan.limit, plan.offset])
    names = [description[0] for description in cursor.description]
    return cursor.fetchall(), names


def _count(connection: duckdb.DuckDBPyConnection, plan: CompiledQuery, path: Path) -> int:
    sql = f"SELECT COUNT(*) FROM read_parquet(?){plan.where_sql}"
    row = connection.execute(sql, [str(path), *plan.where_params]).fetchone()
    return int(row[0]) if row else 0


def _json_value(value: object) -> object:
    if value is None or isinstance(value, (bool, int, float, str)):
        return value
    if isinstance(value, Decimal):
        return float(value)
    if isinstance(value, (dt.datetime, dt.date, dt.time)):
        return value.isoformat()
    return str(value)