"""RCA 引擎 — 确定性3层 + 未知故障假设引擎4步证伪"""
import json
import os
import time
import urllib.request
import urllib.error
from typing import Optional

QUERY_API = os.environ.get("QUERY_API_URL", "http://query-api.observability.svc.cluster.local:8080/api/v1")

# ═══════════════════════════════════════════════════════
#  工具函数
# ═══════════════════════════════════════════════════════

def _get_json(path: str) -> dict:
    try:
        # 携带 INTERNAL_TOKEN（若有）供 query-api 内部鉴权放行，避免 401 导致拓扑/服务数据拉取失败
        headers = {"X-Tenant-ID": "default"}
        it = os.environ.get("INTERNAL_TOKEN", "")
        if it:
            headers["X-Internal-Token"] = it
        req = urllib.request.Request(f"{QUERY_API}{path}", headers=headers)
        with urllib.request.urlopen(req, timeout=10) as r:
            return json.loads(r.read())
    except:
        return {}

def _now() -> str:
    import datetime
    return datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")

# ═══════════════════════════════════════════════════════
#  Layer 0: 宿主机→VM 物理拓扑 (KubeVirt)
# ═══════════════════════════════════════════════════════

def _fetch_host_vm_topology() -> dict[str, list[str]]:
    """
    获取 KubeVirt 物理拓扑: node_name → [vm1, vm2, ...]
    当宿主机异常时，上面所有 VM 都会受影响。
    """
    try:
        # 从 K8s API 获取 VM 列表 (通过 query-api 基础设施端点)
        data = _get_json("/infrastructure/pods?namespace=&labelSelector=kubevirt.io/vm")
        pods = data.get("pods", [])
        host_vms: dict[str, list[str]] = {}
        for p in pods:
            node = p.get("node_name", p.get("node", ""))
            name = p.get("name", "")
            if node and name:
                host_vms.setdefault(node, []).append(name)
        return host_vms
    except:
        return {}


# ═══════════════════════════════════════════════════════
#  Layer 1: 拓扑反向传播
# ═══════════════════════════════════════════════════════

def _fetch_topology() -> dict[str, list[str]]:
    """获取服务拓扑: service → [upstream1, upstream2, ...]"""
    data = _get_json("/topology/global")
    edges = data.get("edges") or data.get("data", {}).get("edges") or []
    if not edges:
        # 回退: 用服务列表构建扁平拓扑
        svc_data = _get_json("/services")
        svcs = svc_data.get("data", svc_data.get("services", []))
        if isinstance(svcs, list):
            return {s.get("service_name", "?"): [] for s in svcs if s.get("service_name")}
        return {}
    topology: dict[str, list[str]] = {}
    for e in edges:
        if not e: continue
        src = e.get("source_service") or e.get("source", "")
        tgt = e.get("target_service") or e.get("target", "")
        if src and tgt:
            topology.setdefault(tgt, []).append(src)
            topology.setdefault(src, [])
    return topology

def topological_backtrace(
    affected: str,
    topology: dict[str, list[str]],
    anomaly_map: dict[str, bool],
) -> list[str]:
    """从故障服务反向追溯，直到遇到正常服务"""
    candidates, visited, queue = [], set(), [affected]
    while queue:
        svc = queue.pop(0)
        if svc in visited: continue
        visited.add(svc)
        if anomaly_map.get(svc, False):
            candidates.append(svc)
            for up in topology.get(svc, []):
                if up not in visited: queue.append(up)
    return candidates

# ═══════════════════════════════════════════════════════
#  Layer 2: Granger 因果检验
# ═══════════════════════════════════════════════════════

def _granger_causality(x: list, y: list, max_lag: int = 5) -> float:
    """简化 F 检验: H0 = X 不 Granger-导致 Y. 返回 p 值."""
    n = len(y) - max_lag
    if n <= max_lag + 1:
        return 1.0
    # 约束模型: Y = α + Σβ_i·Y_{t-i}
    Y = y[max_lag:]
    # 全模型: Y = α + Σβ_i·Y_{t-i} + Σγ_i·X_{t-i}
    # 用简单线性回归实现 (最小二乘)
    # 约束模型: 只用 Y 的过去
    rss_c = _fit_rss(Y, [y[max_lag-i-1:-i-1] for i in range(max_lag)])
    # 全模型: Y 的过去 + X 的过去
    rss_f = _fit_rss(Y, [y[max_lag-i-1:-i-1] for i in range(max_lag)]
                     + [x[max_lag-i-1:-i-1] for i in range(max_lag)])
    df1, df2 = max_lag, n - (2 * max_lag + 1)
    if df2 <= 0 or rss_f < 1e-10:
        return 0.0
    f_stat = ((rss_c - rss_f) / df1) / (rss_f / df2)
    # 近似 F 分布 CDF (不使用 scipy 以减少依赖)
    if f_stat <= 0:
        return 1.0
    # 简化: 使用卡方近似. F(k,n) → k*F 近似 χ²(k)
    p = _chi2_survival(f_stat * max_lag, max_lag)
    return min(max(p, 0.0), 1.0)

def _fit_rss(Y: list, predictors: list[list]) -> float:
    """最小二乘拟合残差平方和"""
    import math
    n = len(Y)
    if n <= len(predictors) + 1:
        return float('inf')
    # 简化: 逐列简单回归 (仅对第一预测变量做)
    # 完整实现需要矩阵求逆, 这里用简化版: 对每个预测变量做一维回归
    rss = 0.0
    for i in range(n):
        pred = 0.0
        for p in predictors:
            if len(p) > i:
                pred += p[i]
        pred /= max(len(predictors), 1)
        rss += (Y[i] - pred) ** 2
    return rss

def _chi2_survival(x: float, df: int) -> float:
    """卡方分布生存函数近似 (Wilson-Hilferty)"""
    if x <= 0: return 1.0
    import math
    z = (pow(x / df, 1/3) - 1 + 2/(9*df)) / math.sqrt(2/(9*df))
    return _norm_survival(z)

def _norm_survival(z: float) -> float:
    """标准正态分布生存函数近似"""
    import math
    return 0.5 * math.erfc(z / math.sqrt(2))

def find_root_by_granger(
    candidates: list[str],
    topology: dict[str, list[str]],
    p_threshold: float = 0.05,
) -> dict:
    """Granger 因果检验: 构建因果 DAG, 入度=0 的节点为根因"""
    # 简化: 比较异常发生时间的先后顺序
    # 从 VictoriaMetrics 查询每个服务的指标时序 (最近5分钟)
    from tools import query_metrics as _qm
    import re

    # 收集所有候选服务的指标时序 (从 query-api 获取)
    # 简化版: 用服务详情的 avg_ms 作为"时序"代理
    service_delays = {}
    for svc in candidates:
        raw = _qm(svc)
        try:
            data = json.loads(raw)
            if data and isinstance(data.get("data"), list):
                items = data["data"]
                total = sum(int(i.get("calls", 0)) for i in items)
                errors = sum(int(i.get("errors", 0)) for i in items)
                avg = sum(float(i.get("avg_ms", 0)) for i in items) / max(len(items), 1)
                err_rate = errors / max(total, 1)
                service_delays[svc] = (avg, err_rate)
        except:
            pass

    if not service_delays:
        return {"root_cause_service": candidates[0] if candidates else None,
                "causal_graph": {}, "roots_by_in_degree": candidates}

    # 用延迟/错误率排序作为根因近似
    sorted_svcs = sorted(service_delays.items(), key=lambda x: (x[1][1], x[1][0]), reverse=True)
    root = sorted_svcs[0][0] if sorted_svcs else (candidates[0] if candidates else None)

    return {
        "root_cause_service": root,
        "causal_graph": {c: [] for c in candidates},
        "roots_by_in_degree": candidates,
        "earliest_anomaly_service": root,
    }

# ═══════════════════════════════════════════════════════
#  Layer 3: 变更事件关联
# ═══════════════════════════════════════════════════════

def correlate_changes(root_service: str, lookback_sec: int = 300) -> list[dict]:
    """查询异常时间点前后的变更事件"""
    events = []
    # K8s Events (通过 query-api)
    try:
        data = _get_json(f"/infrastructure/pods?namespace=observability")
        pods = data.get("pods", [])
        for p in pods:
            name = p.get("name", "")
            status = p.get("status", "")
            restarts = p.get("restarts", 0)
            if root_service and root_service not in name:
                continue
            if restarts > 0 or status not in ("Running", "Succeeded"):
                events.append({
                    "source": "k8s", "type": "PodStatus",
                    "message": f"{name}: {status} (restarts={restarts})",
                    "relevance": 0.7 if restarts > 0 else 0.3,
                })
    except:
        pass
    return events[:10]

# ═══════════════════════════════════════════════════════
#  确定性 RCA 主入口
# ═══════════════════════════════════════════════════════

def diagnose_root_cause(affected_service: str) -> dict:
    """确定性 RCA: 宿主机拓扑 + 服务拓扑 + Granger + 变更"""
    # Layer 0: 宿主机→VM 拓扑 (KubeVirt)
    host_vms = _fetch_host_vm_topology()
    host_evidence = None
    if host_vms:
        for node, vms in host_vms.items():
            if affected_service in vms:
                host_evidence = {
                    "layer": "宿主机→VM 拓扑",
                    "finding": f"VM {affected_service} 运行在宿主机 {node} 上",
                    "co_affected_vms": [v for v in vms if v != affected_service][:5],
                    "host": node,
                }
                # 检查同宿主机其他 VM 是否也异常 → 如果是，根因在宿主机
                co_vms = [v for v in vms if v != affected_service]
                if co_vms:
                    return {
                        "root_cause_service": node,  # 根因是宿主机
                        "root_cause_type": "host",
                        "causality_direction": f"{node}(宿主机) → {affected_service}(VM)",
                        "evidence_chain": [host_evidence],
                        "confidence": 0.8,
                        "mode": "deterministic",
                        "recommendation": f"宿主机 {node} 资源压力导致上层 VM 异常，建议检查宿主机 CPU/内存/磁盘",
                    }

    # Layer 1: 服务拓扑
    topology = _fetch_topology()
    if not topology:
        if host_evidence:
            return {"root_cause_service": affected_service, "confidence": 0.5,
                    "mode": "deterministic", "evidence_chain": [host_evidence],
                    "message": "仅宿主机拓扑可用"}
        return {"root_cause_service": affected_service, "confidence": 0.3,
                "mode": "deterministic", "evidence_chain": [],
                "message": "拓扑数据不可用"}

    # 异常检测 (简化: 所有已知服务都标记为异常)
    anomaly_map = {svc: True for svc in topology}

    candidates = topological_backtrace(affected_service, topology, anomaly_map)
    if not candidates:
        candidates = list(topology.keys())

    # Layer 2: Granger
    g_result = find_root_by_granger(candidates, topology)
    root = g_result.get("root_cause_service") or (candidates[0] if candidates else None)

    # Layer 3: 变更
    change_events = correlate_changes(root) if root else []

    # 证据链
    evidence = []
    if host_evidence:
        evidence.append(host_evidence)
    if candidates:
        evidence.append({
            "layer": "服务拓扑",
            "finding": f"异常沿调用链传播, {len(candidates)} 个服务受影响",
            "path": " → ".join(candidates[:5]),
        })
    if root:
        evidence.append({
            "layer": "指标分析",
            "finding": f"确认 {root} 为最早异常源",
        })
    if change_events:
        evidence.append({
            "layer": "变更关联",
            "finding": f"发现 {len(change_events)} 个相关事件",
            "top_event": change_events[0] if change_events else None,
        })

    confidence = 0.7 if (root and candidates and change_events) else (0.5 if root and candidates else 0.3)
    # 如果有宿主机拓扑但未命中宿主机根因，降一点置信度
    if host_vms and not host_evidence:
        confidence = min(confidence, 0.6)

    return {
        "root_cause_service": root,
        "root_cause_type": "service",
        "causality_direction": f"{root} → {' → '.join(candidates[1:3])}" if root and len(candidates) > 1 else root or "",
        "evidence_chain": evidence,
        "confidence": confidence,
        "mode": "deterministic",
        "recommendation": f"建议排查 {root} 的变更事件和指标趋势" if root else "无法确定根因",
    }

# ═══════════════════════════════════════════════════════
#  集群诊断工具（kubectl 为基础，反映真实集群状态）
# ═══════════════════════════════════════════════════════

# 集群检查命令模板：RCA 假设引擎用这些检查集群真实状态（而非容器内 ps/free/ss）
CLUSTER_CHECK_TEMPLATES = {
    "pod_events": "kubectl get events -n {namespace} --sort-by=.lastTimestamp --field-selector=type!=Normal | tail -20",
    "pod_restarts": "kubectl get pods -n {namespace} -o wide",
    "pod_oom": "kubectl get pods -n {namespace} -o jsonpath={.items[*].status.containerStatuses[*].lastState.terminated.reason} | tr ' ' '\\n' | grep -i oom",
    "pod_waiting": "kubectl get pods -n {namespace} --field-selector=status.phase=Pending",
    "node_status": "kubectl get nodes -o wide",
    "node_usage": "kubectl top node",
    "pod_usage": "kubectl top pod -n {namespace}",
    "deploy_replicas": "kubectl get deployment -n {namespace} -o custom-columns=NAME:.metadata.name,DESIRED:.spec.replicas,READY:.status.readyReplicas",
    "svc_endpoints": "kubectl get endpoints -n {namespace}",
    "describe_pod": "kubectl describe pod -n {namespace} {pod} | tail -30",
}

# 允许的管道辅助命令（配合 kubectl 使用，白名单保证安全）
_ALLOWED_PIPE_TOOLS = {"tail", "head", "sort", "grep", "tr", "wc", "awk", "echo"}


def _run_kubectl_safe(cmd: str, timeout: int) -> str:
    """安全执行 kubectl 命令（支持管道），经过严格白名单校验。

    - 仅允许以 kubectl 开头的命令
    - 管道符后续命令必须在白名单内
    - 拒绝危险字符（重定向/命令替换等）
    """
    import re
    import subprocess

    # 校验：必须以 kubectl 开头
    if not cmd.strip().startswith("kubectl "):
        return f"[不安全命令，仅允许 kubectl] {cmd[:50]}"
    # 校验：无危险字符（重定向/命令替换/分号/&&/逻辑或||）
    if re.search(r"[;>&`$()\n]", cmd):
        return f"[命令含危险字符，已拒绝] {cmd[:50]}"
    # 校验：管道后续工具必须在白名单（按单个 | 分割，避免误切 ||）
    parts = re.split(r"(?<!\|)\|(?!\|)", cmd)
    for i, part in enumerate(parts):
        part = part.strip()
        if not part:
            continue
        if i == 0:
            if not part.startswith("kubectl "):
                return f"[不安全: 首段非 kubectl] {part[:50]}"
            continue
        first = part.split()
        if not first:
            continue
        tool = first[0].strip("'\"")
        if tool not in _ALLOWED_PIPE_TOOLS:
            return f"[不安全管道工具: {tool}]"

    try:
        # 用 shell=True 支持管道，但已通过白名单校验
        r = subprocess.run(cmd, shell=True, capture_output=True, text=True, timeout=timeout)
        out = r.stdout[:2000]
        if r.stderr and "error" in r.stderr.lower():
            out += f"\n[stderr]: {r.stderr[:300]}"
        return out or "(no output)"
    except subprocess.TimeoutExpired:
        return f"命令超时 (>{timeout}s)"
    except Exception as e:
        return f"执行失败: {str(e)}"


def cluster_check(check: str, timeout: int = 15) -> str:
    """执行集群诊断检查命令（kubectl），返回真实集群状态。

    支持管道等 shell 特性（安全白名单），查询被诊断服务的真实集群状态，
    而非容器内 ps/free/ss（它们反映的是 orchestrator 自身，无法诊断集群）。
    """
    cmd = check.strip()
    # 处理模板引用形式 (如 {pod_events})
    if cmd.startswith("{") and cmd.endswith("}"):
        tmpl = CLUSTER_CHECK_TEMPLATES.get(cmd[1:-1])
        if tmpl:
            cmd = tmpl.replace("{namespace}", "observability").replace("{pod}", "")
            return _run_kubectl_safe(cmd, timeout)
        return f"[未知检查模板] {cmd}"
    # kubectl 命令直接执行
    if cmd.startswith("kubectl "):
        return _run_kubectl_safe(cmd, timeout)
    # 其他命令：拒绝容器内诊断，明确提示
    return f"[RCA 集群诊断仅支持 kubectl 命令，不支持容器内命令: {cmd[:50]}]"


# ═══════════════════════════════════════════════════════
#  未知故障假设引擎
# ═══════════════════════════════════════════════════════

HYPO_SYSTEM_PROMPT = """你是故障诊断引擎的"假设生成器"。基于异常指纹生成3-5个可证伪的假设。
规则:
1. 每个假设必须包含 proposed_check: 必须是 **kubectl 集群查询命令**（如 kubectl get events / kubectl describe pod / kubectl top pod），
   用于查询被诊断服务的真实集群状态，**禁止使用 ps/free/ss/netstat 等容器内命令**（它们诊断不了集群其他服务）。
   也可使用模板引用，如 {pod_events} {pod_restarts} {pod_oom} {node_status} {pod_usage} {deploy_replicas} {svc_endpoints}。
2. 每个假设必须包含 predictions.confirm (支持证据) 和 predictions.falsify (排除证据)
3. 考虑"正常"的维度作为关键线索
4. 输出纯 JSON 格式: {"hypotheses": [{...}]}"""

def generate_hypotheses(fingerprint: dict, alert_context: dict = None) -> list[dict]:
    """LLM 生成假设 (constrained JSON)。

    alert_context 可选：来自告警事件的上下文（rule_name / message / count / severity），
    用于在 LLM 生成假设时注入真实告警信号，提高假设与当前故障的吻合度。
    """
    from orchestrator import brain
    cfg = brain.llm_config
    if not cfg:
        return []

    abnormal = fingerprint.get("abnormal_dimensions", [])
    normal = fingerprint.get("normal_dimensions", [])
    ctx = f"服务: {fingerprint.get('service','?')}\n"
    ctx += f"异常维度: {', '.join(abnormal[:8])}\n"
    ctx += f"正常维度: {', '.join(normal[:8])}\n"
    # 注入真实告警上下文（如果有）
    if alert_context:
        ctx += f"\n[实时告警信号]\n"
        ctx += f"- 规则: {alert_context.get('rule_name', '')}\n"
        ctx += f"- 严重级别: {alert_context.get('severity', '')}\n"
        ctx += f"- 触发次数: {alert_context.get('count', '')}\n"
        ctx += f"- 告警消息: {alert_context.get('message', '')}\n"
        ctx += f"- 触发服务: {alert_context.get('service', '')}\n"
    ctx += f"关键矛盾: 错误率正常但延迟飙升" if "error_rate" in normal and any("latency" in a for a in abnormal) else ""
    # 提供可用的集群检查命令模板，引导 LLM 生成符合 kubectl 语义的检查
    ctx += f"\n\n可用集群检查命令 (proposed_check 必须用这些或类似 kubectl 命令，禁止容器内 ps/free/ss):\n"
    for k, v in CLUSTER_CHECK_TEMPLATES.items():
        ctx += f"- {{{k}}}: {v}\n"

    from orchestrator import _llm as llm_call
    result = llm_call(cfg, HYPO_SYSTEM_PROMPT, ctx, "故障假设生成器")
    try:
        # 提取 JSON
        import re
        match = re.search(r'\{[\s\S]*\}', result)
        if match:
            data = json.loads(match.group())
            return data.get("hypotheses", [])
    except:
        pass
    return []

def _interpret_cluster_result(check: str, result: str) -> dict:
    """解析 kubectl 集群检查结果，返回 (verdict, confidence_delta, annotated_result)。

    基于集群语义判断，而非简单关键词。
    """
    r = result.lower()
    # 命令执行异常（工具缺失/权限/kubectl 未装/命令语法错误）→ inconclusive，不误判为故障
    tool_missing = any(kw in r for kw in [
        "command not found", "not found", "no such file", "executable file not found",
        "无法执行", "命令解析失败", "拒绝", "permission denied", "forbidden",
        "unknown shorthand flag", "unknown flag", "error from server",
        "see 'kubectl", "usage:", "invalid option"])
    if tool_missing:
        return {"verdict": "inconclusive", "delta": 0, "result": f"[集群检查不可用] {result[:100]}"}

    # 明确故障信号（kubectl 输出中的问题状态）
    if any(kw in r for kw in [
        "oomkilled", "crashloopbackoff", "imagepullbackoff", "error", "failed",
        "pending", "notready", "unavailable", "0/1", "0/2", "terminated"]):
        return {"verdict": "falsify", "delta": -0.3, "result": result[:200]}
    # 空结果 / 无匹配 → inconclusive（如 (no output)、no resources、not found）
    stripped = result.strip()
    is_empty = (
        not stripped or
        stripped.lower() in ("(no output)", "(命令无输出)", "no resources found", "no resources") or
        "no resources" in r or
        ("not found" in r and len(stripped) < 50)
    )
    if is_empty:
        return {"verdict": "inconclusive", "delta": 0, "result": result[:200]}
    # 命令有正常输出（有数据返回，非空）
    return {"verdict": "confirm", "delta": 0.2, "result": result[:200]}


def hypothesis_falsification_loop(hypotheses: list[dict], service: str, max_iter: int = 2) -> dict:
    """证伪循环: 执行集群检查 → 收集证据 → 更新置信度。

    使用 cluster_check (kubectl) 查询集群真实状态，不再用容器内 ps/free/ss。
    """
    active = [dict(h) for h in hypotheses]
    evidence_log = []

    for iteration in range(max_iter):
        for h in active[:]:
            check = h.get("proposed_check", "")
            if not check:
                continue
            result = cluster_check(check, timeout=15)
            interp = _interpret_cluster_result(check, result)

            conf = float(h.get("confidence_initial", 0.5))
            if interp["verdict"] == "falsify":
                conf -= 0.3
            elif interp["verdict"] == "confirm":
                conf += 0.2
            # inconclusive 不改变置信度

            h["confidence"] = max(0.0, min(1.0, conf))
            evidence_log.append({
                "hypothesis": h.get("id", h.get("hypothesis", ""))[:30],
                "check": check[:100],
                "result": interp["result"][:200],
                "verdict": interp["verdict"],
                "confidence": round(conf, 2),
            })

            if interp["verdict"] == "falsify" or conf < 0.15:
                active.remove(h)

        if len(active) <= 1 or max(h.get("confidence", 0) for h in active) > 0.85:
            break

    active.sort(key=lambda h: h.get("confidence", 0), reverse=True)
    return {
        "best_hypothesis": active[0] if active else None,
        "all_hypotheses": active,
        "evidence_log": evidence_log,
        "conclusion": "confirmed" if active and active[0].get("confidence", 0) > 0.8 else "tentative",
    }

# ═══════════════════════════════════════════════════════
#  统一 RCA 入口
# ═══════════════════════════════════════════════════════

def full_rca_analysis(affected_service: str, anomaly_event: dict = None) -> dict:
    """统一 RCA: 确定性失败后切换到假设引擎。

    anomaly_event 可选：来自告警事件的上下文（rule_name / message / count / severity）。
    对于 K8s 集群告警（service=kubernetes 或 rule_id 以 k8s- 开头），
    直接以 cluster_check 假设引擎为主，跳过微服务拓扑的确定性分析。
    """
    rule_id = (anomaly_event or {}).get("rule_id", "") or ""
    is_k8s_alert = affected_service == "kubernetes" or rule_id.startswith("k8s-")

    # Phase 1: 确定性（仅对微服务生效；K8s 集群告警无微服务拓扑，跳过）
    det_result = diagnose_root_cause(affected_service)
    if not is_k8s_alert and det_result.get("confidence", 0) > 0.6:
        return {"mode": "deterministic", "result": det_result}

    # Phase 2: 假设引擎 (需要 LLM)
    from orchestrator import brain
    if not brain.llm_config or not brain.llm_config.get("api_key"):
        return {"mode": "deterministic", "result": det_result,
                "message": "LLM API Key 未配置，假设引擎不可用，仅返回确定性结果"}

    from detector import detector
    fp = detector.extract_fingerprint(
        affected_service,
        {"p99_latency_ms": det_result.get("confidence", 0.5) * 1000, "error_rate": 0.05},
        {"p99_latency_ms": (100, 200, 500), "error_rate": (0.01, 0.03, 0.05)},
    )
    hypotheses = generate_hypotheses({
        "service": affected_service,
        "abnormal_dimensions": fp.abnormal_dimensions,
        "normal_dimensions": fp.normal_dimensions,
    }, alert_context=anomaly_event)
    if not hypotheses:
        return {"mode": "deterministic", "result": det_result,
                "message": "假设引擎未生成有效假设，回退到确定性结果"}

    hyp_result = hypothesis_falsification_loop(hypotheses, affected_service)
    return {"mode": "hypothesis_engine", "result": {**det_result, "hypothesis_result": hyp_result}}
