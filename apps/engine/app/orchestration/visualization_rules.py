"""Deterministic chart selection (Plan 06 / TASK-605).

The model never picks a chart. Shape of the data decides it, so the same result
always renders the same way.
"""

from __future__ import annotations

from app.schemas.chart import ChartSpec
from app.schemas.result import ResultEnvelope

# Above this many categories a vertical bar chart becomes unreadable.
PIE_MAX_SLICES = 8
SCATTER_MAX_POINTS = 2000


def choose_chart(envelope: ResultEnvelope) -> ChartSpec:
    """Map a ResultEnvelope onto a ChartSpec using only column types and shape."""
    names = [column.name for column in envelope.columns]
    types = {column.name: column.type for column in envelope.columns}
    rows = envelope.rows

    if not rows:
        return ChartSpec(kind="table", title="Tidak ada data", reason="hasil kosong")

    if envelope.tool == "correlation_matrix":
        return ChartSpec(
            kind="scatter",
            x="x",
            y="value",
            series=["y"],
            title="Matriks korelasi",
            reason="tool korelasi menghasilkan pasangan kolom dan nilai",
        )

    if envelope.tool == "time_series_metric":
        return ChartSpec(
            kind="line",
            x="period",
            y="value",
            series=["value"],
            title="Tren waktu",
            reason="sumbu waktu dengan satu metrik",
        )

    if envelope.tool == "distribution_metric":
        return ChartSpec(
            kind="bar",
            x="bucket_start",
            y="count",
            series=["count"],
            title="Distribusi nilai",
            reason="histogram frekuensi",
        )

    numeric = [name for name in names if types.get(name) == "number"]
    categories = [name for name in names if types.get(name) in ("text", "date")]

    if categories and numeric:
        category = categories[0]
        metric = numeric[0]
        if len(rows) <= PIE_MAX_SLICES and len(numeric) == 1:
            return ChartSpec(
                kind="pie",
                x=category,
                y=metric,
                series=[metric],
                title=f"{metric} per {category}",
                reason=f"{len(rows)} kategori (<= {PIE_MAX_SLICES}) dengan satu metrik",
            )
        return ChartSpec(
            kind="bar",
            x=category,
            y=metric,
            series=[metric],
            title=f"{metric} per {category}",
            reason=f"{len(rows)} kategori dengan satu metrik",
        )

    if len(numeric) >= 2:
        return ChartSpec(
            kind="scatter",
            x=numeric[0],
            y=numeric[1],
            series=numeric[:2],
            title=f"{numeric[1]} vs {numeric[0]}",
            reason="dua kolom numerik",
        )

    return ChartSpec(kind="table", title="Hasil analisis", reason="bentuk data tidak dapat divisualisasikan")
