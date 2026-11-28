"""Grounding, narrator, and chart tests (Plan 06 / TASK-605)."""

import pytest

from app.llm.client import LLMError, LLMResponse
from app.orchestration.grounding_checker import check
from app.orchestration.narrator import narrate, template_narrative
from app.orchestration.visualization_rules import choose_chart
from app.schemas.chart import ChartSpec
from app.schemas.result import ResultColumn, ResultEnvelope


def envelope(**overrides) -> ResultEnvelope:
    payload = {
        "tool": "aggregate_metric",
        "columns": [ResultColumn(name="value", type="number")],
        "rows": [{"value": 1234.56}],
        "row_count": 1,
        "summary_stats": {"column": "amount", "aggregation": "sum"},
    }
    payload.update(overrides)
    return ResultEnvelope(**payload)


def group_envelope() -> ResultEnvelope:
    return ResultEnvelope(
        tool="group_by_metric",
        columns=[ResultColumn(name="group", type="text"), ResultColumn(name="value", type="number")],
        rows=[
            {"group": "a", "value": 100.0},
            {"group": "b", "value": 70.0},
        ],
        row_count=2,
        summary_stats={"column": "amount"},
    )


# ---- grounding -----------------------------------------------------------


def test_grounding_accepts_numbers_from_the_result():
    report = check("Total penjualan adalah 1.234,56.", envelope())
    assert report.grounded
    assert report.checked == 1


def test_grounding_rejects_invented_number():
    report = check("Total penjualan adalah 9.999.", envelope())
    assert not report.grounded
    assert "9.999" in report.ungrounded


def test_grounding_tolerates_rounding():
    report = check("Sekitar 1.235 unit.", envelope())
    assert report.grounded


def test_grounding_understands_scale_words():
    report = check("Totalnya 1,23 ribu.", envelope())
    assert report.grounded


def test_grounding_checks_every_group_value():
    report = check("Grup a sebesar 100 dan grup b sebesar 70.", group_envelope())
    assert report.grounded
    report = check("Grup a sebesar 100 dan grup b sebesar 75.", group_envelope())
    assert not report.grounded


def test_grounding_flags_percentage_not_in_result():
    report = check("Peningkatannya 42%.", envelope())
    assert not report.grounded


# ---- narrator ------------------------------------------------------------


class FakeClient:
    def __init__(self, texts: list[str] | None = None, error: Exception | None = None) -> None:
        self._texts = list(texts or [])
        self._error = error
        self.calls = 0

    def complete_text(self, **_kwargs) -> LLMResponse:
        self.calls += 1
        if self._error:
            raise self._error
        return LLMResponse(content=self._texts.pop(0))


def test_narrator_accepts_grounded_text():
    client = FakeClient(["Total penjualan adalah 1.234,56."])
    result = narrate(client=client, question="total?", envelope=envelope(), chart=ChartSpec(kind="bar"))
    assert result.grounded
    assert result.attempts == 1
    assert "1.234,56" in result.text


def test_narrator_regenerates_once_then_falls_back():
    client = FakeClient(["Total 9.999.", "Masih 9.999."])
    result = narrate(client=client, question="total?", envelope=envelope(), chart=ChartSpec(kind="bar"))
    assert result.attempts == 2
    assert client.calls == 2
    # The fallback is the deterministic template: it can only use real numbers.
    assert "9.999" not in result.text
    assert result.fallback_reason.startswith("angka tidak terverifikasi")


def test_narrator_falls_back_when_provider_fails():
    client = FakeClient(error=LLMError("LLM_TIMEOUT", "timeout"))
    result = narrate(client=client, question="total?", envelope=envelope(), chart=ChartSpec(kind="bar"))
    assert result.grounded
    assert result.fallback_reason == "LLM_TIMEOUT"
    assert "1.234,56" in result.text


def test_template_narrative_for_empty_result():
    text = template_narrative("q", envelope(rows=[], row_count=0))
    assert "Tidak ada data" in text


def test_template_narrative_for_group_result():
    text = template_narrative("q", group_envelope())
    assert "2 kelompok" in text
    assert "a" in text


# ---- chart rules ---------------------------------------------------------


def test_chart_picks_line_for_time_series():
    spec = choose_chart(
        ResultEnvelope(
            tool="time_series_metric",
            columns=[ResultColumn(name="period", type="date"), ResultColumn(name="value", type="number")],
            rows=[{"period": "2024-01-01", "value": 1.0}],
            row_count=1,
        )
    )
    assert spec.kind == "line"
    assert spec.x == "period"


def test_chart_picks_pie_for_few_categories():
    spec = choose_chart(group_envelope())
    assert spec.kind == "pie"


def test_chart_picks_bar_for_many_categories():
    rows = [{"group": f"g{i}", "value": float(i)} for i in range(20)]
    spec = choose_chart(
        ResultEnvelope(
            tool="group_by_metric",
            columns=[ResultColumn(name="group", type="text"), ResultColumn(name="value", type="number")],
            rows=rows,
            row_count=len(rows),
        )
    )
    assert spec.kind == "bar"


def test_chart_picks_scatter_for_two_numeric_columns():
    spec = choose_chart(
        ResultEnvelope(
            tool="aggregate_metric",
            columns=[ResultColumn(name="x", type="number"), ResultColumn(name="y", type="number")],
            rows=[{"x": 1.0, "y": 2.0}],
            row_count=1,
        )
    )
    assert spec.kind == "scatter"


def test_chart_picks_table_for_empty_result():
    spec = choose_chart(envelope(rows=[], row_count=0))
    assert spec.kind == "table"
