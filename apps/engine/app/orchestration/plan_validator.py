"""Plan validation (Plan 06 / TASK-603).

The validator is the only thing standing between a hallucinating model and the
DuckDB query compiler, so it checks identity and type, not just shape:

* names must match the dataset schema exactly (no SQL fragments),
* `sum`/`avg` need a numeric column,
* `time_series_metric` needs a date `group_by` plus a `bucket`.
"""

from __future__ import annotations

from dataclasses import dataclass

from app.data_engine.errors import DatasetError
from app.schemas.plan import AnalysisPlan

# Aggregations that require a numeric column.
_NUMERIC_AGGREGATIONS = {"sum", "avg"}

# Tools that cannot run without a grouping column.
_GROUPED_TOOLS = {"group_by_metric", "time_series_metric"}


class PlanError(DatasetError):
    """A plan the validator refuses to execute."""

    def __init__(self, code: str, message: str, *, http_status: int = 422) -> None:
        super().__init__(code, message, http_status=http_status, retryable=False)


@dataclass
class ColumnInfo:
    """One column as the validator sees it: the Parquet name and logical type."""

    name: str
    type: str


class PlanValidator:
    """Rejects any plan whose identifiers or types do not match the dataset."""

    def __init__(self, columns: list[ColumnInfo]) -> None:
        self._types = {column.name: column.type for column in columns}

    def validate(self, plan: AnalysisPlan) -> AnalysisPlan:
        column = self._require_column(plan.column, "column")
        aggregation = plan.aggregation
        if aggregation in _NUMERIC_AGGREGATIONS and self._types[column] != "number":
            raise PlanError(
                "PLAN_TYPE_MISMATCH",
                f"agregasi {aggregation} memerlukan kolom numerik, {column!r} bertipe {self._types[column]}.",
            )

        if plan.group_by is not None:
            group = self._require_column(plan.group_by, "group_by")
            if plan.tool == "time_series_metric" and self._types[group] != "date":
                raise PlanError(
                    "PLAN_TYPE_MISMATCH",
                    f"time_series_metric memerlukan group_by bertipe date, {group!r} bertipe {self._types[group]}.",
                )
        elif plan.tool in _GROUPED_TOOLS:
            raise PlanError("PLAN_MISSING_GROUP", f"{plan.tool} memerlukan group_by.")

        if plan.tool == "time_series_metric" and plan.bucket is None:
            raise PlanError("PLAN_MISSING_BUCKET", "time_series_metric memerlukan bucket.")

        if plan.filters:
            for name in plan.filters:
                self._require_column(name, "filters")

        return plan

    def _require_column(self, name: str, field: str) -> str:
        if name not in self._types:
            raise PlanError("PLAN_UNKNOWN_COLUMN", f"{field} tidak ada pada dataset: {name!r}.")
        return name
