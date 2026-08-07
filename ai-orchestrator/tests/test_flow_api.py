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
    node_registry.reset()
    svc = WorkflowService(FlowStore(os.path.join(tmp, "flows.db")))
    set_flow_service(svc)
    app = FastAPI()
    app.include_router(router)
    return TestClient(app)

def _chain_graph():
    return {"nodes": [
        {"id": "c", "type": "collect", "name": "采集", "config": {"service": "demo"}, "position": {"x": 0, "y": 0}},
        {"id": "s", "type": "summarize", "name": "汇总", "config": {}, "position": {"x": 0, "y": 100}},
    ], "edges": [{"id": "e1", "source": "c", "sourcePort": "next", "target": "s"}]}

def test_node_types_endpoint(client):
    r = client.get("/api/v1/ai/flows/node-types")
    assert r.status_code == 200
    assert any(t["type"] == "condition" for t in r.json()["node_types"])

def test_crud_flow(client):
    r = client.post("/api/v1/ai/flows", json={"name": "f", "description": "d", "graph": _chain_graph()})
    assert r.status_code == 201
    fid = r.json()["id"]
    r2 = client.get(f"/api/v1/ai/flows/{fid}")
    assert r2.status_code == 200 and r2.json()["name"] == "f"
    r3 = client.put(f"/api/v1/ai/flows/{fid}", json={"name": "f2"})
    assert r3.json()["name"] == "f2"
    r4 = client.delete(f"/api/v1/ai/flows/{fid}")
    assert r4.status_code == 200

def test_run_flow(client):
    r = client.post("/api/v1/ai/flows", json={"name": "f", "description": "d", "graph": _chain_graph()})
    fid = r.json()["id"]
    rr = client.post(f"/api/v1/ai/flows/{fid}/run", json={"trigger": {"service": "demo"}})
    assert rr.status_code == 200
    run_id = rr.json()["run_id"]
    dr = client.get(f"/api/v1/ai/flows/{fid}/runs/{run_id}")
    assert dr.status_code == 200 and dr.json()["run"]["status"] == "succeeded"
