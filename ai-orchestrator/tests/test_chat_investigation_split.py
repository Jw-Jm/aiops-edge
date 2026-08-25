"""C2-1/C2-2（CONTROLLED_AI_INVESTIGATION_CANDIDATE）：Chat/Investigation 边界测试。

覆盖：
  1. 普通 Chat 无固定实时采集——纯闲聊 → chat_pure=True（跳过 collect heavy 采集）。
  2. 实时诊断 → investigation_required CTA（不固定采集，走显式 Run）。
  3. 普通诊断/查询 → 走正常 Chat 链路（不短路，保留 exec_context）。
"""

import pytest

from orchestrator import (
    _is_pure_conversation,
    _needs_investigation_cta,
    node_chat_classify,
)


@pytest.mark.asyncio
async def test_pure_conversation_no_fixed_collection():
    """C2-1：纯闲聊/信息查询 → chat_pure=True（跳过 heavy collect，不固定实时采集）。"""
    r = await node_chat_classify({"user_message": "你好，介绍一下你能做什么"})
    assert r.get("chat_pure") is True
    assert r.get("investigation_required") is False


@pytest.mark.asyncio
async def test_live_diagnosis_requires_explicit_run():
    """C2-2：明确要求结构化调查 → investigation_required CTA（不固定采集，走显式 Run）。"""
    r = await node_chat_classify({"user_message": "发起调查分析 order-svc 错误率"})
    assert r.get("investigation_required") is True
    assert r.get("chat_pure") is False
    assert "__investigation_required__" in r.get("final_response", "")


@pytest.mark.asyncio
async def test_normal_diagnosis_goes_normal_chat():
    """普通诊断/查询不短路（保留 exec_context/处置分析能力），但非 chat_pure。"""
    r = await node_chat_classify({"user_message": "诊断 foo 服务延迟"})
    assert r.get("chat_pure") is False
    assert r.get("investigation_required") is False


def test_pure_conversation_keywords():
    assert _is_pure_conversation("你好") is True
    assert _is_pure_conversation("谢谢") is True
    assert _is_pure_conversation("诊断 foo 服务") is False


def test_investigation_cta_keywords():
    assert _needs_investigation_cta("发起调查") is True
    assert _needs_investigation_cta("创建调查") is True
    assert _needs_investigation_cta("完整根因分析") is True
    assert _needs_investigation_cta("诊断 foo 服务") is False
