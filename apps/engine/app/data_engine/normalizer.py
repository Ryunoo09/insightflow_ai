"""Column-name normalization (PRD 27.6).

Names become lowercase ASCII snake_case; duplicates gain a numeric suffix. The
original name is always retained by the caller as metadata.
"""

from __future__ import annotations

import re
import unicodedata

# A small guard against colliding with SQL keywords in generated queries.
_RESERVED = {
    "all", "and", "as", "by", "case", "cast", "column", "create", "default",
    "delete", "desc", "distinct", "drop", "else", "end", "exists", "from",
    "group", "having", "in", "index", "insert", "into", "is", "join", "limit",
    "not", "null", "on", "or", "order", "select", "table", "then", "union",
    "update", "values", "when", "where",
}

_MAX_LEN = 128


def normalize_column_names(names: list[str]) -> list[str]:
    """Return unique snake_case names, in order, for the given raw names."""
    used: set[str] = set()
    out: list[str] = []
    for raw in names:
        base = _normalize_one(raw)
        candidate = base
        suffix = 2
        while candidate in used:
            candidate = f"{base}_{suffix}"
            suffix += 1
        used.add(candidate)
        out.append(candidate)
    return out


def _normalize_one(raw: str) -> str:
    # Fold accents to ASCII, then drop everything that is not [a-z0-9].
    folded = unicodedata.normalize("NFKD", raw or "")
    ascii_name = folded.encode("ascii", "ignore").decode("ascii")
    ascii_name = re.sub(r"[^A-Za-z0-9]+", "_", ascii_name).strip("_").lower()
    if not ascii_name:
        ascii_name = "column"
    if ascii_name[0].isdigit():
        ascii_name = "col_" + ascii_name
    if ascii_name in _RESERVED:
        ascii_name = ascii_name + "_col"
    return ascii_name[:_MAX_LEN]
