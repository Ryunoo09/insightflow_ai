"""Planner and validator tests (Plan 06 / TASK-603).

The validator is tested against the failure modes that were actually observed
during the provider probe: SQL fragments in identifiers, `sum` on a text column,
and time series without a date group_by.
"""

import json

import pytest

from app.data_engine.errors import DatasetError
from app.llm.client import LLMError, LLMResponse
from app.orchestration.planner import PLAN_JSON_SCHEMA, build_planner_prompt, plan_question
from app.orchestration.plan_validator import ColumnInfo, PlanError, PlanValidator
from app.schemas.plan import AnalysisPlan

COLUMNS = [
    ColumnInfo(name="id", type="number"),
    ColumnInfo(name="category", type="text"),
    ColumnInfo(name="amount", type="number"),
    ColumnInfo(name="created", type="date"),
]


def validator() -> PlanValidator:
    return PlanValidator(COLUMNS)


def plan(**overrides) -> AnalysisPlan:
    payload = {"tool": "aggregate_metric", "column": "amount", "aggregation": "sum", "intent": "total"}
    payload.update(overrides)
    return AnalysisPlan(**payload)


def test_accepts_valid_aggregate():
    accepted = validator().validate(plan())
    assert accepted.column == "amount"


def test_rejects_unknown_column():
    with pytest.raises(PlanError) as err:
        validator().validate(plan(column="profit"))
    assert err.value.code == "PLAN_UNKNOWN_COLUMN"


def test_rejects_sql_fragment_as_identifier():
    # The model emitted exactly this shape during the probe.
    with pytest.raises(ValueError):
        AnalysisPlan(tool="group_by_metric", column="amount", aggregation="sum", group_by="date_trunc('month', created)")


def test_rejects_sum_on_text_column():
    with pytest.raises(PlanError) as err:
        validator().validate(plan(column="category"))
    assert err.value.code == "PLAN_TYPE_MISMATCH"


def test_count_is_allowed_on_text_column():
    accepted = validator().validate(plan(column="category", aggregation="count"))
    assert accepted.aggregation == "count"


def test_group_by_metric_requires_group_by():
    with pytest.raises(PlanError) as err:
        validator().validate(plan(tool="group_by_metric"))
    assert err.value.code == "PLAN_MISSING_GROUP"


def test_time_series_requires_date_group_by():
    with pytest.raises(PlanError) as err:
        validator().validate(
            plan(tool="time_series_metric", group_by="category", bucket="month")
        )
    assert err.value.code == "PLAN_TYPE_MISMATCH"


def test_time_series_accepts_date_group_with_bucket():
    accepted = validator().validate(
        plan(tool="time_series_metric", group_by="created", bucket="month")
    )
    assert accepted.bucket == "month"


def test_time_series_requires_bucket():
    with pytest.raises(PlanError) as err:
        validator().validate(plan(tool="time_series_metric", group_by="created"))
    assert err.value.code == "PLAN_MISSING_BUCKET"


def test_rejects_unknown_filter_column():
    with pytest.raises(PlanError) as err:
        validator().validate(plan(filters={"region": "east"}))
    assert err.value.code == "PLAN_UNKNOWN_COLUMN"


def test_schema_requires_group_and_bucket_for_time_series():
    conditional = PLAN_JSON_SCHEMA["allOf"][0]
    assert conditional["if"]["properties"]["tool"]["const"] == "time_series_metric"
    assert set(conditional["then"]["required"]) == {"group_by", "bucket"}
    # json_object mode is never offered: the schema always demands a strict shape.
    assert PLAN_JSON_SCHEMA["additionalProperties"] is False


def test_prompt_lists_schema_with_original_headers():
    prompt = build_planner_prompt(
        {
            "row_count": 20,
            "column_count": 2,
            "columns": [
                {"name": "transactionno", "type": "number", "original_name": "TransactionNo"},
                {"name": "amount", "type": "number", "original_name": ""},
            ],
        },
        "total amount?",
    )
    assert "transactionno | number | TransactionNo" in prompt
    assert "Question: total amount?" in prompt


class FakeClient:
    """Returns queued responses and records every call."""

    def __init__(self, contents: list[str]) -> None:
        self._contents = list(contents)
        self.calls = 0

    def complete_json(self, **_kwargs) -> LLMResponse:
        self.calls += 1
        return LLMResponse(content=self._contents.pop(0))


def test_plan_question_accepts_first_valid_plan():
    client = FakeClient([json.dumps({"tool": "aggregate_metric", "column": "amount", "aggregation": "sum", "intent": "x"})])
    accepted, attempts = plan_question(
        client=client,
        validator=validator(),
        dataset={"columns": []},
        question="total?",
    )
    assert accepted.column == "amount"
    assert attempts == 1
    assert client.calls == 1


def test_plan_question_repairs_once_then_fails():
    bad = json.dumps({"tool": "aggregate_metric", "column": "profit", "aggregation": "sum", "intent": "x"})
    client = FakeClient([bad, bad])
    with pytest.raises(PlanError):
        plan_question(client=client, validator=validator(), dataset={"columns": []}, question="profit?")
    assert client.calls == 2


def test_plan_question_succeeds_on_repair():
    bad = json.dumps({"tool": "aggregate_metric", "column": "profit", "aggregation": "sum", "intent": "x"})
    good = json.dumps({"tool": "aggregate_metric", "column": "amount", "aggregation": "sum", "intent": "x"})
    client = FakeClient([bad, good])
    accepted, attempts = plan_question(
        client=client, validator=validator(), dataset={"columns": []}, question="total?"
    )
    assert accepted.column == "amount"
    assert attempts == 2


def test_plan_question_rejects_malformed_json():
    client = FakeClient(["not json at all"])
    with pytest.raises(LLMError) as err:
        plan_question(client=client, validator=validator(), dataset={"columns": []}, question="?")
    assert err.value.code == "LLM_INVALID_JSON"


def test_plan_error_is_a_dataset_error():
    assert issubclass(PlanError, DatasetError)
