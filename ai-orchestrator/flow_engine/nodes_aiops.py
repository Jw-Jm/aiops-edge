# flow_engine/nodes_aiops.py
import time
from .noderegistry import NodeSpec, register_node, node_registry


def _now():
    return time.strftime("%Y-%m-%d %H:%M:%S")


def _collect(ctx, config):
    svc = config.get("service", "")
    return {"service": svc, "services": f"[mock] 服务={svc} 调用量=1200 错误率=2.1%",
            "infra": "(未采集)", "alerts": "", "red": "", "traces": "", "k8sgpt": ""}


def _clean(ctx, config):
    return {}


def _rca(ctx, config):
    return {"mode": "deterministic", "root_cause": "", "evidence": "", "confidence": 0.0}


def _rag(ctx, config):
    return {"cases": ""}


def _crewai(ctx, config):
    return {"result": f"[mock] {_now()} 专家分析：服务状态基本健康，建议关注错误率。"}


def _holmes(ctx, config):
    return {"result": ""}


def _plan(ctx, config):
    return {"plan": "1. 检查服务日志\n2. 观察错误率趋势", "script": "kubectl get po -n observability"}


def _risk(ctx, config):
    return {"score": 1, "reason": "低风险"}


def _execute(ctx, config):
    return {"output": "(mock 执行，未真正运行命令)"}


def _verify(ctx, config):
    return {"pass": True, "after_metrics": ""}


def _report(ctx, config):
    return {"report": "(mock 报告)"}


def _memorize(ctx, config):
    return {"stored": False}


def _summarize(ctx, config):
    return {"final_response": "(mock 汇总报告)"}


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
