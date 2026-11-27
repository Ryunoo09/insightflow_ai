import pyarrow as pa
import pyarrow.parquet as pq
import pytest
from fastapi.testclient import TestClient

from app.analytics.explorer_compiler import ExplorerQueryError, compile_query
from app.config import settings
from app.main import app
from app.schemas.explorer import ExplorerFilter, ExplorerSort, RowsQueryRequest
from app.schemas.profile import DatasetRef
from app.storage.resolver import StorageResolver

WS = "11111111-1111-1111-1111-111111111111"
DS = "22222222-2222-2222-2222-222222222222"
COLUMNS = ["id", "category", "revenue"]


def _request(**overrides):
    payload = {
        "dataset_ref": DatasetRef(workspace_id=WS, dataset_id=DS, version_no=1),
        "offset": 0,
        "limit": 100,
    }
    payload.update(overrides)
    return RowsQueryRequest(**payload)


def test_compile_rejects_unknown_sort_column():
    with pytest.raises(ExplorerQueryError) as err:
        compile_query(COLUMNS, _request(sort=[ExplorerSort(column="id; DROP TABLE users", direction="asc")]))
    assert err.value.code == "EXPLORER_INVALID_COLUMN"


def test_compile_rejects_unknown_filter_column():
    with pytest.raises(ExplorerQueryError) as err:
        compile_query(COLUMNS, _request(filters=[ExplorerFilter(column="secret", operator="eq", value="x")]))
    assert err.value.code == "EXPLORER_INVALID_COLUMN"


def test_compile_binds_filter_values_and_orders():
    plan = compile_query(
        COLUMNS,
        _request(
            offset=20,
            limit=10,
            sort=[ExplorerSort(column="revenue", direction="desc")],
            filters=[ExplorerFilter(column="category", operator="contains", value="100%_x")],
        ),
    )
    assert plan.where_sql == ' WHERE CAST("category" AS VARCHAR) ILIKE ? ESCAPE \'\\\''
    assert plan.where_params == ["%100\\%\\_x%"]
    assert plan.order_sql == ' ORDER BY "revenue" DESC'
    assert (plan.limit, plan.offset) == (10, 20)
    assert "100%_x" not in plan.where_sql


def test_compile_caps_limit():
    plan = compile_query(COLUMNS, _request(limit=500))
    assert plan.limit == 500


def _client(tmp_path, monkeypatch, token="secret"):
    monkeypatch.setattr(settings, "storage_root", str(tmp_path))
    monkeypatch.setattr(settings, "internal_token", token)
    return TestClient(app)


def _write_parquet(tmp_path):
    resolver = StorageResolver(tmp_path)
    path = resolver.parquet_path(WS, DS, 1)
    path.parent.mkdir(parents=True, exist_ok=True)
    table = pa.table(
        {
            "id": pa.array([1, 2, 3, 4]),
            "category": pa.array(["a", "b", "a", "c"]),
            "revenue": pa.array([10.0, 20.0, 30.0, 40.0]),
        }
    )
    pq.write_table(table, path)


def _body(**overrides):
    payload = {
        "dataset_ref": {"workspace_id": WS, "dataset_id": DS, "version_no": 1},
        "offset": 0,
        "limit": 100,
    }
    payload.update(overrides)
    return payload


def test_rows_query_requires_internal_token(tmp_path, monkeypatch):
    client = _client(tmp_path, monkeypatch)
    _write_parquet(tmp_path)
    response = client.post("/v1/datasets/rows/query", json=_body())
    assert response.status_code == 401
    assert response.json()["error"]["code"] == "INTERNAL_AUTH_REQUIRED"


def test_rows_query_filters_sorts_and_counts(tmp_path, monkeypatch):
    client = _client(tmp_path, monkeypatch)
    _write_parquet(tmp_path)
    response = client.post(
        "/v1/datasets/rows/query",
        headers={"X-Internal-Token": "secret"},
        json=_body(
            limit=1,
            sort=[{"column": "revenue", "direction": "desc"}],
            search="a",
        ),
    )
    assert response.status_code == 200
    body = response.json()
    assert [column["name"] for column in body["columns"]] == COLUMNS
    assert body["columns"][2]["type"] == "number"
    assert body["total"] == 2
    assert body["rows"] == [{"id": 3, "category": "a", "revenue": 30.0}]


def test_rows_query_rejects_unknown_column(tmp_path, monkeypatch):
    client = _client(tmp_path, monkeypatch)
    _write_parquet(tmp_path)
    response = client.post(
        "/v1/datasets/rows/query",
        headers={"X-Internal-Token": "secret"},
        json=_body(filters=[{"column": "id; DROP TABLE users", "operator": "eq", "value": 1}]),
    )
    assert response.status_code == 422
    assert response.json()["error"]["code"] == "EXPLORER_INVALID_COLUMN"


def test_rows_query_missing_parquet_is_not_ready(tmp_path, monkeypatch):
    client = _client(tmp_path, monkeypatch)
    response = client.post(
        "/v1/datasets/rows/query",
        headers={"X-Internal-Token": "secret"},
        json=_body(),
    )
    assert response.status_code == 409
    assert response.json()["error"]["code"] == "EXPLORER_NOT_READY"


def _write_large(tmp_path, rows=150):
    resolver = StorageResolver(tmp_path)
    path = resolver.parquet_path(WS, DS, 1)
    path.parent.mkdir(parents=True, exist_ok=True)
    table = pa.table(
        {
            "id": pa.array(list(range(rows))),
            "category": pa.array(["a" if index % 2 == 0 else "b" for index in range(rows)]),
            "revenue": pa.array([float(index) for index in range(rows)]),
        }
    )
    pq.write_table(table, path)


def _query(client, **overrides):
    return client.post(
        "/v1/datasets/rows/query",
        headers={"X-Internal-Token": "secret"},
        json=_body(**overrides),
    )


def test_rows_query_pages_exact_blocks(tmp_path, monkeypatch):
    client = _client(tmp_path, monkeypatch)
    _write_large(tmp_path)

    first = _query(client, offset=0, limit=100).json()
    assert len(first["rows"]) == 100
    assert first["total"] == 150
    assert first["rows"][0]["id"] == 0

    second = _query(client, offset=100, limit=100).json()
    assert len(second["rows"]) == 50
    assert second["rows"][0]["id"] == 100


def test_rows_query_sorts_ascending_and_descending(tmp_path, monkeypatch):
    client = _client(tmp_path, monkeypatch)
    _write_large(tmp_path)

    ascending = _query(client, limit=3, sort=[{"column": "revenue", "direction": "asc"}]).json()
    assert [row["id"] for row in ascending["rows"]] == [0, 1, 2]

    descending = _query(client, limit=3, sort=[{"column": "revenue", "direction": "desc"}]).json()
    assert [row["id"] for row in descending["rows"]] == [149, 148, 147]


def test_rows_query_filters_eq_and_gt(tmp_path, monkeypatch):
    client = _client(tmp_path, monkeypatch)
    _write_large(tmp_path)

    equals = _query(client, limit=1, filters=[{"column": "category", "operator": "eq", "value": "b"}]).json()
    assert equals["total"] == 75
    assert equals["rows"][0]["category"] == "b"

    greater = _query(client, limit=1, filters=[{"column": "id", "operator": "gt", "value": 145}]).json()
    assert greater["total"] == 4


def test_ensure_read_only_rejects_writes():
    from app.api.v1.explorer import _ensure_read_only

    with pytest.raises(ExplorerQueryError) as err:
        _ensure_read_only("DROP TABLE dataset_columns")
    assert err.value.code == "EXPLORER_READ_ONLY"