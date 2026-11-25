import asyncio
import os

import pytest

from app.storage.resolver import StorageResolver, UnsafePathError


def test_builds_paths_under_root(tmp_path):
    resolver = StorageResolver(tmp_path)

    parquet = resolver.parquet_path("ws1", "ds1", 2)
    assert parquet == tmp_path.resolve() / "parquet" / "ws1" / "ds1" / "2" / "data.parquet"

    source = resolver.source_path("ws1", "ds1", 1, ".CSV")
    assert source.name == "source.csv"

    assert resolver.temp_path("scratch.tmp").parent == tmp_path.resolve() / "temp"


@pytest.mark.parametrize(
    "call",
    [
        lambda r: r.temp_path("../../etc/passwd"),
        lambda r: r.source_path("a/b", "ds1", 1, "csv"),
        lambda r: r.parquet_path("ws1", "..", 1),
        lambda r: r.parquet_path("ws1\\evil", "ds1", 1),
        lambda r: r.parquet_path("", "ds1", 1),
        lambda r: r.source_path("ws1", "ds1", 1, "."),
    ],
)
def test_rejects_unsafe_segments(tmp_path, call):
    with pytest.raises(UnsafePathError):
        call(StorageResolver(tmp_path))


def test_rejects_empty_root():
    with pytest.raises(ValueError):
        StorageResolver("")
