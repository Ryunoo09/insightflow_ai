"""Parameterized DuckDB query compiler for the data explorer (Plan 05).

Identifiers are matched against the Parquet schema before being quoted, and
every value is bound as a statement parameter: user input never becomes SQL.
"""

from __future__ import annotations

from collections.abc import Sequence
from dataclasses import dataclass, field
from pathlib import Path

import pyarrow as pa
import pyarrow.parquet as pq

from app.data_engine.errors import DatasetError
from app.schemas.explorer import ExplorerColumn, RowsQueryRequest

MAX_LIMIT = 500

_OPERATORS = {
    "eq": "=",
    "neq": "<>",
    "gt": ">",
    "gte": ">=",
    "lt": "<",
    "lte": "<=",
}


class ExplorerQueryError(DatasetError):
    """A rejected explorer request. Never retryable: the client must change it."""

    def __init__(self, code: str, message: str, *, http_status: int = 422) -> None:
        super().__init__(code, message, http_status=http_status, retryable=False)


@dataclass
class CompiledQuery:
    where_sql: str
    where_params: list[object] = field(default_factory=list)
    order_sql: str = ""
    limit: int = 100
    offset: int = 0


def parquet_columns(path: Path) -> list[ExplorerColumn]:
    """The authoritative column list and display type for a Parquet artifact."""
    try:
        schema = pq.read_schema(path)
    except (OSError, pa.ArrowInvalid) as exc:
        raise ExplorerQueryError(
            "EXPLORER_NOT_READY", "data parquet belum tersedia.", http_status=409
        ) from exc
    return [ExplorerColumn(name=name, type=_logical_type(schema.field(name).type)) for name in schema.names]


def compile_query(columns: Sequence[str], request: RowsQueryRequest) -> CompiledQuery:
    """Validate sort/filter identifiers and build the WHERE/ORDER fragments."""
    known = set(columns)
    clauses: list[str] = []
    params: list[object] = []

    for filtered in request.filters:
        column = _column(known, filtered.column)
        if filtered.operator == "contains":
            clauses.append(f'CAST({quote(column)} AS VARCHAR) ILIKE ? ESCAPE \'\\\'')
            params.append(_like_pattern(filtered.value))
        else:
            clauses.append(f"{quote(column)} {_OPERATORS[filtered.operator]} ?")
            params.append(filtered.value)

    search = (request.search or "").strip()
    if search:
        pattern = _like_pattern(search)
        matches = [f'CAST({quote(name)} AS VARCHAR) ILIKE ? ESCAPE \'\\\'' for name in columns]
        if matches:
            clauses.append("(" + " OR ".join(matches) + ")")
            params.extend([pattern] * len(matches))

    order = ""
    if request.sort:
        parts = []
        for sort in request.sort:
            column = _column(known, sort.column)
            direction = sort.direction.lower()
            if direction not in ("asc", "desc"):
                raise ExplorerQueryError("EXPLORER_INVALID_SORT", "arah urutan tidak valid.")
            parts.append(f"{quote(column)} {direction.upper()}")
        order = " ORDER BY " + ", ".join(parts)

    where = (" WHERE " + " AND ".join(clauses)) if clauses else ""
    return CompiledQuery(
        where_sql=where,
        where_params=params,
        order_sql=order,
        limit=max(1, min(request.limit, MAX_LIMIT)),
        offset=max(0, request.offset),
    )


def quote(identifier: str) -> str:
    """Quote a validated identifier; embedded quotes can never escape."""
    return '"' + identifier.replace('"', '""') + '"'


def _column(known: set[str], name: str) -> str:
    if name not in known:
        raise ExplorerQueryError("EXPLORER_INVALID_COLUMN", f"kolom tidak dikenal: {name!r}.")
    return name


def _like_pattern(value: object) -> str:
    text = "" if value is None else str(value)
    escaped = text.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")
    return f"%{escaped}%"


def _logical_type(arrow_type: pa.DataType) -> str:
    if pa.types.is_boolean(arrow_type):
        return "boolean"
    if pa.types.is_integer(arrow_type) or pa.types.is_floating(arrow_type) or pa.types.is_decimal(arrow_type):
        return "number"
    if pa.types.is_date(arrow_type) or pa.types.is_timestamp(arrow_type) or pa.types.is_time(arrow_type):
        return "date"
    return "text"