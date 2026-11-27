"""Tool registry and deterministic result cache (Plan 06 / TASK-604).

The registry is the allowlist: the model can only name a tool that exists here,
and every tool is a fixed SQL template with bound parameters.
"""

from __future__ import annotations

import hashlib
import json
from collections.abc import Callable
from pathlib import Path

import duckdb

from app.config import settings
from app.schemas.plan import AnalysisPlan
from app.schemas.result import ResultEnvelope
from app.tools import implementations
from app.tools.implementations import (
    ColumnTypeError,
    ToolError,
    apply_limits,
    ensure_read_only,
    interrupt_after,
    parquet_schema,
)

ToolFunc = Callable[[duckdb.DuckDBPyConnection, Path, AnalysisPlan], ResultEnvelope]


class ToolRegistry:
    """The five registered deterministic tools."""

    def __init__(self) -> None:
        self._tools: dict[str, ToolFunc] = {
            "aggregate_metric": implementations.aggregate_metric,
            "group_by_metric": implementations.group_by_metric,
            "time_series_metric": implementations.time_series_metric,
            "distribution_metric": implementations.distribution_metric,
            "correlation_matrix": implementations.correlation_matrix,
        }

    def names(self) -> tuple[str, ...]:
        return tuple(self._tools)

    def execute(self, plan: AnalysisPlan, parquet_path: Path) -> ResultEnvelope:
        tool = self._tools.get(plan.tool)
        if tool is None:
            raise ToolError("TOOL_NOT_REGISTERED", f"tool tidak terdaftar: {plan.tool!r}.")
        connection = duckdb.connect(database=":memory:")
        apply_limits(connection)
        timer = interrupt_after(connection)
        try:
            return tool(connection, parquet_path, plan)
        except duckdb.InterruptException as exc:
            raise ToolError("ANALYSIS_TIMEOUT", "komputasi melebihi batas waktu.", http_status=504) from exc
        finally:
            timer.cancel()
            connection.close()


def cache_key(plan: AnalysisPlan, parquet_path: Path) -> str:
    """Deterministic cache key: same plan + same file => same result."""
    payload = json.dumps(
        {"plan": plan.model_dump(exclude_none=True), "file": str(parquet_path)},
        sort_keys=True,
        separators=(",", ":"),
    )
    return hashlib.sha256(payload.encode("utf-8")).hexdigest()


def cache_ttl_seconds() -> int:
    return settings.analysis_result_cache_ttl_seconds


__all__ = [
    "ColumnTypeError",
    "ToolError",
    "ToolRegistry",
    "cache_key",
    "cache_ttl_seconds",
    "ensure_read_only",
    "parquet_schema",
]
