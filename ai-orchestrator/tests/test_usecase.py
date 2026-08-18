import tempfile, os, re
from flow_engine.store import FlowStore
from flow_engine.usecase import WorkflowService
from flow_engine.nodes_aiops import register_aiops_nodes
from flow_engine.noderegistry import node_registry

def _svc():
    tmp = tempfile.mkdtemp()
    node_registry.reset()
    register_aiops_nodes()
    return WorkflowService(FlowStore(os.path.join(tmp, "flows.db")))

def _chain_graph():
    return {"nodes": [
        {"id": "c", "type": "collect", "name": "采集", "config": {"service": "demo"}, "position": {"x": 0, "y": 0}},
        {"id": "s", "type": "summarize", "name": "汇总", "config": {}, "position": {"x": 0, "y": 100}},
    ], "edges": [{"id": "e1", "source": "c", "sourcePort": "next", "target": "s"}]}

def test_node_types_include_all():
    svc = _svc()
    types = {t["type"] for t in svc.node_types()}
    assert "collect" in types and "condition" in types and "wait_approval" in types

def test_create_update_get_flow():
    svc = _svc()
    f = svc.create_flow("我的流程", "desc", _chain_graph())
    fid = f["id"]
    got = svc.get_flow(fid)
    assert got["name"] == "我的流程" and len(got["graph"]["nodes"]) == 2

def test_run_chain_succeeds():
    svc = _svc()
    f = svc.create_flow("流程", "", _chain_graph())
    run_id = f"run_{f['id']}"
    res = svc.run_flow(f["id"], {"service": "demo"}, run_id)
    assert res.status == "succeeded"
    assert "s" in res.context.nodes


def test_automatic_run_uses_full_uuid_run_id():
    svc = _svc()
    flow = svc.create_flow("流程", "", _chain_graph())

    result = svc.run_flow(flow["id"], {"type": "cron"})

    assert result.run is not None
    assert re.fullmatch(r"run_[0-9a-f]{32}", result.run["run_id"])

def test_run_approval_pauses_and_resume():
    svc = _svc()
    g = {"nodes": [
        {"id": "w", "type": "wait_approval", "name": "审批", "config": {}, "position": {"x": 0, "y": 0}},
        {"id": "e", "type": "execute", "name": "执行", "config": {}, "position": {"x": 0, "y": 100}},
    ], "edges": [{"id": "e1", "source": "w", "sourcePort": "approved", "target": "e"}]}
    f = svc.create_flow("审批流", "", g)
    run_id = f"run_{f['id']}"
    res = svc.run_flow(f["id"], {}, run_id)
    assert res.status == "waiting_approval"
    # resume
    res2 = svc.resume_run(run_id, approved=True)
    assert res2.status == "succeeded"
