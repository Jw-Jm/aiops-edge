"""LLM mock 通道：独立纯逻辑模块，避免触发 orchestrator 的重依赖导入链。

通过环境变量 LLM_MOCK 控制（"true"/"1"/"yes" 视为开启）。P2-R5: mock 为显式
opt-in，默认关闭；本地联调由 .env.local/compose 显式开启，生产必须保持关闭。
"""

import os


def is_mock_enabled() -> bool:
    return os.getenv("LLM_MOCK", "").lower() in ("true", "1", "yes")


def mock_llm_response(prompt: str) -> str:
    """LLM mock：返回预设诊断文本，便于界面联调，不消耗真实模型。

    文本含 "[mock] analysis" 标记，便于测试与下游程序稳定识别 mock 结果。
    """
    return (
        "[mock] analysis: 已生成根因分析，从指标与拓扑看，可能为最近一次发布引起的调用异常。\n"
        f"待分析内容：{prompt[:200]}"
    )


def should_skip_llm(cfg) -> bool:
    """判断是否应跳过 LLM 调用。

    mock 开启时返回 False（不跳过，让 _llm 短路返回 mock 结果，即使未配置 api_key）；
    否则在缺少 api_key 时返回 True（跳过，返回空）。
    """
    if is_mock_enabled():
        return False
    return not cfg or not cfg.get("api_key")


# ═══════════════════════════════════════════════════════
#  批3: function-calling / 双层 Agent mock 决策
# ═══════════════════════════════════════════════════════
_MOCK_TOOL_SEQUENCE = ["query_metrics"]


def mock_llm_decision(messages, tools):
    """模拟 LLM function-calling 决策：先调工具，最后给出最终结论。

    messages 中"调用工具"的 assistant 消息数量决定进度；调用完预设工具序列后返回 final。
    """
    already_called = sum(
        1 for m in messages if m.get("role") == "assistant" and "工具" in m.get("content", "")
    )
    if already_called < len(_MOCK_TOOL_SEQUENCE):
        name = _MOCK_TOOL_SEQUENCE[already_called]
        args = {"service": "unknown"} if name == "query_metrics" else {}
        return {"type": "tool", "name": name, "arguments": args}
    return {
        "type": "final",
        "content": "[mock] 双层Agent诊断完成：基于指标与拓扑，疑似最近一次发布引起的调用异常，建议回滚。",
    }


def mock_coordinator_plan():
    """mock Coordinator 拆解：返回预设 2-3 个子任务。"""
    return [
        {"task_id": "t1", "task_type": "diagnosis", "target_service": "unknown", "query": "排查服务异常根因"},
        {"task_id": "t2", "task_type": "inspection", "target_service": "unknown", "query": "巡检服务健康状态"},
    ]


def mock_reviewer_result(sub_results):
    """mock Reviewer 合并审查：拼接所有子结论并给质量结论。"""
    parts = []
    for tid, r in (sub_results or {}).items():
        parts.append(f"[{tid}] {r.get('conclusion', '')[:200]}")
    body = "\n".join(parts) if parts else "(无子结论)"
    return "[mock] Reviewer 审查通过：子结论一致，无冲突。合并如下:\n" + body
