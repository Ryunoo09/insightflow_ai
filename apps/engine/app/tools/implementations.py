"""The five deterministic analytics tools (Plan 06 / TASK-604).

Every tool builds a parameterized DuckDB statement from a validated
`AnalysisPlan`. Column identifiers are matched against the Parquet schema and
quoted; values are always bound as parameters, so no text from the model ever
becomes SQL.
"""

from __future__ import annotations

import threading
import time
from decimal import Decimal
from pathlib import Path
from typing import Any

import duckdb
import pyarrow as pa
import pyarrow.parquet as pq

from app.analytics.explorer_compiler import quote
from app.config import settings
from app.data_engine.errors import DatasetError
from app.schemas.plan import AnalysisPlan
from app.schemas.result import ResultColumn, ResultEnvelope

# group_by_metric keeps the largest groups only, so a high-cardinality column
# cannot return a million rows to the narrator.
MAX_GROUPS = 50
# Histogram bucket count for distribution_metric.
HISTOGRAM_BUCKETS = 20
# Hard ceiling on returned rows, independent of the caller.
MAX_RESULT_ROWS = 1000
# correlation_matrix is quadratic in the number of numeric columns.
MAX_CORRELATION_COLUMNS = 5


class ToolError(DatasetError):
    """A tool that cannot run against this dataset."""

    def __init__(self, code: str, message: str, *, http_status: int = 422) -> None:
        super().__init__(code, message, http_status=http_status, retryable=False)


class ColumnTypeError(ToolError):
    """The plan asked for an operation the column type does not support."""


def _logical_type(arrow_type: pa.DataType) -> str:
    if pa.types.is_boolean(arrow_type):
        return "boolean"
    if pa.types.is_integer(arrow_type) or pa.types.is_floating(arrow_type) or pa.types.is_decimal(arrow_type):
        return "number"
    if pa.types.is_date(arrow_type) or pa.types.is_timestamp(arrow_type) or pa.types.is_time(arrow_type):
        return "date"
    return "text"


def parquet_schema(path: Path) -> dict[str, str]:
    """name -> logical type, read from the Parquet file itself."""
    try:
        schema = pq.read_schema(path)
    except (OSError, pa.ArrowInvalid) as exc:
        raise ToolError("ANALYSIS_NOT_READY", "data parquet belum tersedia.", http_status=409) from exc
    return {name: _logical_type(schema.field(name).type) for name in schema.names}


def apply_limits(connection: duckdb.DuckDBPyConnection) -> None:
    """Bound memory and threads, then abort a slow scan with a timer."""
    limit = settings.explorer_memory_limit.strip()
    connection.execute(f"SET memory_limit='{limit}'")
    connection.execute(f"SET threads={max(1, int(settings.explorer_threads))}")


def interrupt_after(connection: duckdb.DuckDBPyConnection) -> threading.Timer:
    """Start the timeout timer that interrupts a runaway query."""
    timer = threading.Timer(settings.explorer_timeout_seconds, connection.interrupt)
    timer.start()
    return timer


def ensure_read_only(sql: str) -> None:
    if not sql.lstrip().lower().startswith("select"):
        raise ToolError("TOOL_READ_ONLY", "hanya kueri baca yang diizinkan.")


def _where(plan: AnalysisPlan, schema: dict[str, str]) -> tuple[str, list[object]]:
    """Build an equality filter clause. Column names come from the schema only."""
    if not plan.filters:
        return "", []
    clauses: list[str] = []
    params: list[object] = []
    for name, value in plan.filters.items():
        if name not in schema:
            raise ToolError("TOOL_UNKNOWN_COLUMN", f"kolom filter tidak dikenal: {name!r}.")
        clauses.append(f"{quote(name)} = ?")
        params.append(value)
    return " WHERE " + " AND ".join(clauses), params


def _aggregation_sql(plan: AnalysisPlan, schema: dict[str, str]) -> str:
    column = quote(plan.column)
    if plan.aggregation == "count":
        # count is valid on any column: count non-null values.
        return f"COUNT({column})"
    if schema.get(plan.column) != "number":
        raise ColumnTypeError(
            "TOOL_TYPE_MISMATCH",
            f"agregasi {plan.aggregation} memerlukan kolom numerik, {plan.column!r} bukan numerik.",
        )
    return f"{plan.aggregation.upper()}({column})"


def _elapsed_ms(started: float) -> int:
    return int((time.monotonic() - started) * 1000)


def aggregate_metric(connection: duckdb.DuckDBPyConnection, path: Path, plan: AnalysisPlan) -> ResultEnvelope:
    started = time.monotonic()
    schema = parquet_schema(path)
    where, params = _where(plan, schema)
    expression = _aggregation_sql(plan, schema)
    sql = f"SELECT {expression} AS value FROM read_parquet(?){where}"
    ensure_read_only(sql)
    row = connection.execute(sql, [str(path), *params]).fetchone()
    value = _json_value(row[0]) if row else None
    envelope = ResultEnvelope(
        tool=plan.tool,
        columns=[ResultColumn(name="value", type="number")],
        rows=[{"value": value}],
        row_count=1,
        summary_stats={"aggregation": plan.aggregation, "column": plan.column},
    )
    envelope.execution_time_ms = _elapsed_ms(started)
    return envelope


def group_by_metric(connection: duckdb.DuckDBPyConnection, path: Path, plan: AnalysisPlan) -> ResultEnvelope:
    started = time.monotonic()
    schema = parquet_schema(path)
    if plan.group_by is None:
        raise ToolError("TOOL_MISSING_GROUP", "group_by_metric memerlukan group_by.")
    where, params = _where(plan, schema)
    expression = _aggregation_sql(plan, schema)
    group = quote(plan.group_by)
    sql = (
        f"SELECT {group} AS grp, {expression} AS value FROM read_parquet(?){where} "
        f"GROUP BY 1 ORDER BY 2 DESC NULLS LAST LIMIT {MAX_GROUPS}"
    )
    ensure_read_only(sql)
    rows = connection.execute(sql, [str(path), *params]).fetchall()
    payload = [{"group": _json_value(group_value), "value": _json_value(value)} for group_value, value in rows]
    envelope = ResultEnvelope(
        tool=plan.tool,
        columns=[ResultColumn(name="group", type="text"), ResultColumn(name="value", type="number")],
        rows=payload,
        row_count=len(payload),
        summary_stats={"aggregation": plan.aggregation, "column": plan.column, "group_by": plan.group_by},
    )
    envelope.execution_time_ms = _elapsed_ms(started)
    return envelope


def time_series_metric(connection: duckdb.DuckDBPyConnection, path: Path, plan: AnalysisPlan) -> ResultEnvelope:
    started = time.monotonic()
    schema = parquet_schema(path)
    if plan.group_by is None or plan.bucket is None:
        raise ToolError("TOOL_MISSING_GROUP", "time_series_metric memerlukan group_by dan bucket.")
    if schema.get(plan.group_by) != "date":
        raise ColumnTypeError("TOOL_TYPE_MISMATCH", f"group_by {plan.group_by!r} bukan kolom tanggal.")
    where, params = _where(plan, schema)
    expression = _aggregation_sql(plan, schema)
    # The bucket is a closed enum validated by the plan schema, so interpolating
    # it is safe; the column itself is quoted from the schema.
    bucket = plan.bucket
    sql = (
        f"SELECT date_trunc('{bucket}', {quote(plan.group_by)}) AS period, {expression} AS value "
        f"FROM read_parquet(?){where} GROUP BY 1 ORDER BY 1 LIMIT {MAX_RESULT_ROWS}"
    )
    ensure_read_only(sql)
    rows = connection.execute(sql, [str(path), *params]).fetchall()
    payload = [{"period": _json_value(period), "value": _json_value(value)} for period, value in rows]
    envelope = ResultEnvelope(
        tool=plan.tool,
        columns=[ResultColumn(name="period", type="date"), ResultColumn(name="value", type="number")],
        rows=payload,
        row_count=len(payload),
        summary_stats={"aggregation": plan.aggregation, "column": plan.column, "bucket": bucket},
    )
    envelope.execution_time_ms = _elapsed_ms(started)
    return envelope


def distribution_metric(connection: duckdb.DuckDBPyConnection, path: Path, plan: AnalysisPlan) -> ResultEnvelope:
    started = time.monotonic()
    schema = parquet_schema(path)
    if schema.get(plan.column) != "number":
        raise ColumnTypeError("TOOL_TYPE_MISMATCH", f"distribusi memerlukan kolom numerik, {plan.column!r} bukan.")
    where, params = _where(plan, schema)
    column = quote(plan.column)
    sql = f"SELECT min({column}) AS lo, max({column}) AS hi, count({column}) AS n FROM read_parquet(?){where}"
    ensure_read_only(sql)
    lo, hi, count = connection.execute(sql, [str(path), *params]).fetchone()
    if not count or lo is None or hi is None:
        envelope = ResultEnvelope(
            tool=plan.tool,
            columns=[ResultColumn(name="bucket_start", type="number"), ResultColumn(name="count", type="number")],
            rows=[],
            row_count=0,
            summary_stats={"column": plan.column},
        )
        envelope.execution_time_ms = _elapsed_ms(started)
        return envelope

    low = float(lo)
    high = float(hi)
    if high <= low:
        # A constant column has no range to bucket: one row is the honest answer.
        payload = [{"bucket_start": low, "count": int(count)}]
    else:
        width = (high - low) / HISTOGRAM_BUCKETS
        histogram_sql = (
            f"SELECT floor(({column} - ?) / ?) AS bucket, count(*) AS n "
            f"FROM read_parquet(?){where} GROUP BY 1 ORDER BY 1 LIMIT {HISTOGRAM_BUCKETS}"
        )
        ensure_read_only(histogram_sql)
        buckets = connection.execute(histogram_sql, [low, width, str(path), *params]).fetchall()
        payload = [
            {"bucket_start": round(low + (int(bucket) * width), 6), "count": int(n)}
            for bucket, n in buckets
        ]
    envelope = ResultEnvelope(
        tool=plan.tool,
        columns=[ResultColumn(name="bucket_start", type="number"), ResultColumn(name="count", type="number")],
        rows=payload[:MAX_RESULT_ROWS],
        row_count=len(payload[:MAX_RESULT_ROWS]),
        summary_stats={"column": plan.column, "min": low, "max": high, "count": int(count)},
    )
    envelope.execution_time_ms = _elapsed_ms(started)
    return envelope


def correlation_matrix(connection: duckdb.DuckDBPyConnection, path: Path, plan: AnalysisPlan) -> ResultEnvelope:
    started = time.monotonic()
    schema = parquet_schema(path)
    if schema.get(plan.column) != "number":
        raise ColumnTypeError("TOOL_TYPE_MISMATCH", f"korelasi memerlukan kolom numerik, {plan.column!r} bukan.")
    if plan.group_by is not None and schema.get(plan.group_by) != "number":
        raise ColumnTypeError("TOOL_TYPE_MISMATCH", f"korelasi memerlukan kolom numerik, {plan.group_by!r} bukan.")

    numeric = [name for name, kind in schema.items() if kind == "number"][:MAX_CORRELATION_COLUMNS]
    if len(numeric) < 2:
        raise ToolError("TOOL_NOT_ENOUGH_COLUMNS", "korelasi memerlukan minimal dua kolom numerik.")
    where, params = _where(plan, schema)
    payload: list[dict[str, Any]] = []
    for left in numeric:
        for right in numeric:
            sql = f"SELECT corr({quote(left)}, {quote(right)}) FROM read_parquet(?){where}"
            ensure_read_only(sql)
            row = connection.execute(sql, [str(path), *params]).fetchone()
            payload.append({"x": left, "y": right, "value": _json_value(row[0]) if row else None})
    envelope = ResultEnvelope(
        tool=plan.tool,
        columns=[
            ResultColumn(name="x", type="text"),
            ResultColumn(name="y", type="text"),
            ResultColumn(name="value", type="number"),
        ],
        rows=payload,
        row_count=len(payload),
        summary_stats={"columns": numeric},
    )
    envelope.execution_time_ms = _elapsed_ms(started)
    return envelope


def _json_value(value: object) -> object:
    """Convert a DuckDB scalar into something JSON can carry."""
    if value is None or isinstance(value, (bool, int, str)):
        return value
    if isinstance(value, float):
        return value
    if isinstance(value, Decimal):
        return float(value)
    if hasattr(value, "isoformat"):
        return value.isoformat()
    return str(value)
