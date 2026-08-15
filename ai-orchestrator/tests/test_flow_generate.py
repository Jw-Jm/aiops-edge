# tests/test_flow_generate.py
import json
import os
import tempfile
import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient
from flow_engine.store import FlowStore
from flow_api import router, set_flow_service
from flow_engine.usecase import WorkflowService
from flow_engine.noderegistry import node_registry
import flow_api

ADMIN = {"X-Internal-Token": "test-internal-token", "X-Internal-Role": "admin"}


@pytest.fixture()
def client(monkeypatch):
    tmp = tempfile.mkdtemp()
    os.environ["INTERNAL_TOKEN"] = "test-internal-token"
    node_registry.reset()
    svc = WorkflowService(FlowStore(os.path.join(tmp, "flows.db")))
    set_flow_service(svc)
    app = FastAPI()
    app.include_router(router)
    return TestClient(app), monkeypatch


def _mock_llm(monkeypatch, raw):
    class FakeLLM:
        def chat(self, system, user):
            return raw
    monkeypatch.setattr(flow_api, "_resolve_llm", lambda: FakeLLM())


def _valid_graph():
    return {"name": "测试流程", "description": "d", "graph": {
        "nodes": [{"id": "n1", "type": "trigger.manual", "name": "手动触发",
                   "config": {}, "position": {"x": 0, "y": 0}}],
        "edges": []}}


def test_generate_strips_fences_and_trailing_comment(client):
    tc, mp = client
    raw = '```json\n' + json.dumps(_valid_graph(), ensure_ascii=False) + '\n```\n# 尾部注释'
    _mock_llm(mp, raw)
    r = tc.post("/api/v1/ai/workflows/generate", json={"prompt": "生成一个工作流"}, headers=ADMIN)
    assert r.status_code == 200
    d = r.json()
    assert d["name"] == "测试流程"
    assert d["graph"]["nodes"][0]["type"] == "trigger.manual"


def test_generate_requires_admin(client):
    tc, mp = client
    _mock_llm(mp, json.dumps(_valid_graph()))
    r = tc.post("/api/v1/ai/workflows/generate", json={"prompt": "x"},
                headers={"X-Internal-Token": "test-internal-token", "X-Internal-Role": "user"})
    assert r.status_code == 403


def test_generate_invalid_graph_cycle_400(client):
    tc, mp = client
    graph = {"name": "环", "description": "", "graph": {
        "nodes": [{"id": "n1", "type": "trigger.manual", "name": "t", "config": {}, "position": {}},
                  {"id": "n2", "type": "collect", "name": "c", "config": {}, "position": {}}],
        "edges": [{"id": "e1", "source": "n1", "sourcePort": "next", "target": "n2"},
                  {"id": "e2", "source": "n2", "sourcePort": "next", "target": "n1"}]}}
    _mock_llm(mp, json.dumps(graph))
    r = tc.post("/api/v1/ai/workflows/generate", json={"prompt": "x"}, headers=ADMIN)
    assert r.status_code == 400


def test_generate_bad_json_400(client):
    tc, mp = client
    _mock_llm(mp, "这不是 JSON")
    r = tc.post("/api/v1/ai/workflows/generate", json={"prompt": "x"}, headers=ADMIN)
    assert r.status_code == 400
