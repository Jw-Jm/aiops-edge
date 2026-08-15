# tests/test_flow_testnode.py
import os
import tempfile
import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient
from flow_engine.store import FlowStore
from flow_api import router, set_flow_service
from flow_engine.usecase import WorkflowService
from flow_engine.noderegistry import node_registry

ADMIN = {"X-Internal-Token": "test-internal-token", "X-Internal-Role": "admin"}


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


def test_test_node_collect(client):
    r = client.post("/api/v1/ai/workflows/test-node",
                    json={"type": "collect", "config": {"service": "demo"},
                          "trigger": {"service": "demo"}},
                    headers=ADMIN)
    assert r.status_code == 200
    d = r.json()
    assert d["ok"] is True
    assert isinstance(d["output"], dict)


def test_test_node_trigger_manual(client):
    r = client.post("/api/v1/ai/workflows/test-node",
                    json={"type": "trigger.manual", "config": {},
                          "trigger": {"type": "manual", "payload": {"x": 1}}},
                    headers=ADMIN)
    assert r.status_code == 200
    d = r.json()
    assert d["ok"] is True
    assert d["output"].get("ok") is True


def test_test_node_unknown_type_400(client):
    r = client.post("/api/v1/ai/workflows/test-node",
                    json={"type": "nope", "config": {}}, headers=ADMIN)
    assert r.status_code == 400


def test_test_node_requires_admin(client):
    r = client.post("/api/v1/ai/workflows/test-node",
                    json={"type": "collect", "config": {}},
                    headers={"X-Internal-Token": "test-internal-token", "X-Internal-Role": "user"})
    assert r.status_code == 403
