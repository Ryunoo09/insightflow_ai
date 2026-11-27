"""Analysis pipeline tests (Plan 06).

The pipeline is exercised with a fake LLM but a real Parquet file, so the event
sequence, the tool execution, and the grounding fallback are all real.
"""

import asyncio
import datetime as dt
import json
from pathlib import Path

import pyarrow as pa
import pyarrow.parquet as pq
import pytest

from app.config import settings
from app.llm.client import LLMResponse
from app.orchestration.pipeline import run_analysis, sse
from app.schemas.analysis import AnalyzeRequest, ContextColumn, ContextDataset
from app.schemas.profile import DatasetRef

WS = "11111111-1111-1111-1111-111111111111"
DS = "22222222-2222-2222-2222-222222222222"


class FakeClient:
    """Returns a fixed plan for JSON calls and fixed text for narration."""

    def __init__(self, plan: dict, narrative: str) -> None:
        self._plan = plan
        self._narrative = narrative
        self.json_calls = 0
        self.text_calls = 0

    def complete_json(self, **_kwargs) -> LLMResponse:
        self.json_calls += 1
        return LLMResponse(content=json.dumps(self._plan))

    def complete_text(self, **_kwargs) -> LLMResponse:
        self.text_calls += 1
        return LLMResponse(content=self._narrative)


@pytest.fixture
def dataset(tmp_path: Path, monkeypatch) -> Path:
    """Write a Parquet file where the resolver expects it."""
    root = tmp_path / "storage"
    path = root / "parquet" / WS / DS / "1" / "data.parquet"
    path.parent.mkdir(parents=True, exist_ok=True)
    table = pa.table(
        {
            "category": pa.array(["a", "b", "a"], type=pa.string()),
            "amount": pa.array([10.0, 20.0, 30.0], type=pa.float64()),
            "created": pa.array([dt.date(2024, 1, 1)] * 3, type=pa.date32()),
        }
    )
    pq.write_table(table, path)
    monkeypatch.setattr(settings, "storage_root", str(root))
    return path


def request(question: str = "total amount?") -> AnalyzeRequest:
    return AnalyzeRequest(
        dataset_ref=DatasetRef(workspace_id=WS, dataset_id=DS, version_no=1),
        question=question,
        dataset=ContextDataset(
            dataset_id=DS,
            version_no=1,
            row_count=3,
            column_count=3,
            columns=[
                ContextColumn(name="category", type="text"),
                ContextColumn(name="amount", type="number"),
                ContextColumn(name="created", type="date"),
            ],
        ),
    )


def collect(req: AnalyzeRequest, client: FakeClient) -> list[tuple[str, dict]]:
    async def run() -> list[tuple[str, dict]]:
        frames: list[tuple[str, dict]] = []
        async for frame in run_analysis(req, cache=None, client=client):
            assert frame.startswith("event: ")
            event, _, rest = frame.partition("\n")
            data = rest.removeprefix("data: ").strip()
            frames.append((event.removeprefix("event: "), json.loads(data)))
        return frames

    return asyncio.run(run())


def test_pipeline_emits_the_documented_event_sequence(dataset: Path):
    client = FakeClient(
        {"tool": "aggregate_metric", "column": "amount", "aggregation": "sum", "intent": "total"},
        "Total amount adalah 60.",
    )
    events = [name for name, _ in collect(request(), client)]
    assert events[0] == "stage_start"
    assert "plan_generated" in events
    assert "tool_executed" in events
    assert "narrative_chunk" in events
    assert "chart_ready" in events
    assert events[-1] == "run_completed"
    # Stages arrive in order.
    assert events.index("plan_generated") < events.index("tool_executed") < events.index("chart_ready")


def test_pipeline_reports_the_computed_value(dataset: Path):
    client = FakeClient(
        {"tool": "aggregate_metric", "column": "amount", "aggregation": "sum", "intent": "total"},
        "Total 60.",
    )
    frames = dict(collect(request(), client))
    assert frames["run_completed"]["result"]["rows"] == [{"value": 60.0}]
    assert frames["run_completed"]["narrative_grounded"] is True


def test_pipeline_falls_back_when_narrative_hallucinates(dataset: Path):
    client = FakeClient(
        {"tool": "aggregate_metric", "column": "amount", "aggregation": "sum", "intent": "total"},
        "Total 999.",
    )
    completed = dict(collect(request(), client))["run_completed"]
    assert completed["narrative_grounded"] is True
    assert "999" not in completed["narrative"]
    assert completed["narrative_fallback"]


def test_pipeline_rejects_invalid_plan(dataset: Path):
    client = FakeClient(
        {"tool": "aggregate_metric", "column": "profit", "aggregation": "sum", "intent": "x"},
        "irrelevant",
    )
    frames = dict(collect(request(), client))
    assert "run_failed" in frames
    assert frames["run_failed"]["code"] == "PLAN_UNKNOWN_COLUMN"
    assert "run_completed" not in frames


def test_pipeline_fails_cleanly_when_parquet_is_missing(tmp_path: Path, monkeypatch):
    monkeypatch.setattr(settings, "storage_root", str(tmp_path / "empty"))
    client = FakeClient({}, "x")
    frames = dict(collect(request(), client))
    assert frames["run_failed"]["code"] == "ANALYSIS_NOT_READY"


def test_sse_frame_is_single_line_json():
    frame = sse("stage_start", {"stage": "planning", "message": "baris\nbaru"})
    assert frame.endswith("\n\n")
    lines = [line for line in frame.split("\n") if line]
    assert len(lines) == 2
    assert lines[0] == "event: stage_start"
