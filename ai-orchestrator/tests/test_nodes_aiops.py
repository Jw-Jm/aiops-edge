# tests/test_nodes_aiops.py
from flow_engine.noderegistry import node_registry
from flow_engine.nodes_aiops import register_aiops_nodes
from flow_engine.expr import RunContext

def test_register_all_aiops_nodes():
    node_registry.reset()
    register_aiops_nodes()
    types = [s.type for s in node_registry.all()]
    for t in ["collect", "clean", "rca", "rag", "crewai", "holmes", "plan",
              "risk", "wait_approval", "execute", "verify", "report", "memorize",
              "summarize", "condition"]:
        assert t in types

def test_condition_ports():
    node_registry.reset()
    register_aiops_nodes()
    spec = node_registry.lookup("condition")
    assert spec.ports == ["true", "false"]

def test_wait_approval_ports():
    node_registry.reset()
    register_aiops_nodes()
    spec = node_registry.lookup("wait_approval")
    assert set(spec.ports) == {"approved", "rejected"}

def test_execute_returns_output_dict():
    node_registry.reset()
    register_aiops_nodes()
    spec = node_registry.lookup("collect")
    out = spec.execute(RunContext(), {"service": "demo"})
    assert isinstance(out, dict)
