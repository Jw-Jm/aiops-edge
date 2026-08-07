import pytest
from flow_engine.noderegistry import NodeSpec, NodeRegistry, register_node, node_registry

def _spec():
    return NodeSpec(
        type="collect", kind="action", category="采集", label="数据采集",
        ports=["next"], config_fields=[{"name": "service", "label": "服务", "type": "text"}],
        output_shape=["services"], execute=lambda ctx, config: {"services": "ok"},
    )

def test_register_and_lookup():
    node_registry.reset()
    register_node(_spec())
    spec = node_registry.lookup("collect")
    assert spec is not None and spec.type == "collect" and spec.label == "数据采集"

def test_duplicate_register_raises():
    node_registry.reset()
    register_node(_spec())
    with pytest.raises(ValueError):
        register_node(_spec())

def test_lookup_unknown_returns_none():
    node_registry.reset()
    assert node_registry.lookup("nope") is None

def test_all_lists_registered():
    node_registry.reset()
    register_node(_spec())
    types = [s.type for s in node_registry.all()]
    assert "collect" in types
