"""Automatic dataset profiling with DuckDB (PRD 33.6, TASK-405)."""

from __future__ import annotations

import datetime as dt
import threading
from decimal import Decimal
from pathlib import Path

import duckdb

from app.data_engine.errors import DatasetError
from app.data_engine.ingest import ColumnMeta

_NUMERIC = {"integer", "decimal"}
_TEMPORAL = {"date", "datetime"}
_HIGH_NULL_PCT = 50.0
_TIMEOUT_CODE = "DATASET_RESOURCE_EXHAUSTED"
_TIMEOUT_MESSAGE = "profiling melebihi batas waktu"


def profile_parquet(
    parquet_path: Path,
    columns: list[ColumnMeta],
    *,
    memory_limit: str = "1GB",
    threads: int = 2,
    timeout_seconds: float = 30,
) -> dict:
    """Compute descriptive statistics for a Parquet file, read-only.

    A watchdog timer calls ``interrupt()`` on the connection when the run
    exceeds ``timeout_seconds``; the query then aborts and is surfaced as a
    non-retryable ``DATASET_RESOURCE_EXHAUSTED`` error.
    """
    con = duckdb.connect(database=":memory:")
    timer = threading.Timer(timeout_seconds, con.interrupt)
    timer.daemon = True
    timer.start()
    try:
        con.execute(f"SET memory_limit='{memory_limit}'")
        con.execute(f"SET threads={int(threads)}")
        con.execute("SET enable_progress_bar=false")
        con.execute(f"CREATE VIEW data AS SELECT * FROM read_parquet({_literal(str(parquet_path))})")

        total = int(con.execute("SELECT count(*) FROM data").fetchone()[0])
        distinct_rows = int(
            con.execute("SELECT count(*) FROM (SELECT DISTINCT * FROM data)").fetchone()[0]
        )
        duplicates = max(0, total - distinct_rows)

        column_profiles = []
        total_nulls = 0
        warnings: list[dict] = []
        for column in columns:
            entry = _profile_column(con, column, total)
            total_nulls += entry["null_count"]
            if total > 0 and entry["null_pct"] >= _HIGH_NULL_PCT:
                warnings.append(
                    {
                        "code": "HIGH_NULL_RATIO",
                        "column": column.name,
                        "message": f"{entry['null_pct']:.0f}% nilai kosong.",
                    }
                )
            column_profiles.append(entry)

        total_cells = total * len(columns)
        completeness = 1.0 - (total_nulls / total_cells) if total_cells else 1.0
        score = completeness * 100.0
        if total:
            score -= min(10.0, 10.0 * duplicates / total)
        score = round(max(0.0, min(100.0, score)), 2)

        return {
            "profile_schema_version": 1,
            "quality_score": score,
            "row_count": total,
            "column_count": len(columns),
            "duplicate_row_count": duplicates,
            "null_cell_pct": round(total_nulls / total_cells * 100, 2) if total_cells else 0.0,
            "columns": column_profiles,
            "dataset": {
                "row_count": total,
                "column_count": len(columns),
                "duplicate_row_count": duplicates,
            },
            "warnings": warnings,
        }
    except duckdb.InterruptException as exc:
        raise DatasetError(_TIMEOUT_CODE, _TIMEOUT_MESSAGE, retryable=False) from exc
    except duckdb.Error as exc:
        # Some abort paths wrap the interrupt; keep the same stable code.
        if _is_interrupt(exc):
            raise DatasetError(_TIMEOUT_CODE, _TIMEOUT_MESSAGE, retryable=False) from exc
        raise
    finally:
        timer.cancel()
        con.close()


def _profile_column(con: duckdb.DuckDBPyConnection, column: ColumnMeta, total: int) -> dict:
    quoted = _quote(column.name)
    null_count, distinct_count = con.execute(
        f"SELECT count(*) FILTER (WHERE {quoted} IS NULL), count(DISTINCT {quoted}) FROM data"
    ).fetchone()

    entry: dict = {
        "name": column.name,
        "type": column.logical_type,
        "role": column.role,
        "null_count": int(null_count),
        "null_pct": round(null_count / total * 100, 2) if total else 0.0,
        "distinct_count": int(distinct_count),
    }

    if column.logical_type in _NUMERIC:
        minimum, maximum, mean, std, quantiles = con.execute(
            f"SELECT min({quoted}), max({quoted}), avg({quoted}), stddev_pop({quoted}), "
            f"quantile_cont({quoted}, [0.25, 0.5, 0.75]) FROM data"
        ).fetchone()
        q1, median, q3 = quantiles if quantiles else (None, None, None)
        entry["stats"] = {
            "min": _num(minimum),
            "max": _num(maximum),
            "mean": _num(mean),
            "median": _num(median),
            "std": _num(std),
            "q1": _num(q1),
            "q3": _num(q3),
        }
        if q1 is not None and q3 is not None:
            iqr = q3 - q1
            lower = q1 - 1.5 * iqr
            upper = q3 + 1.5 * iqr
            outliers = con.execute(
                f"SELECT count(*) FROM data WHERE {quoted} < {float(lower)} OR {quoted} > {float(upper)}"
            ).fetchone()[0]
            entry["outliers"] = {
                "method": "iqr",
                "count": int(outliers),
                "lower": _num(lower),
                "upper": _num(upper),
            }
    elif column.logical_type in _TEMPORAL:
        minimum, maximum = con.execute(f"SELECT min({quoted}), max({quoted}) FROM data").fetchone()
        entry["stats"] = {"min": _iso(minimum), "max": _iso(maximum)}
    else:
        min_length, max_length = con.execute(
            f"SELECT min(length({quoted})), max(length({quoted})) FROM data"
        ).fetchone()
        entry["stats"] = {"min_length": _num(min_length), "max_length": _num(max_length)}
        top = con.execute(
            f"SELECT {quoted}, count(*) AS c FROM data WHERE {quoted} IS NOT NULL "
            f"GROUP BY {quoted} ORDER BY c DESC LIMIT 5"
        ).fetchall()
        entry["top_values"] = [{"value": str(value), "count": int(count)} for value, count in top]

    return entry


def _is_interrupt(exc: BaseException) -> bool:
    if isinstance(exc, duckdb.InterruptException):
        return True
    return "interrupt" in str(exc).lower()


def _quote(identifier: str) -> str:
    return '"' + identifier.replace('"', '""') + '"'


def _literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def _num(value: object) -> float | None:
    if value is None:
        return None
    if isinstance(value, Decimal):
        return float(value)
    return float(value)  # type: ignore[arg-type]


def _iso(value: object) -> str | None:
    if isinstance(value, (dt.datetime, dt.date)):
        return value.isoformat()
    return None
