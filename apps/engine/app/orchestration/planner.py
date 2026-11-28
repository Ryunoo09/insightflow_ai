"""LLM planner: question + schema -> one validated AnalysisPlan (Plan 06 / TASK-603).

The provider probe (§9.1) fixed three things this module must do:

* ask with ``response_format: json_schema`` + ``strict: true`` only,
* put the conditional rule in the schema itself (``time_series_metric`` requires
  ``group_by`` and ``bucket``), because prompt wording alone was ignored,
* repair a rejected plan exactly once — the best provider still failed ~17% of
  the time on the first attempt.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

from app.llm.client import LLMClient, LLMError, Usage
from app.orchestration.plan_validator import PlanError, PlanValidator
from app.schemas.plan import AGGREGATIONS, BUCKETS, TOOL_NAMES, AnalysisPlan

PROMPT_PATH = Path(__file__).resolve().parent.parent / "llm" / "prompts" / "planner" / "v1.txt"

# JSON schema handed to the provider. `additionalProperties: false` plus the
# `allOf/if-then` block is what keeps a model from inventing fields or dropping
# `bucket` for a time series.
PLAN_JSON_SCHEMA: dict[str, Any] = {
    "type": "object",
    "additionalProperties": False,
    "required": ["tool", "column", "aggregation", "intent"],
    "properties": {
        "tool": {"type": "string", "enum": list(TOOL_NAMES)},
        "column": {"type": "string"},
        "aggregation": {"type": "string", "enum": list(AGGREGATIONS)},
        "group_by": {"type": "string"},
        "bucket": {"type": "string", "enum": list(BUCKETS)},
        "filters": {"type": "object", "additionalProperties": {"type": "string"}},
        "intent": {"type": "string"},
    },
    "allOf": [
        {
            "if": {"properties": {"tool": {"const": "time_series_metric"}}, "required": ["tool"]},
            "then": {"required": ["group_by", "bucket"]},
        }
    ],
}


def build_planner_prompt(dataset: dict[str, Any], question: str) -> str:
    """Render the planner user message: schema summary plus the question."""
    lines = [
        f"Dataset rows: {dataset.get('row_count', 0)}",
        f"Dataset columns: {dataset.get('column_count', 0)}",
        "Schema (name | type | original header):",
    ]
    for column in dataset.get("columns", []):
        lines.append(
            f"- {column.get('name')} | {column.get('type')} | {column.get('original_name') or column.get('name')}"
        )
    lines.append("")
    lines.append(f"Question: {question}")
    return "\n".join(lines)


def plan_question(
    *,
    client: LLMClient,
    validator: PlanValidator,
    dataset: dict[str, Any],
    question: str,
    max_attempts: int = 2,
    usage: Usage | None = None,
) -> tuple[AnalysisPlan, int]:
    """Ask the model for a plan, validate it, and repair at most once.

    Returns the accepted plan and the number of LLM calls spent (1 or 2).
    """
    system = PROMPT_PATH.read_text(encoding="utf-8")
    user = build_planner_prompt(dataset, question)
    last_error: PlanError | None = None

    for attempt in range(1, max_attempts + 1):
        response = client.complete_json(system=system, user=user, schema=PLAN_JSON_SCHEMA)
        if usage is not None:
            usage.add(response)
        plan = _parse_plan(response.content)
        try:
            return validator.validate(plan), attempt
        except PlanError as exc:
            last_error = exc
            # Feed the rejection back once; a second failure is final.
            user = (
                f"{build_planner_prompt(dataset, question)}\n\n"
                f"Your previous answer was rejected: {exc.message}\n"
                "Return a corrected JSON object that satisfies every rule."
            )
    raise last_error or PlanError("PLAN_INVALID", "rencana analisis tidak valid.")


def _parse_plan(content: str) -> AnalysisPlan:
    """Parse the model's JSON. A malformed body is a controlled failure."""
    try:
        data = json.loads(content)
    except (json.JSONDecodeError, TypeError) as exc:
        raise LLMError(
            "LLM_INVALID_JSON",
            "model tidak mengembalikan JSON yang valid.",
            http_status=502,
            retryable=False,
        ) from exc
    try:
        return AnalysisPlan.model_validate(data)
    except ValueError as exc:
        raise PlanError("PLAN_INVALID", f"rencana analisis tidak sesuai skema: {exc}") from exc
