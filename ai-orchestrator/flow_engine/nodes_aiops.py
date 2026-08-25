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
    request_context = ctx.get("request_context") if isinstance(ctx, dict) else None
    cluster_id = str(request_context.cluster_id) if request_context is not None else ""
    try:
        from tools import query_metrics, query_traces, query_topology, get_service_list
    except Exception:
        return {"service": svc, "services": "(工具不可用)", "red": "", "traces": "", "topology": ""}
    # 每个工具调用单独 try/except：query-api 不可达/超时/异常都不应让工作流节点失败，
    # 而是返回错误文本，保证流程可继续（数据源不可用时降级为明确提示）。
    red = ""
    try:
        red = (
            query_metrics(svc, cluster_id=cluster_id, request_context=request_context)
            if svc else get_service_list(
                cluster_id=cluster_id, request_context=request_context
            )
        )
    except Exception as e:
        red = f"(数据源不可用: {e})"
    traces = ""
    if svc:
        try:
            traces = query_traces(
                svc, cluster_id=cluster_id, request_context=request_context
            )
        except Exception:
            traces = ""
    topology = ""
    try:
        topology = query_topology(
            cluster_id=cluster_id, request_context=request_context
        )
    except Exception:
        topology = ""
    return {"service": svc, "services": red, "red": red, "traces": traces,
            "topology": topology, "infra": "", "alerts": ""}


def _clean(ctx, config):
    return {}


def _rca(ctx, config):
    """RCA 证据链：图谱证据 + 指标异常 → 候选排序（带证据/置信度/验证动作）。"""
    collected = _collect_out(ctx)
    svc = config.get("service", "") or collected.get("service", "")
    candidates = []
    try:
        from kg_tools import kg_evidence_tool  # 函数内 import 避免循环依赖
        kg_ev = kg_evidence_tool(svc) if svc else ""
    except Exception:
        kg_ev = ""
    red = str(collected.get("red", "") or "")
    err_hit = ("error" in red.lower()) or ("错误" in red) or ("error_rate" in red)
    if kg_ev and "暂不可用" not in kg_ev:
        if err_hit:
            candidates.append({"candidate": f"{svc} 存在依赖链/变更异常(图谱证据+指标异常)",
                               "evidence": kg_ev[:400] + " | RED: " + red[:200],
                               "confidence": 0.7,
                               "verify_action": "沿图谱依赖链检查关联变更与中间件指标"})
        else:
            candidates.append({"candidate": f"{svc} 存在依赖链/变更异常(图谱证据)",
                               "evidence": kg_ev[:500],
                               "confidence": 0.55,
                               "verify_action": "核对图谱关联变更与上游依赖状态"})
    elif err_hit:
        candidates.append({"candidate": f"{svc} 错误率/延迟指标异常",
                           "evidence": red[:400],
                           "confidence": 0.4,
                           "verify_action": "查看 trace 与日志定位错误来源"})
    if not candidates:
        candidates.append({"candidate": "未发现明确异常证据", "evidence": "",
                           "confidence": 0.2,
                           "verify_action": "扩大时间窗口与指标范围复查"})
    candidates.sort(key=lambda c: -c["confidence"])
    top = candidates[0]
    return {"mode": "evidence_chain", "root_cause": top["candidate"],
            "evidence": top["evidence"], "confidence": top["confidence"],
            "candidates": candidates}


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
    """Emit an action proposal; never execute a shell command in this runtime.

    Mutation is owned by query-api → ai-action-executor.  Keeping this legacy
    node proposal-only prevents a flow definition or approval flag from
    bypassing the signed Action boundary.
    """
    script = config.get("script", "")
    if not script:
        return {"output": "(无脚本)", "status": "inconclusive",
                "error_code": "ACTION_EXECUTOR_REQUIRED"}
    return {
        "output": "",
        "status": "proposed",
        "error_code": "ACTION_EXECUTOR_REQUIRED",
        "action_proposal": {"script": str(script)[:2000]},
    }


def _verify(ctx, config):
    """Verify through a fresh read-only observation.

    A workflow node may propose an action, but it must not turn a missing target
    or unavailable data source into a successful verdict.  The observer reads
    the current service metrics independently of the execute node and returns a
    four-state outcome consumed by the report/UI layer.
    """
    import json

    collected = _collect_out(ctx)
    service = str(config.get("service") or collected.get("service") or "").strip()
    if not service:
        return {
            "pass": False,
            "status": "inconclusive",
            "error_code": "VERIFICATION_INCONCLUSIVE",
            "after_metrics": "",
            "summary": "无法验证：未绑定目标服务",
        }

    request_context = None
    if isinstance(getattr(ctx, "vars", None), dict):
        request_context = ctx.vars.get("request_context")
    cluster_id = str(getattr(request_context, "cluster_id", "") or "")
    try:
        from tools import query_metrics
        raw = query_metrics(service, cluster_id=cluster_id,
                            request_context=request_context)
    except Exception as exc:
        return {
            "pass": False,
            "status": "inconclusive",
            "error_code": "VERIFICATION_SOURCE_UNAVAILABLE",
            "after_metrics": "",
            "summary": f"验证数据源不可用: {str(exc)[:200]}",
        }

    def _snapshot(value):
        try:
            payload = json.loads(value) if isinstance(value, str) else value
        except Exception:
            return None
        if not isinstance(payload, dict):
            return None
        rows = payload.get("data")
        if isinstance(rows, dict):
            rows = rows.get("data")
        if not isinstance(rows, list) or not rows:
            return None
        try:
            calls = sum(float(row.get("calls", 0) or 0) for row in rows if isinstance(row, dict))
            errors = sum(float(row.get("errors", 0) or 0) for row in rows if isinstance(row, dict))
            latency = sum(float(row.get("avg_ms", 0) or 0) for row in rows if isinstance(row, dict)) / len(rows)
            return {"latency": latency, "error_rate": errors / max(calls, 1) * 100}
        except (TypeError, ValueError):
            return None

    before = _snapshot(collected.get("red") or collected.get("services"))
    after = _snapshot(raw)
    if before is None or after is None:
        return {
            "pass": False,
            "status": "inconclusive",
            "error_code": "VERIFICATION_INCONCLUSIVE",
            "after_metrics": str(raw)[:1000],
            "summary": "验证窗口缺少可比较的 before/after 指标",
        }

    regressed = (after["latency"] > before["latency"] * 1.2 or
                 after["error_rate"] > before["error_rate"] + 1.0)
    passed = (after["latency"] <= before["latency"] and
              after["error_rate"] <= before["error_rate"])
    status = "regressed" if regressed else "passed" if passed else "partial"
    return {
        "pass": passed,
        "status": status,
        "error_code": "" if passed else "VERIFICATION_NOT_PASSED",
        "before_metrics": before,
        "after_metrics": after,
        "summary": f"before={before}, after={after}",
    }


def _report(ctx, config):
    """报告节点：基于采集数据生成真实报告文本（非 mock），并持久化到报告中心。"""
    co = _collect_out(ctx)
    svc = co.get("service", "")
    red = co.get("red") or "(无指标数据)"
    exec_out = _node_out(ctx, "execute").get("output", "")
    report_text = f"[{_now()}] 服务 {svc} 诊断报告\n- 指标: {red[:300]}\n- 执行结果: {str(exec_out)[:300]}"
    # P0-1 修复: 工作流报告节点输出后持久化到报告中心（MySQL reports 表 + 本地留档）
    # 与 orchestrator 的 _upload_report 复用同一持久化逻辑，确保工作流报告被报告中心收纳
    try:
        from main import _upload_report
        run_id = getattr(ctx, "run_id", "") or config.get("run_id", "")
        _upload_report(run_id, report_text, service=svc or "")
    except Exception as _e:
        try:
            print(f"[flow] 报告持久化失败: {_e}")
        except Exception:
            pass
    return {"report": report_text}


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
