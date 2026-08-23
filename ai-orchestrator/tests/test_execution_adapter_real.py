# ai-orchestrator/tests/test_execution_adapter_real.py
from datetime import datetime as _dt, timezone as _tz

from execution_adapter import ExecutionAdapter, AdapterRequest
from execution_contract import ExecutionContract
from k8s_adapter import K8sAdapter


def _contract():
    return ExecutionContract(
        contract_id="c1", plan_id="p1", intent_id="i1", run_id="r1", requested_by="requester@corp",
        allowed_tools=["k8s_adapter"], allowed_resources=["observability"], allowed_actions=["rollout_restart"],
        max_scope="resource", expire_time=_dt.now(_tz.utc).replace(year=2099),
        rollback_policy={"strategy": "rollback_restart"}, approved_by="approver@corp", status="active",
    )


def test_real_delegation(monkeypatch):
    import k8s_actions
    ran = {}
    monkeypatch.setattr(k8s_actions, "execute", lambda action, kind, namespace, name, **kw: ran.update(dict(a=action, n=name)) or "ok")
    monkeypatch.setattr(k8s_actions, "current_resource_version", lambda kind, namespace, name: "1")
    real = K8sAdapter(adapter_id="k8s-1")
    adapter = ExecutionAdapter(adapter_id="mem-1", real_adapter=real)
    req = AdapterRequest(contract_id="c1", credential_ref="cred://x", target={"kind": "deployment", "namespace": "observability", "resource_id": "exec-drill"}, action="rollout_restart")
    res = adapter.execute(req, _contract())
    assert res.status == "success" and ran.get("n") == "exec-drill"
