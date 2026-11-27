"""Tool registry tests (Plan 06 / TASK-604).

Every tool is checked against a real Parquet file, so the assertions are about
computed numbers, not mocks.
"""

import datetime as dt
from pathlib import Path

import pyarrow as pa
import pyarrow.parquet as pq
import pytest

from app.schemas.plan import AnalysisPlan
from app.schemas.result import ResultEnvelope
from app.tools.registry import (
    ToolError,
    ToolRegistry,
    cache_key,
    parquet_schema,
)


@pytest.fixture
def parquet(tmp_path: Path) -> Path:
    """A small deterministic dataset: 4 categories, 2 numeric columns, 1 date."""
    path = tmp_path / "data.parquet"
    table = pa.table(
        {
            "id": pa.array([1, 2, 3, 4, 5, 6], type=pa.int64()),
            "category": pa.array(["a", "b", "a", "c", "b", "a"], type=pa.string()),
            "amount": pa.array([10.0, 20.0, 30.0, 40.0, 50.0, 60.0], type=pa.float64()),
            "qty": pa.array([1, 2, 3, 4, 5, 6], type=pa.int64()),
            "created": pa.array(
                [
                    dt.date(2024, 1, 5),
                    dt.date(2024, 1, 20),
                    dt.date(2024, 2, 2),
                    dt.date(2024, 2, 15),
                    dt.date(2024, 3, 1),
                    dt.date(2024, 3, 9),
                ],
                type=pa.date32(),
            ),
        }
    )
    pq.write_table(table, path)
    return path


def registry() -> ToolRegistry:
    return ToolRegistry()


def test_registry_exposes_all_five_tools():
    assert set(registry().names()) == {
        "aggregate_metric",
        "group_by_metric",
        "time_series_metric",
        "distribution_metric",
        "correlation_matrix",
    }


def test_parquet_schema_maps_logical_types(parquet: Path):
    schema = parquet_schema(parquet)
    assert schema["amount"] == "number"
    assert schema["category"] == "text"
    assert schema["created"] == "date"


def test_aggregate_sum_is_exact(parquet: Path):
    envelope = registry().execute(
        AnalysisPlan(tool="aggregate_metric", column="amount", aggregation="sum"), parquet
    )
    assert envelope.rows == [{"value": 210.0}]
    assert envelope.row_count == 1
    assert envelope.execution_time_ms >= 0


def test_aggregate_avg_min_max_count(parquet: Path):
    reg = registry()
    avg = reg.execute(AnalysisPlan(tool="aggregate_metric", column="amount", aggregation="avg"), parquet)
    assert avg.rows[0]["value"] == pytest.approx(35.0)
    minimum = reg.execute(AnalysisPlan(tool="aggregate_metric", column="amount", aggregation="min"), parquet)
    assert minimum.rows[0]["value"] == 10.0
    maximum = reg.execute(AnalysisPlan(tool="aggregate_metric", column="amount", aggregation="max"), parquet)
    assert maximum.rows[0]["value"] == 60.0
    count = reg.execute(AnalysisPlan(tool="aggregate_metric", column="category", aggregation="count"), parquet)
    assert count.rows[0]["value"] == 6


def test_aggregate_honours_equality_filter(parquet: Path):
    envelope = registry().execute(
        AnalysisPlan(
            tool="aggregate_metric",
            column="amount",
            aggregation="sum",
            filters={"category": "a"},
        ),
        parquet,
    )
    assert envelope.rows[0]["value"] == 100.0


def test_group_by_sums_per_category_ordered_desc(parquet: Path):
    envelope = registry().execute(
        AnalysisPlan(tool="group_by_metric", column="amount", aggregation="sum", group_by="category"), parquet
    )
    assert envelope.rows == [
        {"group": "a", "value": 100.0},
        {"group": "b", "value": 70.0},
        {"group": "c", "value": 40.0},
    ]
    assert envelope.row_count == 3


def test_time_series_buckets_by_month(parquet: Path):
    envelope = registry().execute(
        AnalysisPlan(
            tool="time_series_metric",
            column="amount",
            aggregation="sum",
            group_by="created",
            bucket="month",
        ),
        parquet,
    )
    assert [row["value"] for row in envelope.rows] == [30.0, 70.0, 110.0]
    assert all(row["period"].startswith("2024-0") for row in envelope.rows)


def test_distribution_returns_buckets_summing_to_row_count(parquet: Path):
    envelope = registry().execute(
        AnalysisPlan(tool="distribution_metric", column="amount", aggregation="count"), parquet
    )
    assert envelope.row_count >= 1
    assert sum(row["count"] for row in envelope.rows) == 6
    assert envelope.summary_stats["min"] == 10.0
    assert envelope.summary_stats["max"] == 60.0


def test_correlation_matrix_is_symmetric_with_unit_diagonal(parquet: Path):
    envelope = registry().execute(
        AnalysisPlan(tool="correlation_matrix", column="amount", aggregation="count"), parquet
    )
    values = {(row["x"], row["y"]): row["value"] for row in envelope.rows}
    assert values[("amount", "amount")] == pytest.approx(1.0)
    assert values[("amount", "qty")] == pytest.approx(values[("qty", "amount")])


def test_sum_on_text_column_is_rejected(parquet: Path):
    with pytest.raises(ToolError) as err:
        registry().execute(AnalysisPlan(tool="aggregate_metric", column="category", aggregation="sum"), parquet)
    assert err.value.code == "TOOL_TYPE_MISMATCH"


def test_unknown_filter_column_is_rejected(parquet: Path):
    with pytest.raises(ToolError) as err:
        registry().execute(
            AnalysisPlan(
                tool="aggregate_metric",
                column="amount",
                aggregation="sum",
                filters={"secret": "x"},
            ),
            parquet,
        )
    assert err.value.code == "TOOL_UNKNOWN_COLUMN"


def test_missing_parquet_is_a_controlled_error(tmp_path: Path):
    with pytest.raises(ToolError) as err:
        registry().execute(
            AnalysisPlan(tool="aggregate_metric", column="amount", aggregation="sum"),
            tmp_path / "missing.parquet",
        )
    assert err.value.code == "ANALYSIS_NOT_READY"


def test_cache_key_is_stable_and_plan_specific(parquet: Path):
    a = AnalysisPlan(tool="aggregate_metric", column="amount", aggregation="sum")
    b = AnalysisPlan(tool="aggregate_metric", column="amount", aggregation="avg")
    assert cache_key(a, parquet) == cache_key(a, parquet)
    assert cache_key(a, parquet) != cache_key(b, parquet)


def test_envelope_rejects_non_finite_numbers():
    with pytest.raises(ValueError):
        ResultEnvelope(
            tool="aggregate_metric",
            columns=[{"name": "value", "type": "number"}],
            rows=[{"value": float("nan")}],
        )
