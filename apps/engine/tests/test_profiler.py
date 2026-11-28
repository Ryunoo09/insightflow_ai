import datetime
import threading
import time

import pyarrow as pa
import pyarrow.parquet as pq
import pytest

from app.data_engine.errors import DatasetError
from app.data_engine.ingest import ColumnMeta
from app.profiling.profiler import profile_parquet


def _col(ordinal, name, logical, physical, role):
    return ColumnMeta(
        ordinal=ordinal,
        name=name,
        original_name=name,
        logical_type=logical,
        physical_type=physical,
        role=role,
    )


def _write(tmp_path, table):
    path = tmp_path / "data.parquet"
    pq.write_table(table, path)
    return path


def test_profile_numeric_and_text(tmp_path):
    table = pa.table(
        {
            "revenue": pa.array([100.0, None, 300.0, 1000.0]),
            "category": pa.array(["a", "a", "b", None]),
            "mostly_null": pa.array([None, None, None, 5]),
        }
    )
    path = _write(tmp_path, table)
    columns = [
        _col(1, "revenue", "decimal", "DOUBLE", "measure"),
        _col(2, "category", "text", "VARCHAR", "dimension"),
        _col(3, "mostly_null", "integer", "BIGINT", "measure"),
    ]

    profile = profile_parquet(path, columns)

    assert profile["profile_schema_version"] == 1
    assert profile["row_count"] == 4
    assert 0.0 <= profile["quality_score"] <= 100.0

    revenue = profile["columns"][0]
    assert revenue["null_count"] == 1
    assert revenue["distinct_count"] == 3
    assert revenue["stats"]["min"] == 100.0
    assert revenue["stats"]["max"] == 1000.0
    assert revenue["stats"]["median"] is not None
    assert revenue["outliers"]["method"] == "iqr"

    category = profile["columns"][1]
    assert category["distinct_count"] == 2
    assert category["top_values"][0]["value"] == "a"

    assert any(w["code"] == "HIGH_NULL_RATIO" for w in profile["warnings"])


def test_profile_dates(tmp_path):
    table = pa.table(
        {
            "order_date": pa.array(
                [datetime.date(2025, 1, 5), datetime.date(2025, 3, 9)],
                type=pa.date32(),
            )
        }
    )
    path = _write(tmp_path, table)
    profile = profile_parquet(path, [_col(1, "order_date", "date", "DATE", "time")])
    stats = profile["columns"][0]["stats"]
    assert stats["min"] == "2025-01-05"
    assert stats["max"] == "2025-03-09"


def test_duplicate_rows_lower_quality(tmp_path):
    table = pa.table({"a": pa.array([1, 1, 1, 2]), "b": pa.array(["x", "x", "x", "y"])})
    path = _write(tmp_path, table)
    profile = profile_parquet(path, [_col(1, "a", "integer", "BIGINT", "measure")])
    assert profile["duplicate_row_count"] == 2
    assert profile["quality_score"] < 100.0


def _no_extra_threads(baseline: set, timeout: float = 2.0) -> bool:
    """Wait until every thread that started after `baseline` has exited."""
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if set(threading.enumerate()) <= baseline:
            return True
        time.sleep(0.01)
    return set(threading.enumerate()) <= baseline


def test_normal_profile_succeeds_and_cancels_timer(tmp_path):
    baseline = set(threading.enumerate())
    table = pa.table({"a": pa.array([1, 2, 3, 4]), "b": pa.array(["x", "y", "x", None])})
    path = _write(tmp_path, table)
    columns = [
        _col(1, "a", "integer", "BIGINT", "measure"),
        _col(2, "b", "text", "VARCHAR", "dimension"),
    ]

    profile = profile_parquet(path, columns, timeout_seconds=30)

    assert profile["row_count"] == 4
    assert _no_extra_threads(baseline)


def test_timeout_aborts_heavy_profile(tmp_path):
    baseline = set(threading.enumerate())
    rows = 200_000
    table = pa.table(
        {
            "id": pa.array(range(rows)),
            "label": pa.array([f"c{i % 30000}" for i in range(rows)]),
            "amount": pa.array([i * 1.5 for i in range(rows)]),
        }
    )
    path = _write(tmp_path, table)
    columns = [
        _col(1, "id", "integer", "BIGINT", "measure"),
        _col(2, "label", "text", "VARCHAR", "dimension"),
        _col(3, "amount", "decimal", "DOUBLE", "measure"),
    ]

    with pytest.raises(DatasetError) as exc:
        profile_parquet(path, columns, timeout_seconds=0.02)

    assert exc.value.code == "DATASET_RESOURCE_EXHAUSTED"
    assert exc.value.retryable is False
    assert _no_extra_threads(baseline)
