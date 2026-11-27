"""Anti-hallucination grounding check (Plan 06 / TASK-605).

Every numeric token in the narrative must trace back to a value computed by a
tool. Numbers that do not are hallucinations, and the narrative is replaced by a
deterministic template rather than shown to the user.

Tolerance exists because a model legitimately rounds: 1,234.567 may appear as
"1.234,57" or "1.2 ribu". Formatting differences are normalized before the
comparison, but the *value* must still exist in the result envelope.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field

from app.schemas.result import ResultEnvelope

# Matches 1234, 1,234.5, 1.234,56, 12%, 1.5e3, -42, Rp1.500.000
_NUMBER_RE = re.compile(r"-?\d[\d.,]*\d|-?\d")
# A number glued to a word ("2024" in "Q1 2024") is still a number; nothing is
# skipped. Only the scale words below change the value.
_SCALE_WORDS = {
    "ribu": 1_000.0,
    "rb": 1_000.0,
    "juta": 1_000_000.0,
    "jt": 1_000_000.0,
    "miliar": 1_000_000_000.0,
    "milyar": 1_000_000_000.0,
    "triliun": 1_000_000_000_000.0,
    "k": 1_000.0,
    "mn": 1_000_000.0,
}

# A number is accepted when it is within this relative distance of a result
# value, which covers legitimate rounding to two decimals.
_RELATIVE_TOLERANCE = 0.02
_ABSOLUTE_TOLERANCE = 0.5


@dataclass
class GroundingReport:
    grounded: bool
    ungrounded: list[str] = field(default_factory=list)
    checked: int = 0


def check(narrative: str, envelope: ResultEnvelope) -> GroundingReport:
    """Verify every numeric token in narrative appears in the envelope."""
    allowed = envelope.numeric_values()
    tokens = _extract_numbers(narrative)
    ungrounded = [raw for raw, value in tokens if not _matches_any(value, allowed)]
    return GroundingReport(grounded=not ungrounded, ungrounded=ungrounded, checked=len(tokens))


def _extract_numbers(text: str) -> list[tuple[str, float]]:
    found: list[tuple[str, float]] = []
    for match in _NUMBER_RE.finditer(text):
        raw = match.group(0)
        value = _parse_number(raw)
        if value is None:
            continue
        tail = text[match.end() : match.end() + 12].strip().lower()
        for word, scale in _SCALE_WORDS.items():
            if tail.startswith(word):
                value *= scale
                break
        found.append((raw, value))
    return found


def _parse_number(raw: str) -> float | None:
    """Interpret a token as a number, handling both 1,234.56 and 1.234,56."""
    text = raw.replace(" ", "")
    if text in ("-", ""):
        return None
    has_comma = "," in text
    has_dot = "." in text
    if has_comma and has_dot:
        # The rightmost separator is the decimal one.
        if text.rfind(",") > text.rfind("."):
            text = text.replace(".", "").replace(",", ".")
        else:
            text = text.replace(",", "")
    elif has_comma:
        # "1,234" is ambiguous; treat a 3-digit tail as a thousands separator.
        head, _, tail = text.partition(",")
        if len(tail) == 3 and tail.isdigit():
            text = head + tail
        else:
            text = text.replace(",", ".")
    elif has_dot:
        # Same ambiguity for Indonesian text: "1.235" means 1235, not 1.235.
        head, _, tail = text.partition(".")
        if len(tail) == 3 and tail.isdigit():
            text = head + tail
    try:
        return float(text)
    except ValueError:
        return None


def _matches_any(value: float, allowed: list[float]) -> bool:
    for candidate in allowed:
        if abs(value - candidate) <= _ABSOLUTE_TOLERANCE:
            return True
        if candidate != 0 and abs(value - candidate) / abs(candidate) <= _RELATIVE_TOLERANCE:
            return True
    return False
