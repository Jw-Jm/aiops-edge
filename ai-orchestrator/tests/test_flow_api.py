import tempfile, os
import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient
from flow_engine.store import FlowStore
from flow_api import router, set_flow_service
from flow_engine.usecase import WorkflowService
from flow_engine.noderegistry import node_registry

# P1-A1: FlowEditor 执行端点（run/resume）随 legacy flow runtime 删除，流程执行
# 迁移到 canonical Investigation Run。本文件仅保留 workflow 定义 CRUD 契约测试
# （执行引擎 run 逻辑由 flow_engine 自身单元测试覆盖）。

@pytest.fixture()
def client():
    tmp = tempfile.mkdtemp()
    os.environ["INTERNAL_TOKEN"] = "test-internal-token"
    node_registry.reset()
    svc = WorkflowService(FlowStore(os.path.join(tmp, "flows.db")))
    set_flow_service(svc)
    app = FastAPI()
    app.include_router(router)
    return TestClient(app)

# P0-3: 工作流定义变更（create/update/delete/toggle）仅限 admin，测试须带内部 token + admin 角色
ADMIN = {"X-Internal-Token": "test-internal-token", "X-Internal-Role": "admin"}

def _chain_graph():
    return {"nodes": [
        {"id": "c", "type": "collect", "name": "采集", "config": {"service": "demo"}, "position": {"x": 0, "y": 0}},
        {"id": "s", "type": "summarize", "name": "汇总", "config": {}, "position": {"x": 0, "y": 100}},
    ], "edges": [{"id": "e1", "source": "c", "sourcePort": "next", "target": "s"}]}

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


def test_flow_run_endpoint_removed_after_legacy_runtime_deletion(client):
    """P1-A1: legacy flow run 端点物理删除 → 404（不可再通过 HTTP 执行 legacy flow）。"""
    created = client.post("/api/v1/ai/workflows", json={"name": "f", "description": "d", "graph": _chain_graph()}, headers=ADMIN)
    fid = created.json()["id"]
    assert client.post(f"/api/v1/ai/workflows/{fid}/run", json={}).status_code == 404
    assert client.post(f"/api/v1/ai/workflows/{fid}/runs/x/resume", json={"approved": True}).status_code == 404
