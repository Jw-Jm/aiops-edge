"""P12 后端 Run API 端点测试（ai_runs_api：列表/详情/创建）。

边界：In-memory RunStateStore；不接真实 query-api/MySQL。
"""
import uuid

import pytest
from fastapi.testclient import TestClient

from ai_runs_api import router
from run_persistence import RunStateStore


@pytest.fixture
def client():
    from fastapi import FastAPI
    app = FastAPI()
    app.include_router(router)
    return TestClient(app)


def _create_run(store: RunStateStore, cluster=None, intent="diagnose"):
    return store.create_run(
        run_id=uuid.uuid4(),
        request_id=uuid.uuid4(),
        tenant_id=uuid.uuid4(),
        intent=intent,
        action_mode="read_only",
        principal_type="user",
        principal_id=uuid.uuid4(),
        primary_cluster_id=UUID(cluster) if cluster else None,
    )


from uuid import UUID


def test_list_runs_returns_created_runs(client):
    """P12：GET /api/v1/ai/runs 返回已创建 Run 列表。"""
    from main import _run_state_store
    _run_state_store._runs.clear()
    r = _create_run(_run_state_store)
    resp = client.get("/api/v1/ai/runs")
    assert resp.status_code == 200
    body = resp.json()
    assert "runs" in body
    assert any(x["run_id"] == str(r.run_id) for x in body["runs"])
    item = next(x for x in body["runs"] if x["run_id"] == str(r.run_id))
    assert item["intent"] == "diagnose"
    assert "created_at" in item


def test_get_run_detail(client):
    """P12：GET /api/v1/ai/runs/{id} 返回 Run 详情。"""
    from main import _run_state_store
    _run_state_store._runs.clear()
    r = _create_run(_run_state_store)
    resp = client.get(f"/api/v1/ai/runs/{r.run_id}")
    assert resp.status_code == 200
    body = resp.json()["run"]
    assert body["run_id"] == str(r.run_id)
    assert "state_version" in body


def test_get_run_not_found(client):
    """P12：不存在的 run_id → 404。"""
    resp = client.get(f"/api/v1/ai/runs/{uuid.uuid4()}")
    assert resp.status_code == 404


def test_create_run_endpoint_moved_to_query_api(client):
    """P10 完整闭环：POST /api/v1/ai/runs 已迁移到 query-api，返回 410 不留第二入口。"""
    from main import _run_state_store
    _run_state_store._runs.clear()
    tenant = str(uuid.uuid4())
    resp = client.post("/api/v1/ai/runs", json={
        "tenant_id": tenant,
        "cluster_id": str(uuid.uuid4()),
        "intent": "diagnose",
        "action_mode": "read_only",
    })
    assert resp.status_code == 410
    assert resp.json()["detail"] == "RUN_CREATION_MOVED_TO_QUERY_API"
