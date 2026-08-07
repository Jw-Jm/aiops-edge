# flow_engine/engine.py
import re
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass, field
from .noderegistry import node_registry
from .graph import Graph, GraphNode, validate_graph
from .expr import RunContext, resolve_value, eval_condition


class RunStatus:
    PENDING = "pending"
    RUNNING = "running"
    WAITING = "waiting_approval"
    SUCCEEDED = "succeeded"
    FAILED = "failed"
    CANCELED = "canceled"


@dataclass
class NodeResult:
    node_id: str
    status: str = ""
    output: dict = field(default_factory=dict)
    fired_port: str = ""
    error: str = ""


@dataclass
class RunResult:
    status: str = RunStatus.RUNNING
    context: RunContext = field(default_factory=RunContext)
    node_results: dict = field(default_factory=dict)
    error: str = ""


def resolve_config(config: dict, ctx: RunContext) -> dict:
    return {k: resolve_value(v, ctx) for k, v in (config or {}).items()}


class Engine:
    MAX_WORKERS = 4

    def execute(self, graph: Graph, trigger: dict, resume_hook=None,
                graph_config: dict = None) -> RunResult:
        validate_graph(graph)
        ctx = RunContext(trigger=trigger, vars={})
        node_by_id = {n.id: n for n in graph.nodes}
        # 出边表：node_id -> list[GraphEdge]
        out_edges = {}
        for e in graph.edges:
            out_edges.setdefault(e.source, []).append(e)
        executed = set()
        result = RunResult(context=ctx)
        graph_config = graph_config or {}

        def _run_node(node: GraphNode) -> NodeResult:
            if node.id in executed:
                return NodeResult(node_id=node.id, status="skipped")
            executed.add(node.id)
            spec = node_registry.lookup(node.type)
            config = resolve_config(node.config or graph_config.get(node.id, {}), ctx)
            nr = NodeResult(node_id=node.id, status="running")
            try:
                out = spec.execute(ctx, config) or {}
                ctx.nodes[node.id] = {"output": out, "status": "done"}
                nr.output = out
                nr.status = "done"
                if node.type == "condition":
                    fired = "true" if eval_condition(config.get("expr", "false"), ctx) else "false"
                elif node.type == "wait_approval":
                    if resume_hook is None:
                        fired = "approved"
                    else:
                        approved, _data = resume_hook(ctx, node.id)
                        if not approved:
                            result.status = RunStatus.WAITING
                        fired = "approved" if approved else "rejected"
                else:
                    fired = "next"
                nr.fired_port = fired
            except Exception as e:
                nr.status = "error"
                nr.error = str(e)
                nr.fired_port = "error"
            return nr

        # 找出 entry：无入边节点（按定义顺序，先 trigger 后其他）
        has_in = {e.target for e in graph.edges}
        entries = [n for n in graph.nodes if n.id not in has_in]
        if not entries:
            entries = [graph.nodes[0]]

        def _visit(node_id: str):
            if node_id in executed:
                return
            node = node_by_id[node_id]
            nr = _run_node(node)
            result.node_results[node.id] = nr
            if nr.status == "error":
                # 找 error 出边
                err_edges = [e for e in out_edges.get(node.id, []) if e.source_port == "error"]
                if not err_edges:
                    result.status = RunStatus.FAILED
                    result.error = nr.error
                    return
                for e in err_edges:
                    _visit(e.target)
                return
            # 依 fired_port 激活下游（并行）
            next_edges = [e for e in out_edges.get(node.id, []) if e.source_port == nr.fired_port]
            if not next_edges:
                return
            futures = []
            with ThreadPoolExecutor(max_workers=self.MAX_WORKERS) as pool:
                for e in next_edges:
                    futures.append(pool.submit(_visit, e.target))
                for f in futures:
                    f.result()
            if result.status == RunStatus.FAILED:
                return

        for entry in entries:
            _visit(entry.id)
            if result.status == RunStatus.FAILED:
                break

        if result.status == RunStatus.WAITING:
            return result
        result.status = RunStatus.FAILED if result.status == RunStatus.FAILED else RunStatus.SUCCEEDED
        return result
