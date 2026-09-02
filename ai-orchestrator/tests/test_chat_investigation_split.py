"""C2-1/C2-2（CONTROLLED_AI_INVESTIGATION_CANDIDATE）：Chat/Investigation 边界测试。

覆盖：
  1. 普通 Chat 无固定实时采集——纯闲聊 → chat_pure=True（跳过 collect heavy 采集）。
  2. 实时诊断 → investigation_required CTA（不固定采集，走显式 Run）。
  3. 普通诊断/查询 → 走正常 Chat 链路（不短路，保留 exec_context）。
"""

import pytest
import asyncio

from orchestrator import (
    BrainOrchestrator,
    _is_pure_conversation,
    _needs_investigation_cta,
    node_chat_classify,
)
from invocation_scope import ScopeViewSnapshot


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


def test_graph_correlation_requests_require_investigation_cta():
    assert _needs_investigation_cta("查看 checkout 的上下游依赖关系") is True
    assert _needs_investigation_cta("查询知识图谱") is True


def test_stream_emits_investigation_cta_from_chat_classify(monkeypatch, tmp_path):
    """The SSE path must not drop the CTA when classify terminates the graph."""

    class _ClassifyOnlyGraph:
        async def astream(self, *_args, **_kwargs):
            yield {
                "chat_classify": {
                    "investigation_required": True,
                    "chat_pure": False,
                    "final_response": "__investigation_required__\n请创建调查",
                }
            }

    brain = BrainOrchestrator(db_path=str(tmp_path / "chat.db"))
    async def _noop_ensure():
        return None
    monkeypatch.setattr(brain, "_ensure_async_checkpointer", _noop_ensure)
    brain.chat_graph = _ClassifyOnlyGraph()
    scope = ScopeViewSnapshot(
        principal_id="33333333-3333-4333-8333-333333333333",
        session_id="44444444-4444-4444-8444-444444444444",
        tenant_id="11111111-1111-4111-8111-111111111111",
        cluster_id="22222222-2222-4222-8222-222222222222",
        request_id="55555555-5555-4555-8555-555555555555",
        source="test", workload_kind="chat",
        chat_session_id="66666666-6666-4666-8666-666666666666",
        chat_turn_id="77777777-7777-4777-8777-777777777777",
    )

    async def _collect():
        return [event async for event in brain.stream_sync(
            "diagnosis", "checkout", "发起调查", "thread-cta", request_context=scope,
        )]

    events = asyncio.run(_collect())
    done = [event for event in events if event.get("type") == "done"]
    assert done and "__investigation_required__" in done[-1].get("text", "")
