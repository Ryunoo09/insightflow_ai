import hashlib

import pyarrow.parquet as pq
import pytest
from openpyxl import Workbook

from app.data_engine.errors import DatasetError
from app.data_engine.ingest import ingest


def _sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _run(tmp_path, text, name="in.csv", encoding="utf-8", **limits):
    src = tmp_path / name
    src.write_text(text, encoding=encoding)
    out = tmp_path / "storage" / "data.parquet"
    kwargs = {"max_rows": 1000, "max_columns": 50}
    kwargs.update(limits)
    return ingest(
        source_path=src,
        parquet_path=out,
        source_format="csv",
        source_sha256=_sha256(src),
        **kwargs,
    )


def test_csv_comma_renames_columns(tmp_path):
    result = _run(tmp_path, "Tanggal Order,Revenue\n2025-01-01,1000\n2025-01-02,2000\n")
    assert result.row_count == 2
    assert [c.name for c in result.columns] == ["tanggal_order", "revenue"]
    assert result.columns[0].original_name == "Tanggal Order"
    table = pq.read_table(result.parquet_path)
    assert table.num_rows == 2
    assert table.column_names == ["tanggal_order", "revenue"]
    assert result.parquet_sha256
    assert not (result.parquet_path.parent / "data.parquet.tmp").exists()


def test_csv_semicolon_and_tab(tmp_path):
    semi = _run(tmp_path, "a;b\n1;2\n", name="semi.csv")
    assert semi.source_delimiter == ";"
    tab = _run(tmp_path, "a\tb\n1\t2\n", name="tab.csv")
    assert tab.source_delimiter == "\t"


def test_csv_null_tokens(tmp_path):
    result = _run(tmp_path, "a,b\nNA,\n1,2\n", name="nulls.csv")
    table = pq.read_table(result.parquet_path)
    assert table.column("a").null_count == 1
    assert table.column("b").null_count == 1


def test_duplicate_column_names_are_unique(tmp_path):
    result = _run(tmp_path, "Value,Value,value\n1,2,3\n")
    names = [c.name for c in result.columns]
    assert len(names) == len(set(names))
    assert names[0] == "value"


def test_checksum_mismatch(tmp_path):
    src = tmp_path / "in.csv"
    src.write_text("a\n1\n", encoding="utf-8")
    with pytest.raises(DatasetError) as exc:
        ingest(
            source_path=src,
            parquet_path=tmp_path / "data.parquet",
            source_format="csv",
            source_sha256="deadbeef",
            max_rows=100,
            max_columns=50,
        )
    assert exc.value.code == "DATASET_CHECKSUM_MISMATCH"


def test_too_many_columns(tmp_path):
    with pytest.raises(DatasetError) as exc:
        _run(tmp_path, "a,b,c\n1,2,3\n", max_columns=2)
    assert exc.value.code == "DATASET_TOO_MANY_COLUMNS"


def test_too_many_rows(tmp_path):
    with pytest.raises(DatasetError) as exc:
        _run(tmp_path, "a\n1\n2\n3\n", max_rows=2)
    assert exc.value.code == "DATASET_TOO_MANY_ROWS"


def test_empty_file(tmp_path):
    with pytest.raises(DatasetError) as exc:
        _run(tmp_path, "", name="empty.csv")
    assert exc.value.code == "DATASET_EMPTY"


def test_header_only(tmp_path):
    with pytest.raises(DatasetError) as exc:
        _run(tmp_path, "a,b\n", name="header.csv")
    assert exc.value.code == "DATASET_EMPTY"


def test_windows1252_encoding(tmp_path):
    src = tmp_path / "latin.csv"
    src.write_bytes("Nama,Nilai\nCafé,5\n".encode("cp1252"))
    out = tmp_path / "data.parquet"
    result = ingest(
        source_path=src,
        parquet_path=out,
        source_format="csv",
        source_sha256=_sha256(src),
        max_rows=100,
        max_columns=50,
    )
    assert result.source_encoding == "cp1252"
    assert pq.read_table(out).num_rows == 1


def test_xlsx_roundtrip(tmp_path):
    src = tmp_path / "book.xlsx"
    workbook = Workbook()
    sheet = workbook.active
    sheet.append(["Nama", "Jumlah"])
    sheet.append(["A", 5])
    sheet.append(["B", 10])
    workbook.save(src)

    out = tmp_path / "data.parquet"
    result = ingest(
        source_path=src,
        parquet_path=out,
        source_format="xlsx",
        source_sha256=_sha256(src),
        max_rows=100,
        max_columns=50,
    )
    assert result.row_count == 2
    assert [c.name for c in result.columns] == ["nama", "jumlah"]
    assert pq.read_table(out).num_rows == 2
