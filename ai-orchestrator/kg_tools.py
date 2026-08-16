"""知识图谱查询工具（LLM 可调用，返回 markdown 证据链）。

数据全部来自 kg_graph 封装（图查询）与 change_events 表（变更详情）。
DB 不可达时返回"知识图谱暂不可用"，绝不抛异常。
"""
from db import db_available, get_conn
import kg_graph


def kg_evidence_tool(service: str, cluster_id: str = "default") -> str:
    """查询服务在运维知识图谱中的证据链：
    服务节点信息 + 上游/下游依赖 + 关联变更 + 所属 pod/node（RUNS_ON 边）。"""
    service = (service or "").strip()
    if not service:
        return "请指定服务名"
    if not db_available():
        return "知识图谱暂不可用"

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
    try:
        conn = get_conn()
        if conn is not None:
            try:
                with conn.cursor() as cur:
                    cur.execute(
                        "SELECT id, cluster_id, service, change_type, operator, "
                        "content, created_at FROM change_events "
                        "WHERE service=%s AND cluster_id=%s "
                        "ORDER BY created_at DESC LIMIT 5",
                        (service, cluster_id))
                    for r in cur.fetchall():
                        changes.append({
                            "id": int(r["id"]),
                            "change_type": str(r.get("change_type") or ""),
                            "operator": str(r.get("operator") or ""),
                            "created_at": str(r.get("created_at") or ""),
                            "content": str(r.get("content") or "")[:200],
                        })
            finally:
                conn.close()
    except Exception:
        changes = []

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
