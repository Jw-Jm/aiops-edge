"""Task 6: orchestrator async refactor — verify _llm_async 不阻塞 event loop + node_* 为 async。

502 根因: orchestrator 的 _llm() 内部 future.result(timeout=60) 同步阻塞 event loop，
导致 uvicorn liveness probe 超时 → kubelet kill → 502。
修复: _llm_async() 用 asyncio.to_thread 把 _llm 丢到线程池；所有 node_* 改 async。
"""
import asyncio
import inspect
import os
import sys
import time

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))


def test_llm_async_does_not_block_event_loop():
    """_llm_async 应该用 asyncio.to_thread，不阻塞 event loop。

    通过 patch _llm 为带 sleep 的慢函数，验证 _llm_async 与 timer 能并发执行。
    若 _llm_async 阻塞 event loop（例如直接同步调用 _llm），timer 无法在期间运行。
    """
    import orchestrator

    async def run_test():
        timer_hits = []

        async def timer():
            for i in range(5):
                timer_hits.append(time.time())
                await asyncio.sleep(0.05)

        # patch _llm 为带 0.3s sleep 的同步慢函数，模拟真实 LLM 阻塞调用
        original_llm = orchestrator._llm

        def slow_llm(cfg, system_prompt, user_prompt, role="分析专家", timeout=60):
            time.sleep(0.3)
            return "[mock] slow response"

        orchestrator._llm = slow_llm
        try:
            # _llm_async 签名与 _llm 对齐: (cfg, system_prompt, user_prompt, role)
            task1 = asyncio.create_task(
                orchestrator._llm_async(None, "sys", "test prompt")
            )
            task2 = asyncio.create_task(timer())

            await task1
            await task2

            # timer 应该跑了 5 次（如果 event loop 没被 _llm_async 阻塞）
            assert len(timer_hits) == 5, (
                f"timer only hit {len(timer_hits)} times — event loop was blocked "
                f"by _llm_async (expected 5 concurrent ticks)"
            )
        finally:
            orchestrator._llm = original_llm

    asyncio.run(run_test())


def test_llm_async_returns_llm_result():
    """_llm_async 应返回 _llm 的结果（透传，不改语义）。"""
    import orchestrator

    async def run_test():
        original_llm = orchestrator._llm
        orchestrator._llm = lambda cfg, sp, up, role="分析专家", timeout=60: f"[ok] {sp}/{up}"
        try:
            result = await orchestrator._llm_async(None, "SYS", "UP")
            assert result == "[ok] SYS/UP", f"unexpected result: {result!r}"
        finally:
            orchestrator._llm = original_llm

    asyncio.run(run_test())


def test_node_collect_is_async():
    """node_collect 应该是 async 函数。"""
    from orchestrator import node_collect
    assert inspect.iscoroutinefunction(node_collect), "node_collect must be async"


def test_all_node_functions_are_async():
    """所有 node_* 函数都应该是 async（LangGraph 混合 sync/async 节点虽支持，
    但统一 async 避免在不预期处阻塞 event loop）。"""
    import orchestrator
    node_fns = [
        name for name in dir(orchestrator)
        if name.startswith("node_") and callable(getattr(orchestrator, name))
    ]
    assert len(node_fns) >= 15, f"expected >=15 node_* functions, got {len(node_fns)}"
    sync_nodes = [n for n in node_fns
                  if not inspect.iscoroutinefunction(getattr(orchestrator, n))]
    assert sync_nodes == [], (
        f"these node_* are still sync (must be async def): {sync_nodes}"
    )


def test_llm_async_is_coroutine():
    """_llm_async 应该是 coroutine function。"""
    from orchestrator import _llm_async
    assert inspect.iscoroutinefunction(_llm_async), "_llm_async must be async def"


def test_execute_sync_is_async():
    """execute_sync 改为 async def（调用方需 await / asyncio.run 包裹）。"""
    from orchestrator import BrainOrchestrator
    assert inspect.iscoroutinefunction(BrainOrchestrator.execute_sync), \
        "execute_sync must be async def"


def test_stream_sync_is_async():
    """stream_sync 改为 async generator（调用方需 async for）。"""
    from orchestrator import BrainOrchestrator
    # async generator (async def + yield) 不是 coroutine function，要用 isasyncgenfunction
    assert inspect.isasyncgenfunction(BrainOrchestrator.stream_sync), \
        "stream_sync must be async def (async generator)"


def test_node_collect_http_calls_do_not_block_event_loop():
    """node_collect 内的 get_service_list/query_metrics 应走 asyncio.to_thread，
    同步 HTTP 阻塞调用不阻塞 event loop（502 根因）。

    通过 patch get_service_list 为带 sleep 的同步慢函数 + 并发 timer 验证：
    若 node_collect 直接同步调用（不走 to_thread），timer 会被阻塞。
    """
    import orchestrator

    async def run_test():
        timer_hits = []

        async def timer():
            for _ in range(4):
                timer_hits.append(time.time())
                await asyncio.sleep(0.05)

        original_get_service_list = orchestrator.get_service_list
        original_parse = orchestrator._parse
        # 慢速同步 get_service_list（模拟真实 HTTP 阻塞 0.3s）
        orchestrator.get_service_list = lambda: time.sleep(0.3) or "[]"
        orchestrator._parse = lambda raw: []  # 快速返回空 list
        try:
            state = {"llm_config": None, "service": ""}
            t1 = asyncio.create_task(orchestrator.node_collect(state))
            t2 = asyncio.create_task(timer())
            await t1
            await t2
            # 若 node_collect 阻塞 event loop，timer 只会跑 1 次；走 to_thread 则 4 次全跑
            assert len(timer_hits) == 4, (
                f"timer only hit {len(timer_hits)} — node_collect blocked the event loop "
                f"(expected 4 concurrent ticks via asyncio.to_thread)"
            )
        finally:
            orchestrator.get_service_list = original_get_service_list
            orchestrator._parse = original_parse

    asyncio.run(run_test())
