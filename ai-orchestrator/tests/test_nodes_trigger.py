# tests/test_nodes_trigger.py
import pytest
from flow_engine.nodes_trigger import TRIGGER_NODES, alert_matches


def test_trigger_node_types_registered():
    assert set(TRIGGER_NODES) == {"trigger.manual", "trigger.cron", "trigger.alert_fired"}
    for spec in TRIGGER_NODES.values():
        assert spec["kind"] == "trigger" and "next" in spec["ports"]


def test_alert_matches_rule_and_severity():
    cfg = {"rule": "high-cpu", "min_severity": "warning"}
    assert alert_matches(cfg, "high-cpu", "critical") is True
    assert alert_matches(cfg, "high-cpu", "info") is False      # 低于最低级别
    assert alert_matches(cfg, "high-mem", "critical") is False  # 规则名不匹配


def test_alert_matches_empty_rule_matches_any():
    assert alert_matches({"rule": "", "min_severity": "warning"}, "any-rule", "critical") is True


# ── A1 graph 校验规则 ─────────────────────────────────────────────
from flow_engine.noderegistry import node_registry, register_trigger_nodes, register_node, NodeSpec
from flow_engine.graph import Graph, GraphNode, GraphEdge, validate_graph


def _reg():
    node_registry.reset()
    register_trigger_nodes()
    register_node(NodeSpec(type="collect", kind="action", category="采集", label="采集",
                           ports=["next"], execute=lambda ctx, config: {}))


def test_validate_rejects_multiple_trigger_nodes():
    _reg()
    g = Graph(nodes=[GraphNode(id="t1", type="trigger.manual"),
                     GraphNode(id="t2", type="trigger.cron")], edges=[])
    with pytest.raises(ValueError, match="trigger"):
        validate_graph(g)


def test_validate_rejects_trigger_with_incoming_edge():
    _reg()
    g = Graph(nodes=[GraphNode(id="c", type="collect"),
                     GraphNode(id="t1", type="trigger.manual")],
              edges=[GraphEdge(id="e1", source="c", source_port="next", target="t1")])
    with pytest.raises(ValueError, match="incoming"):
        validate_graph(g)


def test_validate_accepts_single_trigger_graph():
    _reg()
    g = Graph(nodes=[GraphNode(id="t1", type="trigger.manual"),
                     GraphNode(id="c", type="collect")],
              edges=[GraphEdge(id="e1", source="t1", source_port="next", target="c")])
    validate_graph(g)  # 不应抛错
