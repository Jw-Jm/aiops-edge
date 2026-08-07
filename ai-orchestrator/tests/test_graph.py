import pytest
from flow_engine.noderegistry import NodeSpec, register_node, node_registry
from flow_engine.graph import Graph, GraphNode, GraphEdge, graph_from_dict, validate_graph

def _setup():
    node_registry.reset()
    for t, ports in [("a", ["next"]), ("b", ["next"]), ("cond", ["true", "false"])]:
        register_node(NodeSpec(type=t, kind="action" if t != "cond" else "control",
                               category="t", label=t, ports=ports,
                               execute=lambda ctx, config: {}))

def _graph():
    return Graph(
        nodes=[GraphNode(id="n1", type="a"), GraphNode(id="n2", type="b")],
        edges=[GraphEdge(id="e1", source="n1", source_port="next", target="n2")],
    )

def test_valid_chain_passes():
    _setup()
    validate_graph(_graph())

def test_cycle_detected():
    _setup()
    g = Graph(
        nodes=[GraphNode(id="n1", type="a"), GraphNode(id="n2", type="b")],
        edges=[GraphEdge(id="e1", source="n1", target="n2"),
               GraphEdge(id="e2", source="n2", target="n1")],
    )
    with pytest.raises(ValueError, match="cycle"):
        validate_graph(g)

def test_unknown_node_type():
    _setup()
    g = Graph(nodes=[GraphNode(id="n1", type="nope")], edges=[])
    with pytest.raises(ValueError, match="unknown node type"):
        validate_graph(g)

def test_edge_refs_missing_node():
    _setup()
    g = Graph(
        nodes=[GraphNode(id="n1", type="a")],
        edges=[GraphEdge(id="e1", source="n1", target="ghost")],
    )
    with pytest.raises(ValueError, match="missing"):
        validate_graph(g)

def test_invalid_source_port():
    _setup()
    # 'a' 只有 next，不能用 true
    g = Graph(
        nodes=[GraphNode(id="n1", type="a"), GraphNode(id="n2", type="b")],
        edges=[GraphEdge(id="e1", source="n1", source_port="true", target="n2")],
    )
    with pytest.raises(ValueError, match="port"):
        validate_graph(g)

def test_graph_from_dict():
    _setup()
    d = {"nodes": [{"id": "n1", "type": "a"}], "edges": []}
    g = graph_from_dict(d)
    assert g.nodes[0].id == "n1"
