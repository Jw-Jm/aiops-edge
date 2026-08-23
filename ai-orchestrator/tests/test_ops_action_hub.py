"""P11 只读接线 TDD — OpsActionHub + router"""
import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

import ops_action_api
import ops_action_hub


def _fresh_app():
    # fresh hub for isolation
    hub = ops_action_hub.OpsActionHub()
    ops_action_api._hub = hub
    app = FastAPI()
    app.include_router(ops_action_api.router)
    client = TestClient(app)
    return client, hub


def _propose_payload(overrides=None):
    base = {
        "run_id": "run-1",
        "tenant_id": "t-1",
        "cluster_id": "c-1",
        "resource_id": "svc/checkout",
        "namespace": "default",
        "action_type": "patch_resource",
        "parameters": {"replicas": 3},
        "expected_effect": "scale to 3",
        "verification_policy": "manual_check",
        "root_cause_confidence": 0.9,
        "resource_version": "0",
        "rca_status": "completed",
        "blast_radius": "single_resource",
        "environment": "production",
        "llm_risk_suggestion": "R0",
    }
    if overrides:
        base.update(overrides)
    return base


def test_propose_happy_path():
    client, hub = _fresh_app()
    resp = client.post("/api/v1/ops/actions/propose", json=_propose_payload())
    assert resp.status_code == 200, resp.text
    data = resp.json()["action"]
    assert data["action_hash"]
    # production env ⇒ risk ≥R2
    assert data["risk"] in ("R2", "R3", "R4")
    assert data["execution_frozen"] is True


def test_propose_rca_not_ready():
    client, _ = _fresh_app()
    resp = client.post("/api/v1/ops/actions/propose", json=_propose_payload({"rca_status": "pending"}))
    assert resp.status_code == 400
    assert "RCA_NOT_READY" in resp.text


def test_propose_invalid_action_type():
    client, _ = _fresh_app()
    resp = client.post("/api/v1/ops/actions/propose", json=_propose_payload({"action_type": "drop_table"}))
    assert resp.status_code == 400


def test_propose_restricted_shell_risk_R4():
    client, _ = _fresh_app()
    resp = client.post("/api/v1/ops/actions/propose", json=_propose_payload({"action_type": "restricted_shell"}))
    assert resp.status_code == 200
    data = resp.json()["action"]
    assert data["risk"] == "R4"
    assert data["planner_selectable"] is False


def test_low_confidence_raises_risk():
    client, _ = _fresh_app()
    resp = client.post("/api/v1/ops/actions/propose", json=_propose_payload({"root_cause_confidence": 0.3}))
    assert resp.status_code == 200
    assert resp.json()["action"]["risk"] in ("R3", "R4")


def test_list_and_filter_by_run_id():
    client, _ = _fresh_app()
    client.post("/api/v1/ops/actions/propose", json=_propose_payload({"run_id": "run-a"}))
    client.post("/api/v1/ops/actions/propose", json=_propose_payload({"run_id": "run-b"}))
    resp = client.get("/api/v1/ops/actions")
    assert resp.status_code == 200
    assert len(resp.json()["actions"]) == 2
    assert resp.json()["execution_frozen"] is True
    resp2 = client.get("/api/v1/ops/actions", params={"run_id": "run-a"})
    assert len(resp2.json()["actions"]) == 1
    assert resp2.json()["actions"][0]["run_id"] == "run-a"


def test_get_unknown_404():
    client, _ = _fresh_app()
    resp = client.get("/api/v1/ops/actions/unknown-id")
    assert resp.status_code == 404


def test_confirm_known_and_double_confirm():
    client, _ = _fresh_app()
    r = client.post("/api/v1/ops/actions/propose", json=_propose_payload())
    aid = r.json()["action"]["action_id"]
    resp = client.post(f"/api/v1/ops/actions/{aid}/confirm", json={"requester": "alice"})
    assert resp.status_code == 200
    assert resp.json()["status"] == "confirmed"
    assert resp.json()["execution_frozen"] is True
    assert resp.json()["confirmation_id"]
    # double confirm allowed
    resp2 = client.post(f"/api/v1/ops/actions/{aid}/confirm", json={"requester": "alice"})
    assert resp2.status_code == 200


def test_confirm_unknown_404():
    client, _ = _fresh_app()
    resp = client.post("/api/v1/ops/actions/unknown-id/confirm", json={"requester": "bob"})
    assert resp.status_code == 404


def test_hub_execute_is_fail_closed_by_default():
    """Execution Production Gate：hub 暴露 execute 入口，但默认冻结时 fail-closed。

    设计（c70ddf3）：OpsActionHub.execute 仅在 EXECUTION_FROZEN=0 时委托
    ExecutionAdapter → K8sAdapter 真实执行；默认冻结返回 denied，绝不触发真实变更。
    """
    import os

    os.environ.pop("EXECUTION_FROZEN", None)  # 默认冻结
    hub = ops_action_hub.OpsActionHub()
    assert any(n.lower().startswith("execute") for n in dir(hub)), "hub 必须暴露 execute 入口"
    # 无 action 时直接抛 ActionNotFoundError（更早的 fail-closed，不触真实系统）
    with pytest.raises(ops_action_hub.ActionNotFoundError):
        hub.execute(action_id="missing", execution_identity="x")


def test_api_list_execution_frozen_top_level():
    client, _ = _fresh_app()
    resp = client.get("/api/v1/ops/actions")
    assert resp.status_code == 200
    assert resp.json()["execution_frozen"] is True
