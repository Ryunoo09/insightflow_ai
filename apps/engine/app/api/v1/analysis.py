"""Internal analysis endpoint (Go -> Engine IPC, Plan 06)."""

from __future__ import annotations

from fastapi import APIRouter, Depends
from fastapi.responses import StreamingResponse

from app.api.v1.datasets import require_internal_token
from app.cache.redis import CacheClient
from app.config import settings
from app.orchestration.pipeline import run_analysis
from app.schemas.analysis import AnalyzeRequest

router = APIRouter()

_cache: CacheClient | None = None


def get_cache() -> CacheClient | None:
    """Lazily build the result cache. Redis is optional: analysis still runs."""
    global _cache
    if _cache is None:
        try:
            _cache = CacheClient(settings.redis_url)
        except Exception:  # pragma: no cover - misconfigured redis must not break startup
            _cache = None
    return _cache


@router.post("/ai/analyze")
async def analyze(req: AnalyzeRequest, _: None = Depends(require_internal_token)) -> StreamingResponse:
    return StreamingResponse(
        run_analysis(req, cache=get_cache()),
        media_type="text/event-stream",
        headers={
            "Cache-Control": "no-cache",
            "Connection": "keep-alive",
            "X-Accel-Buffering": "no",
        },
    )
