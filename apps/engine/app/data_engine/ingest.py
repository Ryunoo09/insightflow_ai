"""CSV and XLSX ingestion into an immutable Parquet file (PRD 27)."""

from __future__ import annotations

import csv
import hashlib
import io
import os
from dataclasses import dataclass, field
from pathlib import Path

import pyarrow as pa
import pyarrow.csv as pacsv
import pyarrow.parquet as pq

from app.data_engine.errors import DatasetError
from app.data_engine.normalizer import normalize_column_names

# Null representations from PRD 27.7. Ambiguous tokens like "-" or "0" are not
# converted automatically.
NULL_VALUES = ["", "NA", "N/A", "n/a", "null", "NULL", "None", "#N/A", "nan", "NaN"]

_SAMPLE_BYTES = 65536
_BLOCK_BYTES = 1 << 20
_DELIMITERS = [",", ";", "\t", "|"]


@dataclass
class ColumnMeta:
    ordinal: int
    name: str
    original_name: str
    logical_type: str
    physical_type: str
    role: str


@dataclass
class IngestResult:
    row_count: int
    columns: list[ColumnMeta]
    parquet_path: Path
    parquet_sha256: str
    parquet_size: int
    source_encoding: str | None = None
    source_delimiter: str | None = None
    transformations: list[dict] = field(default_factory=list)
    warnings: list[dict] = field(default_factory=list)


def ingest(
    source_path: Path,
    parquet_path: Path,
    source_format: str,
    source_sha256: str | None,
    max_rows: int,
    max_columns: int,
) -> IngestResult:
    """Normalize a source file into an immutable Parquet artifact."""
    if not source_path.is_file():
        raise DatasetError("DATASET_INVALID_FORMAT", "file sumber tidak ditemukan")
    if source_sha256:
        _verify_checksum(source_path, source_sha256)

    parquet_path.parent.mkdir(parents=True, exist_ok=True)
    if source_format == "csv":
        return _ingest_csv(source_path, parquet_path, max_rows, max_columns)
    if source_format == "xlsx":
        return _ingest_xlsx(source_path, parquet_path, max_rows, max_columns)
    raise DatasetError(
        "DATASET_UNSUPPORTED_FORMAT",
        f"format tidak didukung: {source_format}",
        http_status=415,
    )


def _verify_checksum(path: Path, expected: str) -> None:
    actual = _file_sha256(path)
    if actual.lower() != expected.lower():
        raise DatasetError(
            "DATASET_CHECKSUM_MISMATCH",
            "checksum file sumber tidak cocok.",
            details={"expected": expected, "actual": actual},
        )


# ---------- CSV ----------

def _ingest_csv(source_path: Path, parquet_path: Path, max_rows: int, max_columns: int) -> IngestResult:
    with open(source_path, "rb") as fh:
        sample = fh.read(_SAMPLE_BYTES)

    if not sample.strip():
        raise DatasetError("DATASET_EMPTY", "file tidak memiliki data.")

    encoding = _detect_encoding(sample)
    delimiter = _detect_delimiter(sample.decode(encoding, errors="replace"))

    read_opts = pacsv.ReadOptions(block_size=_BLOCK_BYTES)
    parse_opts = pacsv.ParseOptions(delimiter=delimiter)
    convert_opts = pacsv.ConvertOptions(null_values=NULL_VALUES, strings_can_be_null=True)

    if encoding == "utf-8":
        source: str | io.BytesIO = str(source_path)
    else:
        # Non-UTF-8 files are transcoded once; larger-than-limit files would be
        # rejected before this point.
        text = source_path.read_bytes().decode(encoding, errors="replace")
        source = io.BytesIO(text.encode("utf-8"))

    try:
        reader = pacsv.open_csv(
            source,
            read_options=read_opts,
            parse_options=parse_opts,
            convert_options=convert_opts,
        )
    except pa.ArrowInvalid as exc:
        raise DatasetError("DATASET_PARSE_ERROR", f"CSV tidak dapat dibaca: {exc}") from exc

    schema = reader.schema
    if schema is None or schema.names == []:
        raise DatasetError("DATASET_EMPTY", "file tidak memiliki kolom.")
    if len(schema.names) > max_columns:
        raise DatasetError(
            "DATASET_TOO_MANY_COLUMNS",
            f"file memiliki {len(schema.names)} kolom; batas {max_columns}.",
            details={"columns": len(schema.names), "limit": max_columns},
        )

    normalized = normalize_column_names(schema.names)
    columns = _columns_from_schema(schema, normalized)

    tmp = _tmp_path(parquet_path)
    writer: pq.ParquetWriter | None = None
    rows = 0
    failure: DatasetError | None = None
    try:
        for batch in reader:
            if batch.num_rows == 0:
                continue
            renamed = batch.rename_columns(normalized)
            if writer is None:
                writer = pq.ParquetWriter(tmp, renamed.schema, compression="snappy")
            writer.write_batch(renamed)
            rows += batch.num_rows
            if rows > max_rows:
                raise DatasetError(
                    "DATASET_TOO_MANY_ROWS",
                    f"file memiliki lebih dari {max_rows} baris; batas {max_rows}.",
                    details={"limit": max_rows},
                )
    except DatasetError as exc:
        failure = exc
    except pa.ArrowInvalid as exc:
        failure = DatasetError("DATASET_PARSE_ERROR", f"CSV tidak dapat dibaca: {exc}")
    finally:
        if writer is not None:
            writer.close()

    if failure is not None:
        _remove(tmp)
        raise failure
    if rows == 0:
        _remove(tmp)
        raise DatasetError("DATASET_EMPTY", "file tidak memiliki data.")

    return _finalize(parquet_path, tmp, rows, columns, encoding, delimiter, schema, normalized)


# ---------- XLSX ----------

def _ingest_xlsx(source_path: Path, parquet_path: Path, max_rows: int, max_columns: int) -> IngestResult:
    names, rows_data = _read_first_data_sheet(source_path)

    if len(names) > max_columns:
        raise DatasetError(
            "DATASET_TOO_MANY_COLUMNS",
            f"file memiliki {len(names)} kolom; batas {max_columns}.",
            details={"columns": len(names), "limit": max_columns},
        )
    if len(rows_data) > max_rows:
        raise DatasetError(
            "DATASET_TOO_MANY_ROWS",
            f"file memiliki lebih dari {max_rows} baris; batas {max_rows}.",
            details={"limit": max_rows},
        )
    if not rows_data:
        raise DatasetError("DATASET_EMPTY", "file tidak memiliki data.")

    width = len(names)
    padded = [list(row[:width]) + [None] * (width - len(row)) for row in rows_data]
    arrays = [_safe_array(list(col)) for col in zip(*padded)]
    table = pa.Table.from_arrays(arrays, names=names)

    normalized = normalize_column_names(names)
    table = table.rename_columns(normalized)
    columns = _columns_from_table(table, normalized, original_names=names)

    tmp = _tmp_path(parquet_path)
    try:
        pq.write_table(table, tmp, compression="snappy")
    except pa.ArrowInvalid as exc:
        _remove(tmp)
        raise DatasetError("DATASET_PARSE_ERROR", f"XLSX tidak dapat ditulis: {exc}") from exc

    return _finalize(parquet_path, tmp, table.num_rows, columns, None, None, table.schema, normalized)


def _read_first_data_sheet(source_path: Path) -> tuple[list[str], list[tuple]]:
    import openpyxl

    try:
        workbook = openpyxl.load_workbook(str(source_path), read_only=True, data_only=True)
    except Exception as exc:  # noqa: BLE001 - openpyxl raises many types
        raise DatasetError("DATASET_INVALID_FORMAT", "XLSX tidak dapat dibuka.") from exc

    try:
        for sheet in workbook.worksheets:
            rows = sheet.iter_rows(values_only=True)
            header = next(rows, None)
            if header is None:
                continue
            data = list(rows)
            if not data:
                continue
            names = [
                str(value) if value is not None else f"column_{i + 1}"
                for i, value in enumerate(header)
            ]
            return names, data
    finally:
        workbook.close()

    raise DatasetError("DATASET_EMPTY", "file tidak memiliki data.")


# ---------- Shared ----------

def _finalize(
    parquet_path: Path,
    tmp: Path,
    rows: int,
    columns: list[ColumnMeta],
    encoding: str | None,
    delimiter: str | None,
    schema: pa.Schema,
    normalized: list[str],
) -> IngestResult:
    try:
        os.replace(tmp, parquet_path)
    except OSError as exc:
        _remove(tmp)
        raise DatasetError("DATASET_PROCESS_FAILED", "gagal menyimpan Parquet.", http_status=500) from exc

    transformations = [
        {"type": "rename_column", "column": orig, "to": norm}
        for orig, norm in zip(schema.names, normalized)
        if orig != norm
    ]
    return IngestResult(
        row_count=rows,
        columns=columns,
        parquet_path=parquet_path,
        parquet_sha256=_file_sha256(parquet_path),
        parquet_size=parquet_path.stat().st_size,
        source_encoding=encoding,
        source_delimiter=delimiter,
        transformations=transformations,
    )


def _columns_from_schema(schema: pa.Schema, normalized: list[str]) -> list[ColumnMeta]:
    return _columns_from_table(schema, normalized, original_names=schema.names)


def _columns_from_table(schema: pa.Schema, normalized: list[str], original_names: list[str]) -> list[ColumnMeta]:
    metas: list[ColumnMeta] = []
    for i, name in enumerate(normalized):
        logical, physical, role = _map_type(schema.field(i).type)
        metas.append(
            ColumnMeta(
                ordinal=i + 1,
                name=name,
                original_name=original_names[i],
                logical_type=logical,
                physical_type=physical,
                role=role,
            )
        )
    return metas


def _map_type(arrow_type: pa.DataType) -> tuple[str, str, str]:
    if pa.types.is_boolean(arrow_type):
        return "boolean", "BOOLEAN", "dimension"
    if pa.types.is_integer(arrow_type):
        return "integer", "BIGINT", "measure"
    if pa.types.is_floating(arrow_type):
        return "decimal", "DOUBLE", "measure"
    if pa.types.is_decimal(arrow_type):
        return "decimal", "DECIMAL", "measure"
    if pa.types.is_date(arrow_type):
        return "date", "DATE", "time"
    if pa.types.is_timestamp(arrow_type):
        return "datetime", "TIMESTAMP", "time"
    if pa.types.is_time(arrow_type):
        return "datetime", "TIME", "time"
    return "text", "VARCHAR", "dimension"


def _safe_array(values: list) -> pa.Array:
    try:
        return pa.array(values)
    except (pa.ArrowInvalid, pa.ArrowTypeError):
        return pa.array([None if v is None else str(v) for v in values])


def _detect_encoding(sample: bytes) -> str:
    try:
        sample.decode("utf-8")
        return "utf-8"
    except UnicodeDecodeError:
        return "cp1252"


def _detect_delimiter(text: str) -> str:
    head = "\n".join(text.splitlines()[:5])
    try:
        return csv.Sniffer().sniff(head, delimiters=",;\t|").delimiter
    except csv.Error:
        counts = {d: head.count(d) for d in _DELIMITERS}
        best = max(counts, key=lambda k: counts[k])
        return best if counts[best] > 0 else ","


def _tmp_path(parquet_path: Path) -> Path:
    return parquet_path.with_name(parquet_path.name + ".tmp")


def _remove(path: Path) -> None:
    try:
        path.unlink()
    except FileNotFoundError:
        pass


def _file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with open(path, "rb") as fh:
        for chunk in iter(lambda: fh.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()
