"""Observability tools for AI agents"""
import json
import os
import subprocess
import urllib.request
import urllib.error

from skill_registry import ToolRegistry
from kg_tools import kg_evidence_tool

QUERY_API = os.environ.get("QUERY_API_URL", "http://query-api.observability.svc.cluster.local:8080/api/v1")
INTERNAL_TOKEN = os.environ.get("INTERNAL_TOKEN", "")


def _api_headers():
    h = {"X-Tenant-ID": "default"}
    if INTERNAL_TOKEN:
        h["X-Internal-Token"] = INTERNAL_TOKEN
    return h


def _get_json(url: str) -> dict:
    req = urllib.request.Request(url, headers=_api_headers())
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return json.loads(resp.read())
    except (urllib.error.URLError, json.JSONDecodeError) as e:
        return {"error": str(e)}


def _cluster_param(cluster_id: str = "") -> str:
    """生成 cluster_id 查询参数（空/all 不过滤），供工具 URL 拼接（A-5）。"""
    cid = cluster_id or os.environ.get("CLUSTER_ID", "")
    if cid and cid != "all":
        return f"cluster_id={cid}"
    return ""


def query_metrics(service: str, tenant_id: str = "default", cluster_id: str = "") -> str:
    if not service:
        return "未指定服务名称"
    cp = _cluster_param(cluster_id)
    data = _get_json(f"{QUERY_API}/services/{service}" + (("?" + cp) if cp else ""))
    if isinstance(data, dict) and "error" in data:
        return f"查询失败: {data['error']}"
    return json.dumps(data, indent=2, ensure_ascii=False)[:5000]


def query_traces(service: str = "", tenant_id: str = "default", cluster_id: str = "") -> str:
    cp = _cluster_param(cluster_id)
    url = f"{QUERY_API}/traces?limit=5"
    if cp:
        url += "&" + cp
    data = _get_json(url)
    if isinstance(data, dict) and "error" in data:
        return f"查询失败: {data['error']}"
    return json.dumps(data, indent=2, ensure_ascii=False)[:4000]


def query_logs(service: str = "", minutes: int = 30, cluster_id: str = "") -> str:
    """查询最近 N 分钟日志（ClickHouse log_records，经 query-api）。
    空 service 走全量最近日志。"""
    params = []
    if service:
        params.append(f"service={service}")
    params.append(f"minutes={minutes}")
    cp = _cluster_param(cluster_id)
    if cp:
        params.append(cp)
    url = f"{QUERY_API}/logs/query?" + "&".join(params)
    data = _get_json(url)
    if isinstance(data, dict) and "error" in data:
        return f"日志查询失败: {data['error']}"
    rows = data.get("data", []) if isinstance(data, dict) else []
    if not rows:
        return "（近 30 分钟无日志）"
    lines = []
    for r in rows[:50]:
        sev = r.get("severity", "")
        body = (r.get("body", "") or "").strip().replace("\n", " ")
        lines.append(f"[{r.get('timestamp','')}] {r.get('service_name','')} {sev}: {body[:200]}")
    return "\n".join(lines)


def query_topology(tenant_id: str = "default", cluster_id: str = "") -> str:
    cp = _cluster_param(cluster_id)
    data = _get_json(f"{QUERY_API}/topology/global" + (("?" + cp) if cp else ""))
    if isinstance(data, dict) and "error" in data:
        return f"查询失败: {data['error']}"
    return json.dumps(data, indent=2, ensure_ascii=False)[:3000]


def get_service_list(tenant_id: str = "default", cluster_id: str = "") -> str:
    url = f"{QUERY_API}/services"
    cp = _cluster_param(cluster_id)
    if cp:
        url += "?" + cp
    data = _get_json(url)
    if isinstance(data, dict) and "error" in data:
        return f"查询失败: {data['error']}"
    # P0-1 修复：/api/v1/services 顶层键为 "services"（非 "data"），兼容两种契约
    if isinstance(data, dict):
        if "services" in data:
            data = data["services"]
        elif "data" in data:
            data = data["data"]
    if isinstance(data, list):
        summary = []
        for s in data[:10]:
            calls = float(s.get("calls", s.get("traces", 0)) or 0)
            errors = float(s.get("errors", s.get("error_count", 0)) or 0)
            error_rate = round(errors / calls * 100, 2) if calls > 0 else 0.0
            summary.append({
                "service_name": s.get("service_name"),
                "traces": int(calls),
                "avg_ms": round(float(s.get("avg_latency_ms", s.get("avg_ms", 0)) or 0), 1),
                "max_ms": round(float(s.get("max_ms", 0)), 1),
                "error_rate": error_rate,
            })
        return json.dumps(summary, indent=2, ensure_ascii=False)
    return json.dumps(data, indent=2, ensure_ascii=False)[:4000]


def execute_shell(command: str, timeout: int = 30) -> str:
    from shell_policy import ShellPolicy
    policy = ShellPolicy()
    reject = policy.check(command)
    if reject:
        return f"命令被安全策略拒绝: {reject}"
    if blk := policy.check_extra_blacklist(command):
        return f"命令被安全策略拒绝: {blk}"
    try:
        # 已按产品要求放宽：命令支持管道/重定向（shell=True），执行前经人工审批，
        # 因此按 shell 语义执行（`kubectl ... | grep` 等管道生效）。
        result = subprocess.run(command, shell=True, capture_output=True, text=True, timeout=timeout)
        output = result.stdout[:2000]
        if result.stderr:
            output += "\n[stderr]: " + result.stderr[:500]
        return output or "(no output)"
    except subprocess.TimeoutExpired:
        return f"命令超时 (>{timeout}s)"
    except Exception as e:
        return f"执行失败: {str(e)}"


def k8sgpt_diagnose(namespace: str = "observability") -> str:
    try:
        result = subprocess.run(
            ["k8sgpt", "analyze", "--explain", "-n", namespace, "-o", "text"],
            capture_output=True, text=True, timeout=60
        )
        if result.returncode == 0 and result.stdout.strip():
            return result.stdout[:3000]
        if result.stderr:
            return f"K8sGPT: {result.stderr[:500]}"
        return "未发现集群问题"
    except FileNotFoundError:
        return "K8sGPT 未安装"
    except subprocess.TimeoutExpired:
        return "K8sGPT 诊断超时"
    except Exception as e:
        return f"K8sGPT 诊断失败: {str(e)}"


def deepflow_status() -> str:
    data = _get_json(f"{QUERY_API}/deepflow/status")
    return json.dumps(data, indent=2, ensure_ascii=False)


def get_infrastructure() -> str:
    """获取K8s基础设施信息"""
    # 修复：查询所有 namespace 的 Pod（namespace=all），并完整展示 name/namespace/status/restarts。
    # 之前写死 namespace=observability 且只取 name/status，导致 LLM 拿不到 deepflow、kube-system
    # 等真实 namespace，处置命令只能用 <ns> 占位符或写死 observability。
    pods_data = _get_json(f"{QUERY_API}/infrastructure/pods?namespace=all")
    nodes_data = _get_json(f"{QUERY_API}/infrastructure/nodes")

    pods = pods_data.get("pods", [])
    nodes = nodes_data.get("nodes", [])

    report = f"运行中 Pods: {len(pods)} 个\n"
    # 完整展示每个 Pod 的名字、命名空间、状态、重启次数，
    # 让 LLM 能引用真实资源名（如 redis-76dd9b85cb-q7p2r / redis）生成确定性处置命令
    infos = [(p.get('name','?')[:50], p.get('namespace','?'), p.get('status','?'), p.get('restarts',0)) for p in pods]
    for name, ns, st, rc in infos:
        report += f"  - {ns}/{name}: {st} restarts={rc}\n"

    report += f"\n- 节点: {len(nodes)} 个\n"
    for n in nodes:
        report += f"  - {n.get('name','?')}: {n.get('status','?')} CPU={n.get('cpu','?')} MEM={n.get('memory','?')}\n"
    return report

# ═══════════════════════════════════════════════════════════════
#  Mount E3: query_knowledge 内置运维知识库工具 (cls=safe, category=knowledge)
# ═══════════════════════════════════════════════════════════════
def _query_knowledge(query: str = "", path_prefix: str = "", tags: str = "",
                     max_results: int = 5) -> str:
    """查询内置运维知识库(playbook 处置手册 + 历史案例), 返回诊断建议/处置步骤。"""
    from playbook_loader import query_knowledge
    result = query_knowledge(
        query,
        path_prefix=path_prefix.strip() or None,
        tags=[t.strip() for t in tags.split(",") if t.strip()] if tags else None,
        max_results=int(max_results or 5),
    )
    return json.dumps(result, ensure_ascii=False)[:6000]


if not ToolRegistry.get("query_knowledge"):
    ToolRegistry.register(
        name="query_knowledge",
        description="查询内置运维知识库(playbook 处置手册 + 历史案例), 返回按相关度排序的诊断建议与处置步骤",
        category="knowledge",
        cls_="safe",
        params={
            "query": {"type": "string", "required": True, "default": "", "desc": "检索关键词"},
            "path_prefix": {"type": "string", "required": False, "default": "", "desc": "playbook 分类前缀(diagnostics/alerts/concepts/reference)"},
            "tags": {"type": "string", "required": False, "default": "", "desc": "标签过滤(逗号分隔)"},
            "max_results": {"type": "int", "required": False, "default": 5, "desc": "返回条数"},
        },
    )(_query_knowledge)

# ═══════════════════════════════════════════════════════════════
#  Mount E4: query_knowledge_graph 知识图谱证据链工具 (cls=safe, category=observability)
# ═══════════════════════════════════════════════════════════════
if not ToolRegistry.get("query_knowledge_graph"):
    ToolRegistry.register(
        name="query_knowledge_graph",
        description="查询运维知识图谱: 服务的依赖关系/上下游/关联变更/所属基础设施, 返回结构化证据链",
        category="observability",
        cls_="safe",
        params={
            "service": {"type": "string", "required": True, "default": "", "desc": "服务名"},
            "cluster_id": {"type": "string", "required": False, "default": "default", "desc": "集群ID"},
        },
    )(kg_evidence_tool)
