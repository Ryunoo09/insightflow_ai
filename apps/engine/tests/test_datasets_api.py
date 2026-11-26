import hashlib

from fastapi.testclient import TestClient

from app.api.v1 import datasets as datasets_api
from app.config import settings
from app.data_engine.errors import DatasetError
from app.main import app
from app.storage.resolver import StorageResolver

WS = "11111111-1111-1111-1111-111111111111"
DS = "22222222-2222-2222-2222-222222222222"


def _client(tmp_path, monkeypatch, token="secret"):
    monkeypatch.setattr(settings, "storage_root", str(tmp_path))
    monkeypatch.setattr(settings, "internal_token", token)
    return TestClient(app)


def _body(source_sha256):
    return {
        "dataset_ref": {"workspace_id": WS, "dataset_id": DS, "version_no": 1},
        "source_format": "csv",
        "source_sha256": source_sha256,
        "limits": {"max_rows": 100, "max_columns": 50},
    }


def test_process_rejects_missing_token(tmp_path, monkeypatch):
    client = _client(tmp_path, monkeypatch)
    response = client.post("/v1/datasets/process", json=_body("abc"))
    assert response.status_code == 401
    assert response.json()["error"]["code"] == "INTERNAL_AUTH_REQUIRED"


def test_process_happy_path(tmp_path, monkeypatch):
    client = _client(tmp_path, monkeypatch)
    source = StorageResolver(tmp_path).source_path(WS, DS, 1, "csv")
    source.parent.mkdir(parents=True, exist_ok=True)
    source.write_text("Tanggal Order,Revenue\n2025-01-01,1000\n2025-01-02,2000\n", encoding="utf-8")
    digest = hashlib.sha256(source.read_bytes()).hexdigest()

    response = client.post(
        "/v1/datasets/process",
        json=_body(digest),
        headers={"X-Internal-Token": "secret"},
    )

    assert response.status_code == 200, response.text
    body = response.json()
    assert body["row_count"] == 2
    assert [c["name"] for c in body["columns"]] == ["tanggal_order", "revenue"]
    assert body["parquet"]["sha256"]
    assert body["profile"]["quality_score"] > 0
    assert body["processing_report"]["report_version"] == 1

    parquet = StorageResolver(tmp_path).parquet_path(WS, DS, 1)
    assert parquet.is_file()


def test_process_maps_profile_timeout_as_non_retryable(tmp_path, monkeypatch):
    client = _client(tmp_path, monkeypatch)
    source = StorageResolver(tmp_path).source_path(WS, DS, 1, "csv")
    source.parent.mkdir(parents=True, exist_ok=True)
    source.write_text("a,b\n1,2\n", encoding="utf-8")
    digest = hashlib.sha256(source.read_bytes()).hexdigest()

    captured = {}

    def fake_profile(path, columns, **kwargs):
        captured.update(kwargs)
        raise DatasetError("DATASET_RESOURCE_EXHAUSTED", "profiling melebihi batas waktu")

    monkeypatch.setattr(datasets_api, "profile_parquet", fake_profile)

    response = client.post(
        "/v1/datasets/process",
        json=_body(digest),
        headers={"X-Internal-Token": "secret"},
    )

    assert response.status_code == 422, response.text
    error = response.json()["error"]
    assert error["code"] == "DATASET_RESOURCE_EXHAUSTED"
    assert error["retryable"] is False
    assert captured["timeout_seconds"] == settings.profile_timeout_seconds
