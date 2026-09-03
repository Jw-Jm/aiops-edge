"""Task3: K8s 集群告警的 RCA 针对性修复。

验证:
1. 对 k8s-pod-crash 告警，full_rca_analysis 不再把 kubernetes 当微服务查拓扑，
   处置方案应针对 Pod 重启（kubectl get pods / describe / logs），而非无关微服务。
2. 处置方案中不应出现 deepflow-grafana 等与告警无关的服务名。
"""
import json
import unittest
from unittest.mock import patch


def test_k8s_pod_crash_rca_targets_k8s_not_microservice():
    """k8s-pod-crash 告警：根因应指向 K8s Pod 异常，处置方案含 kubectl 命令，
    不应返回"排查某微服务"或 deepflow-grafana 等无关服务。"""
    from rca import full_rca_analysis

    # 告警上下文：k8s-pod-crash（Pod 频繁重启），service=kubernetes
    anomaly_event = {
        "service": "kubernetes",
        "rule_id": "k8s-pod-crash",
        "rule_name": "Pod 频繁重启",
        "severity": "critical",
        "message": "Pod 频繁重启: pod_restarts 7.00 > threshold 3.00",
        "count": 4893,
    }
    result = full_rca_analysis("kubernetes", anomaly_event=anomaly_event)

    # 归一化：可能返回 {"result": {...}} 或直接 {root_cause,...}
    res = result.get("result", result)
    root_cause = json.dumps(res, ensure_ascii=False)
    action = res.get("recommendation", "") or res.get("action", "")

    # 1. 处置方案应针对 K8s（kubectl 命令）
    assert "kubectl" in root_cause, f"K8s RCA 处置方案缺少 kubectl 命令: {action}"
    # 2. 根因应提到 Pod / 重启，而非无关微服务
    assert any(k in root_cause for k in ["Pod", "pod", "容器", "重启", "restart"]), \
        f"K8s RCA 根因未指向 Pod 异常: {root_cause}"
    # 3. 不应指向无关微服务（deepflow-grafana 等）
    assert "deepflow-grafana" not in root_cause, \
        f"K8s RCA 错误地指向无关微服务 deepflow-grafana: {root_cause}"


def test_k8s_oom_rca_targets_memory():
    """k8s-oom-killed 告警：处置方案应针对 OOM/内存，而非微服务拓扑。"""
    from rca import full_rca_analysis

    anomaly_event = {
        "service": "kubernetes",
        "rule_id": "k8s-oom-killed",
        "rule_name": "Pod OOMKilled",
        "severity": "critical",
        "message": "Pod OOMKilled: oom_count 1.00 > threshold 0.00",
        "count": 99,
    }
    result = full_rca_analysis("kubernetes", anomaly_event=anomaly_event)
    res = result.get("result", result)
    root_cause = json.dumps(res, ensure_ascii=False)
    assert "kubectl" in root_cause, f"OOM RCA 缺少 kubectl 命令: {root_cause}"
    assert any(k in root_cause for k in ["OOM", "内存", "Memory", "oom"]), \
        f"OOM RCA 未指向内存问题: {root_cause}"
    assert "deepflow-grafana" not in root_cause, \
        f"OOM RCA 错误指向无关微服务: {root_cause}"


def test_k8s_rca_with_llm_uses_hypothesis_engine():
    """配置 LLM 时，K8s RCA 走假设引擎（hypothesis_engine），体现重新分析的差异。"""
    from rca import full_rca_analysis

    anomaly_event = {
        "service": "kubernetes",
        "rule_id": "k8s-deployment-unavailable",
        "rule_name": "Deployment 不可用",
        "severity": "critical",
        "message": "Deployment 不可用: unavailable_replicas 2.00 > threshold 0.00",
        "count": 451,
    }
    # mock LLM 配置 + generate_hypotheses 返回有效假设
    with patch("orchestrator.brain") as mock_brain, \
         patch("rca.generate_hypotheses", return_value=[
             {"hypothesis": "Deployment 滚动更新失败导致副本不可用", "priority": 0.9,
              "proposed_check": "kubectl rollout status deploy/xxx -n observability"}
         ]), \
         patch("rca.hypothesis_falsification_loop", return_value={"conclusion": "确认滚动更新失败"}):
        mock_brain.llm_config = {"api_key": "test", "provider": "mock", "model": "mock"}
        mock_brain._llm = None
        result = full_rca_analysis("kubernetes", anomaly_event=anomaly_event)
        res = result.get("result", result)
        assert result.get("mode") == "hypothesis_engine", f"LLM 配置时应走假设引擎: {result.get('mode')}"
        assert "kubectl" in json.dumps(res, ensure_ascii=False), \
            f"K8s RCA 处置方案缺 kubectl: {res}"
        assert "deepflow-grafana" not in json.dumps(res, ensure_ascii=False)


def test_k8s_rca_differs_by_rule_name():
    """Issue4: 三个不同 K8s 告警（Deployment不可用/OOM/Pod重启）即使 rule_id 缺失，
    也能按 rule_name 反推，返回不同处置方案，避免三个根因分析内容一致。"""
    from rca import full_rca_analysis

    scenarios = [
        {"rule_id": "", "rule_name": "Pod 频繁重启", "expect": ["CrashLoopBackOff", "logs", "重启"]},
        {"rule_id": "", "rule_name": "Pod OOMKilled", "expect": ["OOM", "内存", "Memory"]},
        {"rule_id": "", "rule_name": "Deployment 不可用", "expect": ["rollout", "不可用", "Deployment"]},
    ]
    plans = []
    for s in scenarios:
        ev = {"service": "kubernetes", "rule_id": s["rule_id"], "rule_name": s["rule_name"],
              "severity": "critical", "message": s["rule_name"]}
        result = full_rca_analysis("kubernetes", anomaly_event=ev)
        res = result.get("result", result)
        txt = json.dumps(res, ensure_ascii=False)
        assert any(k in txt for k in s["expect"]), \
            f"rule_name={s['rule_name']} 处置方案未针对性: {txt[:200]}"
        plans.append(txt)
    # 三个方案的 root_cause/action 应互不相同（rule-specific）
    assert plans[0] != plans[1] != plans[2], \
        "三个 K8s 告警的根因分析内容仍一致"


if __name__ == "__main__":
    unittest.main()

def test_shell_string_input_redirection_channel_removed():
    """P1-S1 结构化改造：`<` 输入重定向 PoC（`kubectl get pods | sort < /etc/shadow`）
    所在的 shell 执行路径已整体移除，任意 shell 字符串一律拒绝。
    回归锁定: 白名单管道 + 文件参数读取通道不再存在。"""
    from rca import cluster_check

    for cmd in (
        "kubectl get pods | sort < /etc/shadow",
        "kubectl get pods | wc -l < /etc/passwd",
        "kubectl get pods < /etc/shadow",
        "kubectl get pods | head /proc/self/environ",
    ):
        out = cluster_check(cmd)
        assert "拒绝" in out, f"must reject: {cmd}"


def test_shell_pipe_strings_rejected_by_structured_interface():
    """P1-S1 设计变更（原测试断言"放行常规管道"已失效）:
    审核报告 §5.2 要求彻底移除 RCA 通用 shell——含白名单管道，
    LLM 只允许输出结构化检查 (kind/namespace/pod)。"""
    from rca import cluster_check

    out = cluster_check("kubectl version --client | head -1")
    assert "拒绝" in out
