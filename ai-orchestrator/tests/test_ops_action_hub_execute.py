# ai-orchestrator/tests/test_ops_action_hub_execute.py
import os

os.environ["EXECUTION_FROZEN"] = "0"
from ops_action_hub import OpsActionHub


def test_execute_when_unfrozen(monkeypatch):
    import k8s_actions
    monkeypatch.setattr(k8s_actions, "execute", lambda *a, **kw: "ok")
    monkeypatch.setattr(k8s_actions, "current_resource_version", lambda *a, **kw: "1")
    hub = OpsActionHub()
    prop = hub.propose(run_id="r1", tenant_id="t1", cluster_id="c1", resource_id="exec-drill", namespace="observability", action_type="restart", parameters={}, expected_effect="restart", rca_status="confirmed")
    aid = prop["action_id"]
    hub.confirm(action_id=aid, requester="requester@corp")
    res = hub.execute(action_id=aid, execution_identity="exec@corp")
    assert res["status"] in ("success", "dry_run", "denied")
    assert res["execution_frozen"] is False


def test_execute_denied_when_frozen(monkeypatch):
    os.environ["EXECUTION_FROZEN"] = "1"
    hub = OpsActionHub()
    prop = hub.propose(run_id="r2", tenant_id="t1", cluster_id="c1", resource_id="exec-drill", namespace="observability", action_type="restart", parameters={}, expected_effect="restart", rca_status="confirmed")
    aid = prop["action_id"]
    hub.confirm(action_id=aid, requester="requester@corp")
    res = hub.execute(action_id=aid, execution_identity="exec@corp")
    assert res["status"] == "denied" and res["execution_frozen"] is True
