# flow_engine/usecase.py
from __future__ import annotations

import json
import uuid
from .store import FlowStore
from .graph import graph_from_dict, graph_to_dict, validate_graph
from .engine import Engine, RunStatus, resolve_config
from .noderegistry import node_registry
from .nodes_aiops import register_aiops_nodes


class WorkflowService:
    def __init__(self, store: FlowStore):
        self.store = store
        self.engine = Engine()
        register_aiops_nodes()

    def node_types(self) -> list[dict]:
        return [{"type": s.type, "kind": s.kind, "category": s.category,
                 "label": s.label, "ports": s.ports, "config_fields": s.config_fields,
                 "output_shape": s.output_shape} for s in node_registry.all()]

    def list_flows(self) -> list[dict]:
        return self.store.list_flows()

    def get_flow(self, flow_id):
        return self.store.get_flow(flow_id)

    def create_flow(self, name, description, graph) -> dict:
        self._check_graph(graph)
        flow_id = f"flow_{uuid.uuid4().hex[:8]}"
        self.store.save_flow({"id": flow_id, "name": name, "description": description,
                              "enabled": True, "graph": graph})
        return self.get_flow(flow_id)

    def update_flow(self, flow_id, data: dict) -> dict:
        existing = self.get_flow(flow_id)
        if not existing:
            raise KeyError(flow_id)
        graph = data.get("graph", existing["graph"])
        self._check_graph(graph)
        self.store.save_flow({"id": flow_id, "name": data.get("name", existing["name"]),
                              "description": data.get("description", existing["description"]),
                              "enabled": data.get("enabled", existing["enabled"]),
                              "graph": graph})
        return self.get_flow(flow_id)

    def delete_flow(self, flow_id) -> bool:
        return self.store.delete_flow(flow_id)

    def toggle_flow(self, flow_id) -> bool:
        return self.store.toggle_flow(flow_id)

    def _check_graph(self, graph):
        g = graph_from_dict(graph)
        validate_graph(g)
        return g

    def _run_with(self, graph, run_id, flow_id, trigger, resume_hook=None):
        g = graph_from_dict(graph)
        validate_graph(g)
        store = self.store
        run = store.get_run(run_id)
        version = run["flow_version"] if run else 1
        store.update_run_status(run_id, "running")
        result = self.engine.execute(g, trigger, resume_hook=resume_hook,
                                     graph_config={n["id"]: n.get("config", {}) for n in graph["nodes"]})
        type_map = {n["id"]: n["type"] for n in graph["nodes"]}
        for node_id, nr in result.node_results.items():
            store.save_run_node(run_id, node_id, type_map.get(node_id, ""),
                                nr.status, "{}", json.dumps(nr.output, ensure_ascii=False),
                                nr.fired_port, nr.error)
        store.update_run_status(run_id, result.status, error=result.error,
                                context_json=json.dumps({"trigger": trigger}, ensure_ascii=False))
        return result

    def run_flow(self, flow_id, trigger: dict, run_id: str = None):
        flow = self.get_flow(flow_id)
        if not flow:
            raise KeyError(flow_id)
        if not flow["enabled"]:
            raise ValueError(f"flow disabled: {flow_id}")
        run_id = run_id or f"run_{uuid.uuid4().hex[:8]}"
        self.store.create_run(flow_id, flow["version"], "manual", json.dumps(trigger, ensure_ascii=False),
                              run_id=run_id)
        return self._run_with(flow["graph"], run_id, flow_id, trigger,
                              resume_hook=lambda ctx, node_id: (False, {}))

    def resume_run(self, run_id, approved: bool):
        run = self.store.get_run(run_id)
        if not run:
            raise KeyError(run_id)
        flow = self.get_flow(run["flow_id"])
        trigger = json.loads(run["trigger_json"] or "{}")
        if approved:
            def hook(ctx, node_id):
                return True, {}
            # 需要重新执行：这里简化，重新跑一遍（真实实现需从暂停点恢复）
            return self._run_with(flow["graph"], run_id, run["flow_id"], trigger, resume_hook=hook)
        return self._run_with(flow["graph"], run_id, run["flow_id"], trigger,
                              resume_hook=lambda ctx, node_id: (False, {}))
