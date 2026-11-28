"""Grounded narrator (Plan 06 / TASK-605).

Only aggregate results (<50 rows) and the original question reach the model.
The narrative is then grounded against the ResultEnvelope; a single failed
regeneration falls back to a deterministic template, so the user never sees an
unverified number.
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Any

from app.data_engine.errors import DatasetError
from app.llm.client import LLMClient, LLMError, Usage
from app.orchestration.grounding_checker import check
from app.schemas.chart import ChartSpec
from app.schemas.result import ResultEnvelope

# The narrator only ever sees this many result rows.
NARRATOR_MAX_ROWS = 50

NARRATOR_SYSTEM = (
    "You are InsightFlow's business analyst. Write a short, factual summary in "
    "Indonesian (2-4 sentences) of the computed result below.\n"
    "Rules:\n"
    "1. Use ONLY numbers that appear in the result. Never estimate, extrapolate, "
    "or invent a number.\n"
    "2. Do not mention columns, tools, SQL, or the computation process.\n"
    "3. Plain text only: no markdown tables, no HTML, no code fences.\n"
    "4. If the result is empty, say that no data matched.\n"
)


@dataclass
class Narrative:
    text: str
    grounded: bool
    attempts: int
    fallback_reason: str = ""


def narrate(
    *,
    client: LLMClient,
    question: str,
    envelope: ResultEnvelope,
    chart: ChartSpec,
    max_attempts: int = 2,
    usage: Usage | None = None,
) -> Narrative:
    """Produce a narrative and prove every number in it came from the result."""
    prompt = _build_prompt(question, envelope, chart)
    attempts = 0
    last_report_reason = ""

    for attempt in range(1, max_attempts + 1):
        attempts = attempt
        try:
            response = client.complete_text(system=NARRATOR_SYSTEM, user=prompt)
        except LLMError as exc:
            # A provider failure must not hide the computed result.
            return Narrative(text=template_narrative(question, envelope), grounded=True, attempts=attempt, fallback_reason=exc.code)
        if usage is not None:
            usage.add(response)
        text = response.content.strip()
        report = check(text, envelope)
        if report.grounded:
            return Narrative(text=text, grounded=True, attempts=attempt)
        last_report_reason = "angka tidak terverifikasi: " + ", ".join(report.ungrounded[:5])
        prompt = (
            f"{_build_prompt(question, envelope, chart)}\n\n"
            f"Your previous answer contained numbers not present in the result: "
            f"{', '.join(report.ungrounded[:5])}. Rewrite using only the numbers above."
        )

    return Narrative(
        text=template_narrative(question, envelope),
        grounded=True,
        attempts=attempts,
        fallback_reason=last_report_reason,
    )


def _build_prompt(question: str, envelope: ResultEnvelope, chart: ChartSpec) -> str:
    rows = envelope.rows[:NARRATOR_MAX_ROWS]
    payload: dict[str, Any] = {
        "question": question,
        "tool": envelope.tool,
        "columns": [{"name": c.name, "type": c.type} for c in envelope.columns],
        "rows": rows,
        "row_count": envelope.row_count,
        "summary_stats": envelope.summary_stats,
        "suggested_chart": chart.kind,
    }
    return json.dumps(payload, ensure_ascii=False, indent=2, default=str)


def template_narrative(question: str, envelope: ResultEnvelope) -> str:
    """Deterministic, always-correct narrative used when the model cannot be trusted."""
    if not envelope.rows:
        return "Tidak ada data yang cocok dengan pertanyaan tersebut."
    if envelope.row_count == 1 and "value" in envelope.rows[0]:
        value = _format_number(envelope.rows[0]["value"])
        column = envelope.summary_stats.get("column", "nilai")
        aggregation = envelope.summary_stats.get("aggregation", "hasil")
        return f"{aggregation} dari {column} adalah {value}."
    if envelope.tool == "group_by_metric":
        top = envelope.rows[0]
        group = top.get("group")
        value = _format_number(top.get("value"))
        return (
            f"Terdapat {envelope.row_count} kelompok pada hasil ini. "
            f"Nilai tertinggi ada pada {group} dengan {value}."
        )
    if envelope.tool == "time_series_metric":
        first, last = envelope.rows[0], envelope.rows[-1]
        return (
            f"Deret waktu mencakup {envelope.row_count} periode, "
            f"dari {first.get('period')} sampai {last.get('period')}."
        )
    return f"Analisis menghasilkan {envelope.row_count} baris data."


def _format_number(value: object) -> str:
    """Render with Indonesian conventions: '.' thousands, ',' decimals."""
    if isinstance(value, bool):
        return str(value)
    if isinstance(value, int):
        return f"{value:,}".replace(",", ".")
    if isinstance(value, float):
        return f"{value:,.2f}".replace(",", "\x00").replace(".", ",").replace("\x00", ".")
    return str(value)
