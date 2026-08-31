"""知识图谱查询工具（LLM 可调用，返回 markdown 证据链）。

数据全部来自 kg_graph 封装；图谱事实与变更详情由 query-api 持有。
边界不可达时返回"知识图谱暂不可用"，绝不抛异常。
"""
from __future__ import annotations

import os
from typing import Any, Mapping


def graph_query_tool(
    *,
    graph_operation: str,
    execution_context: Any,
    tenant_id: str | None = None,
    cluster_id: str | None = None,
    context_ref: str = "",
    client: Any,
    **params: Any,
) -> dict:
    """The only orchestrator graph-read tool.

    ``client`` is injected by the runtime so this function cannot accidentally
    open a HugeGraph/SQL connection.  The internal client performs tool,
    capability, scope and lease checks before any datasource I/O.
    """
    tenant_id = tenant_id or str(getattr(execution_context, "tenant_id", ""))
    cluster_id = cluster_id or str(getattr(execution_context, "cluster_id", ""))
    if not tenant_id or not cluster_id:
        raise ValueError("graph query requires tenant_id and cluster_id")
    result = client.query_graph_v1(
        tenant_id=tenant_id,
        cluster_id=cluster_id,
        params={"graph_operation": graph_operation, **params},
        context_ref=context_ref or str(getattr(execution_context, "run_id", "graph-query")),
        execution_context=execution_context,
    )
    return result.body


def kg_evidence_tool(service: str, cluster_id: str = "") -> str:
    """查询服务在运维知识图谱中的证据链：
    服务节点信息 + 上游/下游依赖 + 关联变更 + 所属 pod/node（RUNS_ON 边）。"""
    # Legacy compatibility only.  New RCA and graph tools use graph_query_tool.
    import kg_graph

    service = (service or "").strip()
    if not service:
        return "请指定服务名"
    if (os.environ.get("AIOPS_ENV", "").strip().lower() == "production"
            and str(cluster_id or "").strip() in {"", "default", "all"}):
        return "知识图谱查询拒绝：必须提供显式 cluster_id"
    node = kg_graph.get_node("service", service, cluster_id)
    if node is None:
        return (f"知识图谱中未找到服务 {service!r}（cluster_id={cluster_id}），"
                "请先执行 build 或核对服务名")
    node_id = int(node["id"])
    try:
        nb = kg_graph.neighbors(node_id, hops=2)
    except Exception:
        return "知识图谱暂不可用"
    node_map = {int(n["id"]): n for n in nb.get("nodes", [])}

    upstream, downstream, infra = [], [], []
    for e in nb.get("edges", []):
        if e["type"] == "DEPENDS_ON":
            if int(e["dst_id"]) == node_id:
                n = node_map.get(int(e["src_id"]))
                if n:
                    upstream.append(n)
            elif int(e["src_id"]) == node_id:
                n = node_map.get(int(e["dst_id"]))
                if n:
                    downstream.append(n)
        elif e["type"] == "RUNS_ON":
            if int(e["src_id"]) == node_id:
                other = int(e["dst_id"])
            elif int(e["dst_id"]) == node_id:
                other = int(e["src_id"])
            else:
                continue
            n = node_map.get(other)
            if n:
                infra.append(n)

    changes = []
    for e in nb.get("edges", []):
        if e.get("type") != "HAS_CHANGE" or int(e.get("src_id", -1)) != node_id:
            continue
        change = node_map.get(int(e.get("dst_id", -1)))
        if not change or change.get("type") != "change":
            continue
        props = change.get("props", {})
        changes.append({
            "id": props.get("change_id", change.get("name", "")),
            "change_type": str(props.get("change_type", "")),
            "operator": str(props.get("operator", "")),
            "created_at": str(props.get("created_at", "")),
            "content": str(props.get("content") or props.get("summary") or "")[:200],
        })

    lines = [
        f"# 知识图谱证据链：{service}（cluster_id={cluster_id}）",
        f"- 服务节点：id={node_id}，type=service",
        "\n## 上游依赖（依赖本服务的方）",
    ]
    if upstream:
        for n in upstream:
            lines.append(f"- {n['name']}（id={n['id']}）")
    else:
        lines.append("- 无")

    lines.append("\n## 下游依赖（本服务所依赖的方）")
    if downstream:
        for n in downstream:
            lines.append(f"- {n['name']}（id={n['id']}）")
    else:
        lines.append("- 无")

    lines.append("\n## 所属基础设施（RUNS_ON）")
    if infra:
        for n in infra:
            lines.append(f"- {n['name']}（type={n['type']}，id={n['id']}）")
    else:
        lines.append("- 无")

    lines.append("\n## 关联变更（最近 5 条）")
    if changes:
        for c in changes:
            lines.append(
                f"- [{c['created_at']}] {c['change_type']} by {c['operator']}: "
                f"{c['content']}")
    else:
        lines.append("- 无")

    return "\n".join(lines)
