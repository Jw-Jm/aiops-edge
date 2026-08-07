# tests/test_engine.py
import time
from flow_engine.noderegistry import NodeSpec, register_node, node_registry
from flow_engine.graph import Graph, GraphNode, GraphEdge
from flow_engine.engine import Engine, RunStatus

def _setup():
    node_registry.reset()
    register_node(NodeSpec(type="start", kind="action", category="t", label="s",
                           ports=["next"], execute=lambda ctx, config: {"out": config.get("v", 0)}))
    register_node(NodeSpec(type="double", kind="action", category="t", label="d",
                           ports=["next"],
                           execute=lambda ctx, config: {"out": ctx.nodes.get("n1", {}).get("output", {}).get("out", 0) * 2}))
    register_node(NodeSpec(type="condition", kind="control", category="t", label="c",
                           ports=["true", "false"], execute=lambda ctx, config: {}))
    register_node(NodeSpec(type="wait_approval", kind="control", category="t", label="a",
                           ports=["approved", "rejected"], execute=lambda ctx, config: {}))
    # condition/wait_approval 的实际端口由引擎根据 expr/resume 决定，execute 返回值仅写 output

def _g(nodes, edges):
    return Graph(nodes=[GraphNode(id=i, type=t) for i, t in nodes],
                 edges=[GraphEdge(id=f"e{idx}", source=s, source_port=p, target=t) for idx, (s, p, t) in enumerate(edges)])

def test_sequential_chain():
    _setup()
    g = _g([("n1", "start"), ("n2", "double")],
           [("n1", "next", "n2")])
    res = Engine().execute(g, {"service": "x"}, graph_config={"n1": {"v": 21}})
    assert res.status == "succeeded"
    assert res.context.nodes["n2"]["output"]["out"] == 42

def test_error_port_marks_failed_when_no_error_edge():
    _setup()
    node_registry.register(NodeSpec(type="boom", kind="action", category="t", label="b",
                                    ports=["next"], execute=lambda ctx, config: (_ for _ in ()).throw(RuntimeError("x"))))
    g = _g([("n1", "boom")], [])
    res = Engine().execute(g, {})
    assert res.status == "failed"
    assert "x" in res.error

def test_condition_routes_true():
    _setup()
    g = _g([("n1", "start"), ("n2", "condition"), ("nt", "start"), ("nf", "start")],
           [("n1", "next", "n2"), ("n2", "true", "nt"), ("n2", "false", "nf")])
    res = Engine().execute(g, {}, graph_config={"n1": {"v": 1}, "n2": {"expr": "{{nodes.n1.output.out}} == 1"}})
    assert res.status == "succeeded"
    assert "nt" in res.context.nodes and "nf" not in res.context.nodes

def test_wait_approval_pauses_and_resumes():
    _setup()
    g = _g([("n1", "start"), ("n2", "wait_approval"), ("n3", "double")],
           [("n1", "next", "n2"), ("n2", "approved", "n3")])
    calls = {"n": 0}
    def hook(ctx, node_id):
        calls["n"] += 1
        return True, {}
    res = Engine().execute(g, {}, resume_hook=hook, graph_config={"n1": {"v": 5}})
    assert calls["n"] == 1
    assert res.status == "succeeded"
    assert "n3" in res.context.nodes
