import tempfile, os
import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient
from flow_engine.store import FlowStore
from flow_api import router, set_flow_service
from flow_engine.usecase import WorkflowService
from flow_engine.noderegistry import node_registry

@pytest.fixture()
def client():
    tmp = tempfile.mkdtemp()
    os.environ["INTERNAL_TOKEN"] = "test-internal-token"
    os.environ["LEGACY_FLOW_RUNTIME_ENABLED"] = "1"  # explicit test opt-in; production defaults off
    node_registry.reset()
    svc = WorkflowService(FlowStore(os.path.join(tmp, "flows.db")))
    set_flow_service(svc)
    app = FastAPI()
    app.include_router(router)
    return TestClient(app)


def test_legacy_flow_runtime_is_disabled_without_explicit_opt_in(client, monkeypatch):
    monkeypatch.delenv("LEGACY_FLOW_RUNTIME_ENABLED", raising=False)
    r = client.post("/api/v1/ai/workflows/missing/run", json={})
    assert r.status_code == 410

# P0-3: 工作流定义变更（create/update/delete/toggle）仅限 admin，测试须带内部 token + admin 角色
ADMIN = {"X-Internal-Token": "test-internal-token", "X-Internal-Role": "admin"}

def _chain_graph():
    return {"nodes": [
        {"id": "c", "type": "collect", "name": "采集", "config": {"service": "demo"}, "position": {"x": 0, "y": 0}},
        {"id": "s", "type": "summarize", "name": "汇总", "config": {}, "position": {"x": 0, "y": 100}},
    ], "edges": [{"id": "e1", "source": "c", "sourcePort": "next", "target": "s"}]}


def _approval_graph():
    return {"nodes": [
        {"id": "w", "type": "wait_approval", "name": "审批", "config": {}, "position": {"x": 0, "y": 0}},
        {"id": "e", "type": "execute", "name": "执行", "config": {}, "position": {"x": 0, "y": 100}},
    ], "edges": [{"id": "e1", "source": "w", "sourcePort": "approved", "target": "e"}]}

def test_node_types_endpoint(client):
    r = client.get("/api/v1/ai/workflows/node-types")
    assert r.status_code == 200
    assert any(t["type"] == "condition" for t in r.json()["node_types"])

def test_crud_flow(client):
    r = client.post("/api/v1/ai/workflows", json={"name": "f", "description": "d", "graph": _chain_graph()}, headers=ADMIN)
    assert r.status_code == 201
    fid = r.json()["id"]
    r2 = client.get(f"/api/v1/ai/workflows/{fid}")
    assert r2.status_code == 200 and r2.json()["name"] == "f"
    r3 = client.put(f"/api/v1/ai/workflows/{fid}", json={"name": "f2"}, headers=ADMIN)
    assert r3.json()["name"] == "f2"
    r4 = client.delete(f"/api/v1/ai/workflows/{fid}", headers=ADMIN)
    assert r4.status_code == 200


def test_crud_flow_requires_admin(client):
    # P0-3: 非 admin 创建工作流必须 403
    r = client.post("/api/v1/ai/workflows", json={"name": "f", "description": "d", "graph": _chain_graph()},
                    headers={"X-Internal-Token": "test-internal-token", "X-Internal-Role": "user"})
    assert r.status_code == 403


def test_run_flow(client):
    r = client.post("/api/v1/ai/workflows", json={"name": "f", "description": "d", "graph": _chain_graph()}, headers=ADMIN)
    fid = r.json()["id"]
    rr = client.post(f"/api/v1/ai/workflows/{fid}/run", json={"trigger": {"service": "demo"}})
    assert rr.status_code == 200
    run_id = rr.json()["run_id"]
    dr = client.get(f"/api/v1/ai/workflows/{fid}/runs/{run_id}")
    assert dr.status_code == 200 and dr.json()["run"]["status"] == "succeeded"


def test_run_flow_returns_the_persisted_terminal_record(client):
    created = client.post(
        "/api/v1/ai/workflows",
        json={"name": "f", "description": "d", "graph": _chain_graph()},
        headers=ADMIN,
    )
    fid = created.json()["id"]

    response = client.post(f"/api/v1/ai/workflows/{fid}/run", json={"trigger": {"service": "demo"}})

    assert response.status_code == 200
    payload = response.json()
    run = payload["run"]
    assert run["run_id"] == payload["run_id"]
    assert run["flow_id"] == fid
    assert run["status"] == "succeeded"
    assert [node["node_id"] for node in run["nodes"]] == ["c", "s"]


def test_repeated_identical_runs_create_distinct_terminal_records(client):
    created = client.post(
        "/api/v1/ai/workflows",
        json={"name": "f", "description": "d", "graph": _chain_graph()},
        headers=ADMIN,
    )
    fid = created.json()["id"]

    first = client.post(f"/api/v1/ai/workflows/{fid}/run", json={"trigger": {"service": "demo"}})
    second = client.post(f"/api/v1/ai/workflows/{fid}/run", json={"trigger": {"service": "demo"}})

    assert first.status_code == 200
    assert second.status_code == 200
    assert first.json()["run_id"] != second.json()["run_id"]
    assert [run["status"] for run in client.get(f"/api/v1/ai/workflows/{fid}/runs").json()["runs"]] == ["succeeded", "succeeded"]


def test_approved_resume_returns_persisted_terminal_record(client):
    created = client.post(
        "/api/v1/ai/workflows",
        json={"name": "approval", "description": "", "graph": _approval_graph()},
        headers=ADMIN,
    )
    fid = created.json()["id"]
    started = client.post(f"/api/v1/ai/workflows/{fid}/run", json={"trigger": {}})
    assert started.json()["status"] == "waiting_approval"

    resumed = client.post(
        f"/api/v1/ai/workflows/{fid}/runs/{started.json()['run_id']}/resume",
        json={"approved": True},
        headers=ADMIN,
    )

    assert resumed.status_code == 200
    run = resumed.json()["run"]
    assert run["run_id"] == started.json()["run_id"]
    assert run["status"] == "succeeded"
    assert {node["node_id"] for node in run["nodes"]} == {"w", "e"}


def test_resume_requires_approver(client):
    # P0-3: resume approved=True 必须 admin/审批人；普通用户 403
    r = client.post("/api/v1/ai/workflows", json={"name": "f", "description": "d", "graph": _chain_graph()}, headers=ADMIN)
    fid = r.json()["id"]
    rr = client.post(f"/api/v1/ai/workflows/{fid}/run", json={"trigger": {"service": "demo"}})
    run_id = rr.json()["run_id"]
    resp = client.post(f"/api/v1/ai/workflows/{fid}/runs/{run_id}/resume", json={"approved": True},
                       headers={"X-Internal-Token": "test-internal-token", "X-Internal-Role": "user"})
    assert resp.status_code == 403
