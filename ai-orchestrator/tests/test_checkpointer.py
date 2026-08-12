"""验证 BrainOrchestrator 使用 AsyncSqliteSaver（落盘），而非 MemorySaver（内存）。

背景:
- 旧实现 L1004 `self.checkpointer = MemorySaver()` 不落盘，进程重启后会话历史丢失。
- 新实现: __init__ 用 MemorySaver 占位（模块加载时无 running event loop，无法创建
  AsyncSqliteSaver），首次 ainvoke/astream 前调 _ensure_async_checkpointer() 延迟切换为
  AsyncSqliteSaver，checkpoint 落盘到 SQLite 文件。

注意 brief 中测试样例的 checkpoint 数据格式不合法（缺 `id` 字段、config 缺
`checkpoint_ns`），aput 会抛 KeyError；此处已修正为 LangGraph 期望的最小合法格式。
"""
import asyncio
import os
import sys
import tempfile
import uuid

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

# 测试隔离: 独立 temp dir，避免残留 checkpoint 干扰其他测试。
# 注意: 必须在 import orchestrator 之前设置，因为 orchestrator 模块底部
# `brain = BrainOrchestrator()` 会在 import 时读取 AIOPS_DATA_DIR。
os.environ["AIOPS_DATA_DIR"] = tempfile.mkdtemp(prefix="aiops-ckpt-test-")


def _make_checkpoint(checkpoint_id: str = "ckpt-1"):
    """构造一个最小合法的 LangGraph checkpoint dict。

    AsyncSqliteSaver.aput 要求 checkpoint 含 `id` 字段（用于主键），
    其余字段经 JsonPlusSerializer 序列化存储。
    """
    return {
        "v": 1,
        "id": checkpoint_id,
        "ts": "2026-08-11T00:00:00+00:00",
        "channel_values": {"messages": ["hello"]},
        "channel_versions": {},
        "versions_seen": {},
        "pending_sends": [],
    }


def test_checkpointer_starts_as_memory_in_init():
    """__init__（同步、模块加载期）应仍用 MemorySaver 占位，不能直接创建 AsyncSqliteSaver。

    这保证 `import orchestrator` 在无 running event loop 时不会失败。
    """
    from orchestrator import BrainOrchestrator
    from langgraph.checkpoint.memory import MemorySaver
    brain = BrainOrchestrator()
    assert isinstance(brain.checkpointer, MemorySaver), \
        f"__init__ 应使用 MemorySaver 占位，got {type(brain.checkpointer)}"


def test_checkpointer_is_async_sqlite_after_setup():
    """BrainOrchestrator 在 async context 中初始化后，checkpointer 应为 AsyncSqliteSaver。"""
    async def _check():
        from orchestrator import BrainOrchestrator
        from langgraph.checkpoint.sqlite.aio import AsyncSqliteSaver
        brain = BrainOrchestrator()
        await brain._ensure_async_checkpointer()  # 延迟初始化
        assert isinstance(brain.checkpointer, AsyncSqliteSaver), \
            f"Expected AsyncSqliteSaver, got {type(brain.checkpointer)}"
        # 清理 aiosqlite 连接，避免资源泄漏警告
        if getattr(brain, "_async_conn", None) is not None:
            await brain._async_conn.close()
    asyncio.run(_check())


def test_checkpointer_persists_across_instances():
    """两个 BrainOrchestrator 实例共享同一个 SQLite 文件，第二个实例能读到第一个的 checkpoint。"""
    async def _check():
        from orchestrator import BrainOrchestrator
        brain1 = BrainOrchestrator()
        await brain1._ensure_async_checkpointer()
        # 写入一个 checkpoint
        thread_id = f"test-thread-{uuid.uuid4().hex[:8]}"
        config = {"configurable": {"thread_id": thread_id, "checkpoint_ns": ""}}
        await brain1.checkpointer.aput(config, _make_checkpoint(), {}, {})
        # 关闭 brain1 的连接，确保数据已落盘
        await brain1._async_conn.close()
        # 新实例读同一文件
        brain2 = BrainOrchestrator()
        await brain2._ensure_async_checkpointer()
        checkpoint = await brain2.checkpointer.aget(config)
        assert checkpoint is not None, "checkpoint should persist across instances"
        assert checkpoint.get("channel_values", {}).get("messages") == ["hello"], \
            f"checkpoint content mismatch: {checkpoint}"
        await brain2._async_conn.close()
    asyncio.run(_check())


def test_checkpointer_survives_across_event_loops():
    """main.py 的 sync handler 用 asyncio.run() 每次新建 event loop。

    brain 单例的 saver 若绑定到第一次 asyncio.run() 的 loop（之后已关闭），
    第二次 asyncio.run() 调用 graph.ainvoke 会向已关闭的 loop 提交协程 → 报错。
    _ensure_async_checkpointer 必须检测 loop 切换并重建 saver。
    """
    from orchestrator import BrainOrchestrator

    brain = BrainOrchestrator()

    # 第一次 asyncio.run: 初始化 saver + 写 checkpoint
    async def _first():
        await brain._ensure_async_checkpointer()
        cfg = {"configurable": {"thread_id": "cross-loop-tid", "checkpoint_ns": ""}}
        await brain.checkpointer.aput(cfg, _make_checkpoint("ckpt-cross"), {}, {})
        return cfg
    config = asyncio.run(_first())

    # 第二次 asyncio.run: 新 event loop，应能读到上次写入的 checkpoint
    async def _second():
        await brain._ensure_async_checkpointer()  # 检测到 loop 变化，重建 saver
        ckpt = await brain.checkpointer.aget(config)
        assert ckpt is not None, "checkpoint should survive across event loops"
        assert ckpt.get("id") == "ckpt-cross", f"unexpected checkpoint: {ckpt}"
        if getattr(brain, "_async_conn", None) is not None:
            await brain._async_conn.close()
    asyncio.run(_second())


def test_get_session_state_reads_checkpoint_synchronously():
    """修复 P0: main.py get_session/list_sessions 改用同步 `get_session_state()` 读 checkpoint
    state，不再用 `graph.get_state()`（AsyncSqliteSaver 主线程同步调用会抛 InvalidStateError /
    跨 loop 抛 RuntimeError → HTTP 500 → 前端历史会话点击无反应）。

    验证: 写入 checkpoint 后，用同步 get_session_state 能在任意 loop（即使 saver 绑定到已关闭 loop）
    读到 user_message/final_response/intent，不抛异常。
    """
    from orchestrator import BrainOrchestrator

    brain = BrainOrchestrator()
    thread_id = f"get-state-{uuid.uuid4().hex[:8]}"

    # loop 1: 写一个 checkpoint（模拟首次会话，saver 绑定到该临时 loop）
    async def _write():
        await brain._ensure_async_checkpointer()
        cfg = {"configurable": {"thread_id": thread_id, "checkpoint_ns": ""}}
        ckpt = _make_checkpoint(f"ckpt-{thread_id}")
        ckpt["channel_values"] = {
            "messages": [], "user_message": "诊断 service-a",
            "final_response": "已定位根因，建议重启 service-a", "intent": "diagnosis",
        }
        await brain.checkpointer.aput(cfg, ckpt, {}, {})
    asyncio.run(_write())
    # 第一次 loop 已关闭

    # 同步读取（模拟 get_session 主线程调用）：不依赖 event loop，应能读到
    vals = brain.get_session_state(thread_id)
    assert vals is not None, "get_session_state should return state"
    assert vals.get("user_message") == "诊断 service-a", f"unexpected user_message: {vals}"
    assert vals.get("final_response", "").startswith("已定位根因"), \
        f"unexpected final_response: {vals.get('final_response')}"
    assert vals.get("intent") == "diagnosis", f"unexpected intent: {vals}"

    # 清理 aiosqlite 连接，避免资源泄漏
    async def _cleanup():
        if getattr(brain, "_async_conn", None) is not None:
            await brain._async_conn.close()
    asyncio.run(_cleanup())
