"""Analysis orchestration: plan -> execute -> narrate -> chart (Plan 06).

Each stage emits an SSE event so the client can show progress. The pipeline is
the only place that touches the LLM, DuckDB, and Redis, and it never lets a
provider failure hide a computed result.
"""

from __future__ import annotations

import json
import time
from collections.abc import AsyncIterator
from pathlib import Path
from typing import Any

from app.cache.redis import DEFAULT_RESULT_TTL_SECONDS, CacheClient, build_key
from app.config import settings
from app.data_engine.errors import DatasetError
from app.llm.client import LLMClient, LLMError, Usage
from app.orchestration.narrator import narrate
from app.orchestration.planner import plan_question
from app.orchestration.plan_validator import ColumnInfo, PlanError, PlanValidator
from app.orchestration.visualization_rules import choose_chart
from app.schemas.analysis import AnalyzeRequest
from app.schemas.result import ResultEnvelope
from app.storage.resolver import StorageResolver
from app.tools.registry import ToolRegistry, cache_key, parquet_schema

PREFIX_RESULT_CACHE = "rc"


def sse(event: str, data: dict[str, Any]) -> str:
    """Serialize one SSE frame. Data is always single-line JSON."""
    return f"event: {event}\ndata: {json.dumps(data, ensure_ascii=False, default=str)}\n\n"


async def run_analysis(
    request: AnalyzeRequest,
    *,
    cache: CacheClient | None = None,
    client: LLMClient | None = None,
) -> AsyncIterator[str]:
    """Yield SSE frames for one analysis run."""
    started = time.monotonic()
    ref = request.dataset_ref
    resolver = StorageResolver(settings.storage_root)
    parquet_path = resolver.parquet_path(ref.workspace_id, ref.dataset_id, ref.version_no)
    client = client or LLMClient()
    usage = Usage()

    yield sse("stage_start", {"stage": "planning", "message": "Menyusun rencana analisis..."})
    try:
        schema = parquet_schema(parquet_path)
    except DatasetError as exc:
        yield sse("run_failed", {"code": exc.code, "message": exc.message})
        return

    validator = PlanValidator([ColumnInfo(name=name, type=kind) for name, kind in schema.items()])
    dataset = request.dataset.model_dump()

    try:
        plan, attempts = plan_question(
            client=client,
            validator=validator,
            dataset=dataset,
            question=request.question,
            usage=usage,
        )
    except PlanError as exc:
        yield sse("run_failed", {"code": exc.code, "message": exc.message, "stage": "planning"})
        return
    except LLMError as exc:
        yield sse("run_failed", {"code": exc.code, "message": exc.message, "stage": "planning"})
        return

    yield sse(
        "plan_generated",
        {
            "plan": plan.model_dump(exclude_none=True),
            "repair_attempts": attempts,
        },
    )

    yield sse("stage_start", {"stage": "computing", "message": "Menghitung data..."})
    registry = ToolRegistry()
    try:
        envelope, cached = await _execute(registry, plan, parquet_path, cache)
    except DatasetError as exc:
        yield sse("run_failed", {"code": exc.code, "message": exc.message, "stage": "computing"})
        return
    yield sse(
        "tool_executed",
        {
            "tool": envelope.tool,
            "row_count": envelope.row_count,
            "execution_time_ms": envelope.execution_time_ms,
            "cache_hit": cached,
        },
    )

    yield sse("stage_start", {"stage": "narrating", "message": "Menyusun ringkasan..."})
    chart = choose_chart(envelope)
    narrative = narrate(
        client=client,
        question=request.question,
        envelope=envelope,
        chart=chart,
        usage=usage,
    )

    # Stream the narrative in small chunks so the UI paints progressively even
    # though the model returns it whole.
    for chunk in _chunks(narrative.text, 60):
        yield sse("narrative_chunk", {"text": chunk})

    yield sse("chart_ready", {"chart": chart.model_dump()})
    yield sse(
        "run_completed",
        {
            "result": envelope.model_dump(),
            "narrative": narrative.text,
            "narrative_grounded": narrative.grounded,
            "narrative_fallback": narrative.fallback_reason,
            "chart": chart.model_dump(),
            "usage": usage.as_dict(),
            "duration_ms": int((time.monotonic() - started) * 1000),
        },
    )


async def _execute(
    registry: ToolRegistry,
    plan: Any,
    parquet_path: Path,
    cache: CacheClient | None,
) -> tuple[ResultEnvelope, bool]:
    """Run a tool, using Redis when available. A cache failure never blocks."""
    key = None
    if cache is not None:
        key = build_key(settings.env, PREFIX_RESULT_CACHE, cache_key(plan, parquet_path))
        try:
            hit = await cache.get(key)
        except Exception:  # pragma: no cover - redis outage must not break analysis
            hit = None
        if hit:
            try:
                return ResultEnvelope.model_validate_json(hit), True
            except ValueError:
                # A stale or corrupted entry is ignored, not fatal.
                pass

    envelope = registry.execute(plan, parquet_path)

    if cache is not None and key is not None:
        try:
            await cache.set_with_ttl(
                key,
                envelope.model_dump_json(),
                settings.analysis_result_cache_ttl_seconds or DEFAULT_RESULT_TTL_SECONDS,
            )
        except Exception:  # pragma: no cover
            pass
    return envelope, False


def _chunks(text: str, size: int) -> list[str]:
    return [text[i : i + size] for i in range(0, len(text), size)] or [""]
