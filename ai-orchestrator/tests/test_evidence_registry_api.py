"""Evidence Registry + Evidence Detail API 测试（tenant+cluster+run 三元授权）。

范围：
- evidence_registry：内存注册表（MVP，重启即失）的注册/列举/三元授权语义。
- ai_runs_api 新增路由：
  - GET /api/v1/ai/runs/{run_id}/evidences?tenant_id=&cluster_id=
  - GET /api/v1/ai/runs/{run_id}/evidences/{evidence_id}?tenant_id=&cluster_id=

边界：In-memory；不接真实 query-api/MySQL；只读（不执行、不变更证据）。
"""
import os
import tempfile

# 本机环境 /var/lib/aiops 不可写：main（经 rag）在 import 时创建数据目录，
# 必须在任何 main/orchestrator 导入前重定向到临时目录。
os.environ.setdefault("AIOPS_DATA_DIR", tempfile.mkdtemp(prefix="aiops-evidence-test-"))

import uuid

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

import evidence_registry
from ai_runs_api import router
from evidence_registry import EvidenceRegistry
from run_persistence import RunStateStore


# ── Registry 单元语义 ────────────────────────────────────────────

def test_register_and_list_roundtrip_and_stable_generated_id():
    """注册→列举 roundtrip；重复注册生成的 evidence_id 稳定。"""
    reg = EvidenceRegistry()
    entries = [{"layer": "服务拓扑", "finding": "异常沿调用链传播"}]
    reg.register_run("run-1", "tenant-a", "cluster-x", entries)
    out = reg.list_evidences("run-1")
    assert len(out) == 1
    eid = out[0]["evidence_id"]
    assert isinstance(eid, str) and len(eid) == 32
    # 列举不暴露内部 scope 字段
    assert "tenant_id" not in out[0]
    assert "cluster_id" not in out[0]

    # 重复注册相同内容 → 生成的 evidence_id 稳定（确定性哈希）
    reg.register_run("run-1", "tenant-a", "cluster-x", [
        {"layer": "服务拓扑", "finding": "异常沿调用链传播"},
    ])
    again = reg.list_evidences("run-1")
    assert len(again) == 1
    assert again[0]["evidence_id"] == eid


def test_register_preserves_existing_evidence_id():
    """已有 evidence_id 的条目保留原值。"""
    reg = EvidenceRegistry()
    reg.register_run("run-2", "t", "c", [{"evidence_id": "fixed-id", "fact": "x"}])
    assert reg.list_evidences("run-2")[0]["evidence_id"] == "fixed-id"


def test_authorize_and_get_happy_path():
    reg = EvidenceRegistry()
    reg.register_run("run-3", "tenant-a", "cluster-x", [{"layer": "指标分析", "finding": "确认根因"}])
    eid = reg.list_evidences("run-3")[0]["evidence_id"]
    ev = reg.authorize_and_get("run-3", eid, "tenant-a", "cluster-x")
    assert ev["evidence_id"] == eid
    assert ev["finding"] == "确认根因"


def test_wrong_tenant_or_cluster_denied():
    reg = EvidenceRegistry()
    reg.register_run("run-4", "tenant-a", "cluster-x", [{"finding": "f"}])
    eid = reg.list_evidences("run-4")[0]["evidence_id"]
    with pytest.raises(PermissionError):
        reg.authorize_and_get("run-4", eid, "tenant-b", "cluster-x")
    with pytest.raises(PermissionError):
        reg.authorize_and_get("run-4", eid, "tenant-a", "cluster-y")


def test_unknown_run_or_evidence_raises_lookup_error():
    reg = EvidenceRegistry()
    with pytest.raises(LookupError):
        reg.authorize_and_get("no-such-run", "any-id", "t", "c")
    reg.register_run("run-5", "t", "c", [{"finding": "f"}])
    with pytest.raises(LookupError):
        reg.authorize_and_get("run-5", "no-such-evidence", "t", "c")


def test_get_evidence_without_scope_check():
    reg = EvidenceRegistry()
    reg.register_run("run-6", "t", "c", [{"finding": "f"}])
    eid = reg.list_evidences("run-6")[0]["evidence_id"]
    assert reg.get_evidence("run-6", eid)["finding"] == "f"
    assert reg.get_evidence("run-6", "missing") is None


def test_module_singleton_accessor():
    assert evidence_registry.get_registry() is evidence_registry.get_registry()


# ── 路由级测试（TestClient + 注入的 RunStateStore）──────────────

@pytest.fixture
def client():
    app = FastAPI()
    app.include_router(router)
    return TestClient(app)


def _create_run(store: RunStateStore, tenant: uuid.UUID, cluster: uuid.UUID):
    return store.create_run(
        run_id=uuid.uuid4(),
        request_id=uuid.uuid4(),
        tenant_id=tenant,
        intent="diagnose",
        action_mode="read_only",
        principal_type="user",
        principal_id=uuid.uuid4(),
        primary_cluster_id=cluster,
    )


@pytest.fixture
def scoped_run(monkeypatch):
    """清空全局 store 与 registry，创建一个带 tenant/cluster 的 Run 并注册证据。"""
    from main import _run_state_store
    _run_state_store._runs.clear()
    evidence_registry._registry = EvidenceRegistry()

    tenant, cluster = uuid.uuid4(), uuid.uuid4()
    run = _create_run(_run_state_store, tenant, cluster)
    evidence_registry.get_registry().register_run(
        str(run.run_id), str(tenant), str(cluster),
        [{"layer": "服务拓扑", "finding": "异常沿调用链传播, 2 个服务受影响"}],
    )
    return {"run_id": str(run.run_id), "tenant_id": str(tenant), "cluster_id": str(cluster)}


def test_route_list_evidences_ok(client, scoped_run):
    s = scoped_run
    resp = client.get(
        f"/api/v1/ai/runs/{s['run_id']}/evidences",
        params={"tenant_id": s["tenant_id"], "cluster_id": s["cluster_id"]},
    )
    assert resp.status_code == 200
    body = resp.json()
    assert body["run_id"] == s["run_id"]
    assert body["count"] == 1
    assert body["evidences"][0]["finding"].startswith("异常沿调用链传播")


def test_route_get_single_evidence_ok(client, scoped_run):
    s = scoped_run
    eid = client.get(
        f"/api/v1/ai/runs/{s['run_id']}/evidences",
        params={"tenant_id": s["tenant_id"], "cluster_id": s["cluster_id"]},
    ).json()["evidences"][0]["evidence_id"]
    resp = client.get(
        f"/api/v1/ai/runs/{s['run_id']}/evidences/{eid}",
        params={"tenant_id": s["tenant_id"], "cluster_id": s["cluster_id"]},
    )
    assert resp.status_code == 200
    assert resp.json()["evidence"]["evidence_id"] == eid


def test_route_scope_mismatch_403(client, scoped_run):
    s = scoped_run
    # 错误租户
    resp = client.get(
        f"/api/v1/ai/runs/{s['run_id']}/evidences",
        params={"tenant_id": str(uuid.uuid4()), "cluster_id": s["cluster_id"]},
    )
    assert resp.status_code == 403
    # 错误集群
    resp = client.get(
        f"/api/v1/ai/runs/{s['run_id']}/evidences",
        params={"tenant_id": s["tenant_id"], "cluster_id": str(uuid.uuid4())},
    )
    assert resp.status_code == 403
    # 缺参 fail-closed
    resp = client.get(f"/api/v1/ai/runs/{s['run_id']}/evidences")
    assert resp.status_code == 403


def test_route_unknown_run_404(client):
    resp = client.get(
        f"/api/v1/ai/runs/{uuid.uuid4()}/evidences",
        params={"tenant_id": "t", "cluster_id": "c"},
    )
    assert resp.status_code == 404


def test_route_unknown_evidence_404(client, scoped_run):
    s = scoped_run
    resp = client.get(
        f"/api/v1/ai/runs/{s['run_id']}/evidences/no-such-evidence",
        params={"tenant_id": s["tenant_id"], "cluster_id": s["cluster_id"]},
    )
    assert resp.status_code == 404


def test_route_single_evidence_scope_mismatch_403(client, scoped_run):
    s = scoped_run
    eid = client.get(
        f"/api/v1/ai/runs/{s['run_id']}/evidences",
        params={"tenant_id": s["tenant_id"], "cluster_id": s["cluster_id"]},
    ).json()["evidences"][0]["evidence_id"]
    resp = client.get(
        f"/api/v1/ai/runs/{s['run_id']}/evidences/{eid}",
        params={"tenant_id": str(uuid.uuid4()), "cluster_id": s["cluster_id"]},
    )
    assert resp.status_code == 403
