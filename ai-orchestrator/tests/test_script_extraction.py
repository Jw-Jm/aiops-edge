"""Issue5: 处置建议卡必须给出具体可执行命令，而非只罗列分析报告。

验证:
1. _fallback_script 对纯分析报告（无命令）能生成确定性可执行 kubectl 命令
2. _extract_script 能从 LLM 的『## 处置命令』小节提取命令
3. _extract_script 能从 ```bash 代码块提取命令
4. _action_summary 生成简洁摘要（含命令），不罗列整篇报告
"""
import json
import unittest


def test_fallback_script_from_prose_analysis():
    """纯分析报告（无命令）→ 应生成可执行的 kubectl 命令。"""
    from orchestrator import _fallback_script

    report = ("检测到 deepflow-server 的 Pod 频繁重启，restartCount 达到 7 次，"
              "可能由容器崩溃、资源限制或探针失效导致。建议排查异常 Pod 的日志与状态。")
    script = _fallback_script(report, "deepflow-server")
    assert "kubectl" in script, f"兜底脚本应含 kubectl: {script}"
    assert "deepflow-server" in script, f"兜底脚本应含服务名: {script}"
    # 应是可执行命令行，而非报告文本
    for line in script.splitlines():
        assert line.strip().startswith("kubectl"), f"每行都应是 kubectl 命令: {line}"


def test_extract_script_from_remdiation_section():
    """LLM 输出『## 处置命令』小节 → 应提取出 kubectl 命令。"""
    from orchestrator import _extract_script

    text = (
        "分析结论：Deployment 存在不可用副本，需要重启恢复。\n\n"
        "## 处置命令\n"
        "kubectl rollout restart deployment/order-svc -n observability\n"
        "kubectl get pods -n observability | grep order-svc\n"
    )
    script = _extract_script(text)
    assert "rollout restart" in script, f"应提取处置命令: {script}"
    assert "grep order-svc" in script, f"应提取第二条命令: {script}"


def test_extract_script_from_codeblock():
    """LLM 输出 ```bash 代码块 → 应提取命令。"""
    from orchestrator import _extract_script

    text = "分析：\n```bash\nkubectl get pods -n observability\nkubectl describe pod -l app=foo -n observability\n```"
    script = _extract_script(text)
    assert "kubectl get pods" in script and "kubectl describe" in script, f"应提取代码块命令: {script}"


def test_action_summary_is_concise():
    """_action_summary 生成简洁摘要（目标+命令+一句依据），不罗列整篇报告。"""
    from orchestrator import _action_summary

    script = "kubectl get pods -n observability -l app=order-svc"
    long_report = "这是一段很长的分析报告，" * 50
    summary = _action_summary(script, long_report, "order-svc")
    assert "order-svc" in summary
    assert "kubectl get pods" in summary
    # 摘要长度应远小于报告全文（不罗列整篇）
    assert len(summary) < 1000, f"摘要应简洁: len={len(summary)}"
    assert "依据" in summary


if __name__ == "__main__":
    unittest.main()
