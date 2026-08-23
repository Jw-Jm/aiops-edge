# ai-orchestrator/tests/test_k8s_adapter.py
import datetime
from datetime import datetime as _dt, timezone as _tz

import pytest
from execution_adapter import AdapterRequest, ADAPTER_STATUSES
from execution_contract import ExecutionContract
from k8s_adapter import K8sAdapter


def _contract(actions=("rollout_restart",), resources=("observability",)):
    return ExecutionContract(
        contract_id="c1",
        plan_id="p1",
        intent_id="i1",
        run_id="r1",
        requested_by="requester@corp",
        allowed_tools=["k8s_adapter"],
        allowed_resources=list(resources),
        allowed_actions=list(actions),
        max_scope="resource",
        expire_time=_dt.now(_tz.utc).replace(year=2099),
        rollback_policy={"strategy": "rollback_restart"},
        approved_by="approver@corp",
        status="active",
    )


def test_dry_run_delegates_to_preflight(monkeypatch):
    import k8s_actions
    captured = {}
    monkeypatch.setattr(k8s_actions, "preflight", lambda action, kind, namespace, name, **kw: captured.update(
        dict(action=action, kind=kind, namespace=namespace, name=name)) or {"ok": True, "preflight_token": "t", "resource_version": "7", "command": "kubectl rollout restart", "category": "exec_write"}
    )
    adapter = K8sAdapter(adapter_id="k8s-1")
    req = AdapterRequest(contract_id="c1", credential_ref="cred://x", target={"kind": "deployment", "namespace": "observability", "resource_id": "exec-drill"}, action="rollout_restart")
    res = adapter.dry_run(req, _contract())
    assert res.status == "dry_run"
    assert captured["action"] == "rollout_restart" and captured["name"] == "exec-drill"


def test_execute_runs_real_action(monkeypatch):
    import k8s_actions
    ran = {}
    monkeypatch.setattr(k8s_actions, "execute", lambda action, kind, namespace, name, **kw: ran.update(
        dict(action=action, kind=kind, namespace=namespace, name=name)) or "restarted")
    monkeypatch.setattr(k8s_actions, "current_resource_version", lambda kind, namespace, name: "9")
    adapter = K8sAdapter(adapter_id="k8s-1")
    req = AdapterRequest(contract_id="c1", credential_ref="cred://x", target={"kind": "deployment", "namespace": "observability", "resource_id": "exec-drill"}, action="rollout_restart", idempotency_key="k1")
    res = adapter.execute(req, _contract())
    assert res.status == "success" and ran["name"] == "exec-drill"
    # R4.1 幂等：同 key 返回同一结果
    res2 = adapter.execute(req, _contract())
    assert res2.execution_trace_id == res.execution_trace_id


def test_forbidden_action_denied(monkeypatch):
    adapter = K8sAdapter(adapter_id="k8s-1")
    req = AdapterRequest(contract_id="c1", credential_ref="cred://x", target={"kind": "deployment", "namespace": "observability", "resource_id": "exec-drill"}, action="delete_pod")
    res = adapter.execute(req, _contract(actions=("rollout_restart",)))
    assert res.status == "denied"
