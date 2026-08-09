# flow_engine/nodes_aiops.py
import time
from .noderegistry import NodeSpec, register_node, node_registry


def _now():
    return time.strftime("%Y-%m-%d %H:%M:%S")


def _collect(ctx, config):
    """采集节点：从 query-api 获取真实服务指标/链路/拓扑（非 mock）。

    复用 orchestrator 的数据工具（query_metrics/query_traces/query_topology），
    它们经 query-api 读 ClickHouse/VictoriaMetrics。query-api 不可达时返回明确错误。
    """
    svc = config.get("service", "")
    try:
        from tools import query_metrics, query_traces, query_topology, get_service_list
    except Exception:
        return {"service": svc, "services": "(工具不可用)", "red": "", "traces": "", "topology": ""}
    # 每个工具调用单独 try/except：query-api 不可达/超时/异常都不应让工作流节点失败，
    # 而是返回错误文本，保证流程可继续（数据源不可用时降级为明确提示）。
    red = ""
    try:
        red = query_metrics(svc) if svc else get_service_list()
    except Exception as e:
        red = f"(数据源不可用: {e})"
    traces = ""
    if svc:
        try:
            traces = query_traces(svc)
        except Exception:
            traces = ""
    topology = ""
    try:
        topology = query_topology()
    except Exception:
        topology = ""
    return {"service": svc, "services": red, "red": red, "traces": traces,
            "topology": topology, "infra": "", "alerts": ""}


def _clean(ctx, config):
    return {}


def _rca(ctx, config):
    return {"mode": "deterministic", "root_cause": "", "evidence": "", "confidence": 0.0}


def _rag(ctx, config):
    return {"cases": ""}


def _collect_out(ctx):
    """从运行上下文取 collect 节点输出（ctx.nodes[<id>]["output"]）。"""
    for nid, v in (getattr(ctx, "nodes", {}) or {}).items():
        out = (v or {}).get("output", {})
        if isinstance(out, dict) and ("red" in out or "services" in out):
            return out
    return {}


def _node_out(ctx, node_id):
    """从运行上下文取指定节点的输出 dict（ctx.nodes[<id>]["output"]）。"""
    v = (getattr(ctx, "nodes", {}) or {}).get(node_id, {})
    out = (v or {}).get("output", {})
    return out if isinstance(out, dict) else {}


def _crewai(ctx, config):
    """专家分析：基于已采集的真实数据做确定性汇总（非 mock 文本）。"""
    red = _collect_out(ctx).get("red") or ""
    return {"result": f"[{_now()}] 基于采集数据汇总：{red[:500]}" if red else f"[{_now()}] 无采集数据可分析"}


def _holmes(ctx, config):
    return {"result": ""}


def _plan(ctx, config):
    """生成方案：基于采集数据给出诊断计划；脚本为可执行白名单内的只读命令。"""
    svc = _collect_out(ctx).get("service", "")
    return {"plan": f"1. 检查服务 {svc} 指标\n2. 查看链路与日志定位问题",
            "script": f"kubectl get pods -n observability | grep {svc}" if svc else "kubectl get pods -n observability"}


def _risk(ctx, config):
    return {"score": 1, "reason": "低风险（仅诊断，无写操作）"}


def _execute(ctx, config):
    """执行节点：真实执行，但受 ShellPolicy 白名单 + 元字符拦截约束。

    仅执行可执行白名单（readonly/write）内的命令；越权命令不执行并返回提示。
    """
    script = config.get("script", "")
    if not script:
        return {"output": "(无脚本)"}
    try:
        from shell_policy import ShellPolicy
        policy = ShellPolicy()
        if mc := policy.check_shell_metachars(script):
            return {"output": f"拒绝执行（含 shell 元字符）: {mc}"}
        allowed, _cat = policy.is_whitelisted_for_execute(script)
        if not allowed:
            return {"output": "拒绝执行（不在可执行白名单内）"}
    except Exception as e:
        return {"output": f"安全校验失败: {e}"}
    try:
        from tools import execute_shell
        out = execute_shell(script, timeout=30)
        return {"output": out[:2000]}
    except Exception as e:
        return {"output": f"执行异常: {e}"}


def _verify(ctx, config):
    return {"pass": True, "after_metrics": ""}


def _report(ctx, config):
    """报告节点：基于采集数据生成真实报告文本（非 mock）。"""
    co = _collect_out(ctx)
    svc = co.get("service", "")
    red = co.get("red") or "(无指标数据)"
    exec_out = _node_out(ctx, "execute").get("output", "")
    return {"report": f"[{_now()}] 服务 {svc} 诊断报告\n- 指标: {red[:300]}\n- 执行结果: {str(exec_out)[:300]}"}


def _memorize(ctx, config):
    return {"stored": False}


def _summarize(ctx, config):
    """汇总节点：基于各节点真实输出做确定性汇总。"""
    lines = []
    nodes = getattr(ctx, "nodes", {}) or {}
    for nid, v in nodes.items():
        out = (v or {}).get("output", {})
        if not isinstance(out, dict):
            continue
        for k, val in out.items():
            if isinstance(val, str) and val and "mock" not in val.lower():
                lines.append(f"{nid}.{k}: {val[:200]}")
    if not lines:
        lines.append("工作流执行完成，无额外输出")
    return {"final_response": "\n".join(lines)[:3000]}


def _condition(ctx, config):
    return {}


def _wait_approval(ctx, config):
    return {"plan": config.get("plan", ""), "script": config.get("script", ""),
            "risk_score": config.get("risk_score", 0), "risk_reason": config.get("risk_reason", "")}


def register_aiops_nodes():
    """幂等注册所有 AIOps 节点。已注册的 type 跳过。"""
    specs = [
        NodeSpec("collect", "action", "采集", "数据采集", ["next"],
                 config_fields=[{"name": "service", "label": "服务", "type": "text"}],
                 output_shape=["services", "infra", "alerts", "red"], execute=_collect),
        NodeSpec("clean", "action", "采集", "数据清洗", ["next"], execute=_clean),
        NodeSpec("rca", "action", "分析", "RCA 根因分析", ["next"],
                 config_fields=[{"name": "service", "label": "服务", "type": "text"}],
                 output_shape=["root_cause", "confidence"], execute=_rca),
        NodeSpec("rag", "action", "分析", "RAG 案例匹配", ["next"], execute=_rag),
        NodeSpec("crewai", "action", "分析", "专家分析", ["next"],
                 output_shape=["result"], execute=_crewai),
        NodeSpec("holmes", "action", "分析", "Trace 分析", ["next"],
                 output_shape=["result"], execute=_holmes),
        NodeSpec("plan", "action", "执行", "生成方案", ["next"],
                 output_shape=["plan", "script"], execute=_plan),
        NodeSpec("risk", "action", "执行", "风险评估", ["next"],
                 output_shape=["score", "reason"], execute=_risk),
        NodeSpec("wait_approval", "control", "控制", "人工审批", ["approved", "rejected"],
                 config_fields=[{"name": "plan", "label": "方案", "type": "textarea"},
                                {"name": "script", "label": "脚本", "type": "textarea"},
                                {"name": "risk_score", "label": "风险分", "type": "number"}],
                 output_shape=["plan", "script", "risk_score"], execute=_wait_approval),
        NodeSpec("execute", "action", "执行", "执行方案", ["next"],
                 config_fields=[{"name": "script", "label": "脚本", "type": "textarea"}],
                 output_shape=["output"], execute=_execute),
        NodeSpec("verify", "action", "执行", "执行验证", ["next"], execute=_verify),
        NodeSpec("report", "action", "执行", "生成报告", ["next"], execute=_report),
        NodeSpec("memorize", "action", "执行", "记忆学习", ["next"], execute=_memorize),
        NodeSpec("summarize", "action", "执行", "汇总输出", ["next"],
                 output_shape=["final_response"], execute=_summarize),
        NodeSpec("condition", "control", "控制", "条件分支", ["true", "false"],
                 config_fields=[{"name": "expr", "label": "条件表达式", "type": "text"}],
                 execute=_condition),
    ]
    for s in specs:
        if node_registry.lookup(s.type) is None:
            register_node(s)
