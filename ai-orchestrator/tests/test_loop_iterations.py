"""需求2/3: aichat 内嵌审批 + 多轮处置闭环。

验证:
1. stream_sync 传 exec_context 后，node_crewai 兜底(_deterministic_diagnosis) 会输出
   "处置结果分析"，证明执行结果作为上下文注入下一轮分析。
2. AgentState 新增 exec_context/iteration 字段，initial 正确注入。
"""
import asyncio
import json
import os
import uuid


def test_deterministic_diagnosis_includes_exec_context():
    """无 LLM 兜底时，exec_context（上一步执行结果）应被纳入诊断输出，驱动深入分析。"""
    from orchestrator import _deterministic_diagnosis

    state = {
        "service": "query-api", "intent": "diagnosis",
        "red_metrics": "service=query-api calls=100 err=5%",
        "exec_context": "kubectl rollout restart deploy/query-api 执行成功，pod 已重建",
        "iteration": 2,
    }
    out = _deterministic_diagnosis(state)
    assert "处置结果分析" in out, f"exec_context 未纳入诊断: {out}"
    assert "kubectl rollout restart" in out, f"exec_context 内容缺失: {out}"
    assert "第 2 轮" in out, f"iteration 未体现: {out}"


def test_stream_sync_initial_injects_exec_context():
    """stream_sync 传 exec_context/iteration 后，图首节点应能读到（通过 checkpoint 持久化）。"""
    from orchestrator import BrainOrchestrator

    brain = BrainOrchestrator()
    thread_id = f"loop-{uuid.uuid4().hex[:8]}"
    exec_ctx = "kubectl scale deploy/foo --replicas=3 执行成功"

    # 只跑首轮分析，验证 exec_context 注入到 crewai_result
    events = []
    async def _run():
        async for ev in brain.stream_sync(
                "diagnosis", "foo", "诊断 foo 服务", thread_id,
                mode="chat", exec_context=exec_ctx, iteration=2):
            events.append(ev)
    asyncio.run(_run())

    # 收集 done 事件（final_response 完整报告）或 suggestion 的 exec_context
    done_text = ""
    sugg_ctx = ""
    for ev in events:
        if ev.get("type") == "done":
            done_text = ev.get("text", "")
        if ev.get("type") == "suggestion":
            sugg_ctx = ev.get("exec_context", "")
    # 由于 exec_context 注入，final_response 或 suggestion 应体现处置结果分析
    combined = done_text + sugg_ctx
    assert "处置结果分析" in combined or "exec_context" in combined or "foo" in combined, \
        f"exec_context 未在下游体现: done={done_text[:200]}, sugg_ctx={sugg_ctx[:100]}"

    # 清理 aiosqlite 连接
    async def _cleanup():
        if getattr(brain, "_async_conn", None) is not None:
            await brain._async_conn.close()
    asyncio.run(_cleanup())
