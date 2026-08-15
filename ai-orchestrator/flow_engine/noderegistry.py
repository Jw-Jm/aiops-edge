from dataclasses import dataclass, field
from typing import Callable, Optional


@dataclass
class NodeSpec:
    type: str
    kind: str            # trigger | action | control | data
    category: str
    label: str
    ports: list          # 控制输出端口，默认 ["next"]
    config_fields: list = field(default_factory=list)
    output_shape: list = field(default_factory=list)
    execute: Optional[Callable] = None  # fn(ctx, config) -> dict


class NodeRegistry:
    def __init__(self):
        self._specs: dict[str, NodeSpec] = {}

    def register(self, spec: NodeSpec):
        if spec.type in self._specs:
            raise ValueError(f"node type already registered: {spec.type}")
        self._specs[spec.type] = spec

    def lookup(self, type_: str) -> Optional[NodeSpec]:
        return self._specs.get(type_)

    def all(self) -> list:
        return list(self._specs.values())

    def reset(self):
        self._specs.clear()


node_registry = NodeRegistry()


def register_node(spec: NodeSpec):
    node_registry.register(spec)


def register_trigger_nodes():
    """并入 nodes_trigger.TRIGGER_NODES 三种触发器节点（幂等）。

    engine 以 execute(ctx, config) 两参调用 execute, 故此处用闭包包装
    exec_trigger(ctx, node_id, node_type, config)（node_id 由引擎运行期确定,
    包装器传空串, exec_trigger 落到固定键 "trigger"）。
    """
    from .nodes_trigger import TRIGGER_NODES, exec_trigger
    for t, d in TRIGGER_NODES.items():
        if node_registry.lookup(t) is None:
            node_registry.register(NodeSpec(
                type=t, kind=d["kind"], category="触发", label=d["label"],
                ports=d["ports"],
                config_fields=[{"name": k, **v} for k, v in d["config_fields"].items()],
                execute=lambda ctx, config, _t=t: exec_trigger(ctx, "", _t, config)))
