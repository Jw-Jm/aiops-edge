"""LLM mock 通道：独立纯逻辑模块，避免触发 orchestrator 的重依赖导入链。

通过环境变量 LLM_MOCK 控制（"true"/"1"/"yes" 视为开启）。本机部署联调默认开启，
生产接真实模型时设 LLM_MOCK=false。
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
