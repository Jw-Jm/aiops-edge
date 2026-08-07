# tests/test_store.py
import tempfile, os
from flow_engine.store import FlowStore
from flow_engine.graph import Graph, GraphNode, GraphEdge, graph_from_dict

def _store():
    tmp = tempfile.mkdtemp()
    return FlowStore(os.path.join(tmp, "flows.db"))

def test_save_and_get_flow():
    s = _store()
    fid = s.save_flow({"name": "f1", "description": "", "enabled": True,
                       "graph_json": '{"nodes": [{"id": "n1", "type": "a"}], "edges": []}'})
    f = s.get_flow(fid)
    assert f["name"] == "f1" and f["version"] == 1

def test_save_increments_version():
    s = _store()
    fid = s.save_flow({"name": "f1", "graph_json": "{}"})
    s.save_flow({"id": fid, "name": "f2", "graph_json": "{}"})
    assert s.get_flow(fid)["version"] == 2
    assert s.get_flow(fid)["name"] == "f2"

def test_delete_flow():
    s = _store()
    fid = s.save_flow({"name": "f", "graph_json": "{}"})
    assert s.delete_flow(fid) is True
    assert s.get_flow(fid) is None

def test_run_lifecycle():
    s = _store()
    rid = s.create_run("flow_1", 1, "manual", "{}")
    s.update_run_status(rid, "running")
    s.save_run_node(rid, "n1", "a", "done", "{}", '{"out":1}', "next", "")
    s.update_run_status(rid, "succeeded", context_json='{"trigger":{}}')
    run = s.get_run(rid)
    assert run["status"] == "succeeded"
    nodes = s.get_run_nodes(rid)
    assert len(nodes) == 1 and nodes[0]["node_type"] == "a"
