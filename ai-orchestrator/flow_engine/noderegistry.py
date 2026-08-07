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
