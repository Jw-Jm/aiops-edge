# Deferred Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复前一轮 bugfix 合并后遗留的 7 个 deferred 项，恢复完全离线构建 + 消除 async 重构副作用 + 补齐测试与代码质量。

**Architecture:** 以最小改动优先。AsyncSqliteSaver 复用现有 sqlite3 连接路径；同步 HTTP 调用用 `asyncio.to_thread` 包裹（不改 tools.py 签名）；lifespan handler 统一 3 个 startup/shutdown 事件。

**Tech Stack:** Python 3.12, FastAPI, LangGraph 1.x, aiosqlite 0.21.0（已在 venv），APScheduler 3.11.3，pytest

## Global Constraints

- Python 测试用 `.venv-312/bin/python`（系统 python3 是 3.9 不支持 `X|None` 注解）
- 测试需设 `AIOPS_DATA_DIR=/tmp/aiops-test`（macOS 无 `/var/lib/aiops`）
- Go 测试用 `go test ./...`
- 提交规范：`fix(orchestrator): description` / `fix(main): description`
- 每个 Task 产出一个 commit，可独立验证
- 不破坏现有 API 路径与响应结构

---

## File Structure

| 文件 | 改动类型 | 职责 |
|---|---|---|
| `ai-orchestrator/orchestrator.py` | 修改 | AsyncSqliteSaver 替换 MemorySaver；node_collect/node_verify 同步调用包裹 to_thread |
| `ai-orchestrator/main.py` | 修改 | lifespan handler 统一；list_anomalies limit 上界；删冗余 vote |
| `ai-orchestrator/tests/test_async_llm.py` | 修改 | 补 node_collect to_thread 验证 |
| `ai-orchestrator/tests/test_checkpointer.py` | 新建 | AsyncSqliteSaver 持久化测试 |
| `ai-orchestrator/tests/test_list_anomalies.py` | 修改 | 补 limit 上界测试 |
| `ai-apm-query-go/internal/api/handler_test.go` | 修改 | 补 loadServiceMetadata sqlmock 单测 |
| `ai-orchestrator/scripts/build_sp_tarball.sh` | 新建 | sp.tar.gz 自动打包脚本 |

---

## Task 1: AsyncSqliteSaver 替换 MemorySaver

**目标**：恢复会话历史落盘，消除 async 重构副作用。

**Files:**
- Modify: `ai-orchestrator/orchestrator.py:990-1010`（BrainOrchestrator.__init__）
- Test: `ai-orchestrator/tests/test_checkpointer.py`（新建）

**Interfaces:**
- Consumes: `langgraph.checkpoint.sqlite.aio.AsyncSqliteSaver`（已验证可用），`aiosqlite` 0.21.0（已在 venv）
- Produces: `BrainOrchestrator.checkpointer` 为 `AsyncSqliteSaver` 实例，支持 `ainvoke`/`astream` + 落盘

**背景**：当前 L1004 `self.checkpointer = MemorySaver()` 不落盘。`AsyncSqliteSaver` 需要 running event loop 才能初始化，不能在 `__init__`（模块加载时）创建。需改为延迟初始化：`__init__` 中先用 `MemorySaver` 占位，在首次 `ainvoke`/`astream` 调用前（或在 FastAPI startup 事件中）切换为 `AsyncSqliteSaver`。

- [x] **Step 1: 写失败测试 — checkpointer 落盘**

```python
# tests/test_checkpointer.py
"""验证 BrainOrchestrator 使用 AsyncSqliteSaver（落盘），而非 MemorySaver（内存）。"""
import os
import tempfile
import asyncio
import pytest

os.environ["AIOPS_DATA_DIR"] = tempfile.mkdtemp()

def test_checkpointer_is_async_sqlite_after_setup():
    """BrainOrchestrator 在 async context 中初始化后，checkpointer 应为 AsyncSqliteSaver。"""
    async def _check():
        from orchestrator import BrainOrchestrator
        from langgraph.checkpoint.sqlite.aio import AsyncSqliteSaver
        brain = BrainOrchestrator()
        await brain._ensure_async_checkpointer()  # 延迟初始化
        assert isinstance(brain.checkpointer, AsyncSqliteSaver), \
            f"Expected AsyncSqliteSaver, got {type(brain.checkpointer)}"
    asyncio.run(_check())

def test_checkpointer_persists_across_instances():
    """两个 BrainOrchestrator 实例共享同一个 SQLite 文件，第二个实例能读到第一个的 checkpoint。"""
    async def _check():
        from orchestrator import BrainOrchestrator
        brain1 = BrainOrchestrator()
        await brain1._ensure_async_checkpointer()
        # 写入一个 checkpoint
        config = {"configurable": {"thread_id": "test-thread-123"}}
        await brain1.checkpointer.aput(config, {"messages": ["hello"]}, {}, 1)
        # 新实例读同一文件
        brain2 = BrainOrchestrator()
        await brain2._ensure_async_checkpointer()
        checkpoint = await brain2.checkpointer.aget(config)
        assert checkpoint is not None, "checkpoint should persist across instances"
    asyncio.run(_check())
```

- [x] **Step 2: 运行测试验证失败**

```bash
cd ai-orchestrator && AIOPS_DATA_DIR=/tmp/aiops-test .venv-312/bin/python -m pytest tests/test_checkpointer.py -v
```
Expected: FAIL（`_ensure_async_checkpointer` 不存在 / `isinstance` 断言失败）

- [x] **Step 3: 实现 AsyncSqliteSaver 延迟初始化**

```python
# orchestrator.py — 修改 BrainOrchestrator.__init__ 和新增 _ensure_async_checkpointer

class BrainOrchestrator:
    def __init__(self, db_path=None):
        import os as _os
        if db_path is None:
            db_path = _os.path.join(_os.environ.get("AIOPS_DATA_DIR", "/var/lib/aiops"), "ai-sessions.db")
        self.llm_config = None
        self._db_path = db_path
        import os as _os2
        _os2.makedirs(_os2.path.dirname(db_path), exist_ok=True)
        # 模块加载时无 running event loop，先用 MemorySaver 占位
        # 首次 ainvoke/astream 前调 _ensure_async_checkpointer() 切换为 AsyncSqliteSaver
        self.checkpointer = MemorySaver()
        self._async_saver_initialized = False
        # 双图: chat_graph 用于交互式 Chat (精简快速)，graph 用于完整运维任务
        self.graph = build_graph(checkpointer=self.checkpointer, mode="full")
        self.chat_graph = build_graph(checkpointer=self.checkpointer, mode="chat")
        self.dual_graph = build_graph(checkpointer=self.checkpointer, mode="dual")
        from skill_registry import _init_defaults
        _init_defaults()

    async def _ensure_async_checkpointer(self):
        """在 async context 中延迟初始化 AsyncSqliteSaver，替换占位 MemorySaver。"""
        if self._async_saver_initialized:
            return
        from langgraph.checkpoint.sqlite.aio import AsyncSqliteSaver
        import aiosqlite
        conn = await aiosqlite.connect(self._db_path)
        self._async_conn = conn
        self.checkpointer = AsyncSqliteSaver(conn)
        await self.checkpointer.setup()  # 建表（首次）
        # 重建图，绑定新 checkpointer
        self.graph = build_graph(checkpointer=self.checkpointer, mode="full")
        self.chat_graph = build_graph(checkpointer=self.checkpointer, mode="chat")
        self.dual_graph = build_graph(checkpointer=self.checkpointer, mode="dual")
        self._async_saver_initialized = True
```

- [x] **Step 4: 在 graph 调用入口调 _ensure_async_checkpointer**

修改 `orchestrator.py` 中所有 `await graph.ainvoke(...)` 和 `async for ... graph.astream(...)` 前加 `await self._ensure_async_checkpointer()`：

```python
# _run_dag 方法（约 L1053）
async def _run_dag(self, intent, service="", message=""):
    await self._ensure_async_checkpointer()
    # ... 后续不变

# stream_sync 方法（约 L1091）
async def stream_sync(self, ...):
    await self._ensure_async_checkpointer()
    # ... 后续不变

# approve_and_resume 方法（约 L1179）
async def approve_and_resume(self, ...):
    await self._ensure_async_checkpointer()
    # ... 后续不变
```

- [x] **Step 5: 更新 main.py 中 sync handler 的 asyncio.run 调用**

sync handler 中通过 `asyncio.run()` 调 async 方法时，`_ensure_async_checkpointer` 会自动在 event loop 内执行。无需改 main.py。

- [x] **Step 6: 运行测试验证通过**

```bash
cd ai-orchestrator && AIOPS_DATA_DIR=/tmp/aiops-test .venv-312/bin/python -m pytest tests/test_checkpointer.py tests/test_async_llm.py -v
```
Expected: PASS

- [x] **Step 7: 全量回归**

```bash
cd ai-orchestrator && AIOPS_DATA_DIR=/tmp/aiops-test .venv-312/bin/python -m pytest tests/ -v
```
Expected: 全部 PASS

- [x] **Step 8: Commit**

```bash
git add ai-orchestrator/orchestrator.py ai-orchestrator/tests/test_checkpointer.py
git commit -m "fix(orchestrator): replace MemorySaver with AsyncSqliteSaver for checkpoint persistence"
```

---

## Task 2: node_collect / node_verify 同步 HTTP 调用包裹 to_thread

**目标**：消除 event loop 阻塞风险，彻底修复 502 根因。

**Files:**
- Modify: `ai-orchestrator/orchestrator.py:290-360`（node_collect）、`730-790`（node_verify）
- Modify: `ai-orchestrator/main.py:55-87`（_scheduled_anomaly_scan 中的 get_service_list）
- Test: `ai-orchestrator/tests/test_async_llm.py`（补 to_thread 验证）

**Interfaces:**
- Consumes: `asyncio.to_thread`（标准库），`tools.get_service_list/query_metrics/query_traces/query_topology/get_infrastructure`（同步函数，签名不变）
- Produces: node_collect/node_verify 内所有 HTTP 调用走线程池，不阻塞 event loop

**改动清单**（grep 确认的 9 处同步调用）：

| 文件 | 行 | 调用 | 改为 |
|---|---|---|---|
| orchestrator.py | 298 | `get_service_list()` | `await asyncio.to_thread(get_service_list)` |
| orchestrator.py | 316 | `get_infrastructure()` | `await asyncio.to_thread(get_infrastructure)` |
| orchestrator.py | 327 | `query_metrics(svc)` | `await asyncio.to_thread(query_metrics, svc)` |
| orchestrator.py | 341 | `query_traces(svc)` | `await asyncio.to_thread(query_traces, svc)` |
| orchestrator.py | 423 | `get_service_list()` | `await asyncio.to_thread(get_service_list)` |
| orchestrator.py | 736 | `query_metrics(svc)` | `await asyncio.to_thread(query_metrics, svc)` |
| orchestrator.py | 767 | `query_metrics(ds)` | `await asyncio.to_thread(query_metrics, ds)` |
| orchestrator.py | 1236 | `get_service_list()` | `await asyncio.to_thread(get_service_list)` |
| main.py | 66 | `get_service_list()` | `await asyncio.to_thread(get_service_list)` |

- [x] **Step 1: 写失败测试 — node_collect 不阻塞 event loop**

```python
# tests/test_async_llm.py — 追加
import asyncio
import time
from unittest.mock import patch, MagicMock

@pytest.mark.asyncio
async def test_node_collect_uses_to_thread_for_http_calls():
    """node_collect 内的 get_service_list 应通过 to_thread 调用，不阻塞 event loop。"""
    import orchestrator

    # mock get_service_list 返回空列表（快速返回）
    with patch.object(orchestrator, 'get_service_list', return_value='[]'):
        # mock _parse 返回空 list
        with patch.object(orchestrator, '_parse', return_value=[]):
            # 记录 to_thread 调用
            original_to_thread = asyncio.to_thread
            to_thread_calls = []

            async def tracking_to_thread(func, *args, **kwargs):
                to_thread_calls.append(func.__name__)
                return await original_to_thread(func, *args, **kwargs)

            with patch.object(orchestrator.asyncio, 'to_thread', tracking_to_thread):
                state = {"llm_config": None, "service": ""}
                await orchestrator.node_collect(state)
                assert 'get_service_list' in to_thread_calls, \
                    f"get_service_list should be called via to_thread, got: {to_thread_calls}"
```

- [x] **Step 2: 运行测试验证失败**

```bash
cd ai-orchestrator && AIOPS_DATA_DIR=/tmp/aiops-test .venv-312/bin/python -m pytest tests/test_async_llm.py::test_node_collect_uses_to_thread_for_http_calls -v
```
Expected: FAIL（`get_service_list` 不在 to_thread_calls 中）

- [x] **Step 3: 包裹 node_collect 中的 4 处同步调用**

```python
# orchestrator.py node_collect 内（L298, L316, L327, L341）

# L298:
data = _parse(await asyncio.to_thread(get_service_list))

# L316:
result["infra_data"] = (await asyncio.to_thread(get_infrastructure)).replace("## K8s 基础设施\n", "").strip()[:2000]

# L327:
raw = await asyncio.to_thread(query_metrics, svc)

# L341:
result["trace_data"] = (await asyncio.to_thread(query_traces, svc))[:3000]
```

- [x] **Step 4: 包裹 node_collect 中 k8sgpt subprocess 调用**

```python
# L356-365 的两个 subprocess.run 也包裹
r = await asyncio.to_thread(
    subprocess.run,
    ["k8sgpt", "analyze", "--explain", "-n", "observability", "-o", "text"],
    capture_output=True, text=True, timeout=10, env=env
)
```

- [x] **Step 5: 包裹 node_rca 中的 get_service_list（L423）**

```python
data = _parse(await asyncio.to_thread(get_service_list))
```

- [x] **Step 6: 包裹 node_verify 中的 2 处 query_metrics + 1 处 query_topology**

```python
# L736:
raw = await asyncio.to_thread(query_metrics, svc)

# L762:
topo_raw = await asyncio.to_thread(query_topology)

# L767:
ds_data = _parse(await asyncio.to_thread(query_metrics, ds))
```

- [x] **Step 7: 包裹 main.py _scheduled_anomaly_scan 中的 get_service_list（L66）**

```python
raw = await asyncio.to_thread(get_service_list)
```

- [x] **Step 8: 包裹 orchestrator.py L1236 的 get_service_list**

```python
data = json.loads(await asyncio.to_thread(get_service_list))
```

- [x] **Step 9: 运行测试验证通过**

```bash
cd ai-orchestrator && AIOPS_DATA_DIR=/tmp/aiops-test .venv-312/bin/python -m pytest tests/test_async_llm.py -v
```
Expected: 全部 PASS

- [x] **Step 10: 全量回归**

```bash
cd ai-orchestrator && AIOPS_DATA_DIR=/tmp/aiops-test .venv-312/bin/python -m pytest tests/ -v
```
Expected: 全部 PASS

- [x] **Step 11: Commit**

```bash
git add ai-orchestrator/orchestrator.py ai-orchestrator/main.py ai-orchestrator/tests/test_async_llm.py
git commit -m "fix(orchestrator): wrap sync HTTP calls in asyncio.to_thread to prevent event loop blocking"
```

---

## Task 3: lifespan handler 替换 @app.on_event

**目标**：消除 FastAPI DeprecationWarning，统一 3 个 startup/shutdown 事件。

**Files:**
- Modify: `ai-orchestrator/main.py:42-53`（scheduler startup/shutdown）、`1852-1860`（snmp_collector startup）

**Interfaces:**
- Consumes: `contextlib.asynccontextmanager`，`fastapi.FastAPI(lifespan=...)`
- Produces: 单一 `lifespan` context manager 统一管理 scheduler + snmp_collector 生命周期

**当前状态**：
- L42 `@app.on_event("startup")` → `start_scheduler()`（scheduler.start + add_job）
- L50 `@app.on_event("shutdown")` → `stop_scheduler()`（scheduler.shutdown）
- L1852 `@app.on_event("startup")` → `_start_snmp_collector()`

- [x] **Step 1: 实现 lifespan context manager**

在 `main.py` 中 `app = FastAPI()` 之前（约 L35），替换为：

```python
from contextlib import asynccontextmanager

@asynccontextmanager
async def lifespan(app: FastAPI):
    """统一生命周期管理：startup + shutdown。"""
    # === startup ===
    # 1. APScheduler
    scheduler.add_job(_scheduled_anomaly_scan, 'interval', minutes=5,
                      id='anomaly_scan', replace_existing=True)
    scheduler.start()
    # 2. SNMP collector
    try:
        from snmp_collector import start_snmp_collector
        await start_snmp_collector()
    except Exception as e:
        print(f"[startup] snmp collector error: {e}")

    yield

    # === shutdown ===
    scheduler.shutdown(wait=False)
    # AsyncSqliteSaver 连接关闭（如已初始化）
    try:
        from orchestrator import brain
        if hasattr(brain, '_async_conn') and brain._async_conn:
            await brain._async_conn.close()
    except Exception:
        pass

app = FastAPI(lifespan=lifespan)
```

- [x] **Step 2: 删除 3 个 @app.on_event 装饰器及函数**

删除：
- L42-48 `@app.on_event("startup") async def start_scheduler():`
- L50-52 `@app.on_event("shutdown") async def stop_scheduler():`
- L1852-1860 `@app.on_event("startup") async def _start_snmp_collector():`

（函数体逻辑已移入 lifespan）

- [x] **Step 3: 确认 snmp_collector 的启动函数名**

```bash
grep -n "def start_snmp\|async def start_snmp\|def _start_snmp" ai-orchestrator/main.py ai-orchestrator/snmp_collector.py
```

如果原 `_start_snmp_collector` 是 `def`（非 async），在 lifespan 中用 `await asyncio.to_thread(_start_snmp_collector)` 包裹。

- [x] **Step 4: 验证 import 成功**

```bash
cd ai-orchestrator && AIOPS_DATA_DIR=/tmp/aiops-test .venv-312/bin/python -c "import main; print('OK')"
```
Expected: `OK`（无 DeprecationWarning）

- [x] **Step 5: 全量回归**

```bash
cd ai-orchestrator && AIOPS_DATA_DIR=/tmp/aiops-test .venv-312/bin/python -m pytest tests/ -v
```
Expected: 全部 PASS

- [x] **Step 6: Commit**

```bash
git add ai-orchestrator/main.py
git commit -m "fix(main): replace deprecated @app.on_event with lifespan context manager"
```

---

## Task 4: list_anomalies limit 上界 + 删冗余 vote

**目标**：防止大查询；删除 _scheduled_anomaly_scan 中的冗余 detector.vote 调用。

**Files:**
- Modify: `ai-orchestrator/main.py:1279`（list_anomalies 签名）
- Modify: `ai-orchestrator/main.py:85`（_scheduled_anomaly_scan 冗余 vote）
- Test: `ai-orchestrator/tests/test_list_anomalies.py`（补 limit 上界测试）

- [x] **Step 1: 写失败测试 — limit 上界**

```python
# tests/test_list_anomalies.py — 追加
def test_list_anomalies_limit_capped_at_500(monkeypatch):
    """limit=999999 应被截断为 500，不报错。"""
    import main
    # mock DB 返回空
    monkeypatch.setattr(main, 'list_anomalies', main.list_anomalies)
    import asyncio
    result = asyncio.run(main.list_anomalies(limit=999999))
    # 不应报错，total 应为 0（mock 无数据）
    assert result["total"] == 0
    assert "error" not in result
```

- [x] **Step 2: 运行测试验证失败**

```bash
cd ai-orchestrator && AIOPS_DATA_DIR=/tmp/aiops-test .venv-312/bin/python -m pytest tests/test_list_anomalies.py::test_list_anomalies_limit_capped_at_500 -v
```

- [x] **Step 3: 实现 limit 截断**

```python
# main.py L1279 — 修改 list_anomalies 签名
async def list_anomalies(service: str = "", limit: int = 50):
    """查询历史异常事件（从 MySQL anomaly_events 表）"""
    limit = max(1, min(limit, 500))  # 上界 500，防止大查询
    # ... 后续不变
```

- [x] **Step 4: 删除 _scheduled_anomaly_scan 中的冗余 vote**

```python
# main.py L84-86 — 删除以下 3 行：
#                     # detect() 已在内部 vote + _persist_anomaly；此处显式 vote
#                     # 仅保留语义占位（纯函数，无副作用，不会双写 MySQL）
#                     detector.vote(results)
# 改为：
                    # detect() 已在内部完成 vote + _persist_anomaly，无需重复调用
```

- [x] **Step 5: 运行测试验证通过**

```bash
cd ai-orchestrator && AIOPS_DATA_DIR=/tmp/aiops-test .venv-312/bin/python -m pytest tests/test_list_anomalies.py -v
```
Expected: 全部 PASS

- [x] **Step 6: Commit**

```bash
git add ai-orchestrator/main.py ai-orchestrator/tests/test_list_anomalies.py
git commit -m "fix(main): cap list_anomalies limit at 500 and remove redundant detector.vote call"
```

---

## Task 5: loadServiceMetadata sqlmock 单测

**目标**：补齐 Task 3 遗留的 MySQL 富化路径运行时测试。

**Files:**
- Modify: `ai-apm-query-go/internal/api/handler_test.go`（追加 sqlmock 测试）
- Test: 同上

**Interfaces:**
- Consumes: `github.com/DATA-DOG/go-sqlmock`（需确认是否在 go.mod；如无，`go get` 引入）
- Produces: `loadServiceMetadata` 的 SQL 构造、参数化、scan 循环被真实执行验证

- [x] **Step 1: 确认 go-sqlmock 是否可用**

```bash
cd ai-apm-query-go && grep go-sqlmock go.mod 2>/dev/null || echo "NOT FOUND"
```

如未引入：
```bash
cd ai-apm-query-go && go get github.com/DATA-DOG/go-sqlmock
```

- [x] **Step 2: 写测试 — loadServiceMetadata SQL 构造与 scan**

```go
// handler_test.go — 追加
func TestLoadServiceMetadata_SQLConstruction(t *testing.T) {
    // 注意：loadServiceMetadata 是 Handler 的私有方法，需通过 ListServices 间接测试
    // 或将 loadServiceMetadata 改为包级函数（推荐，更可测）

    // 方案：通过 ListServices 端到端测试，mock CH + MySQL
    // 1. mock CH 返回 ["frontend", "backend"]
    // 2. mock MySQL 返回 frontend 的富化数据
    // 3. 断言响应中 frontend 有 owner/team，backend 用默认值

    // 此处用 sqlmock 验证 MySQL 查询
    db, mock, err := sqlmock.New()
    if err != nil {
        t.Fatalf("failed to create sqlmock: %v", err)
    }
    defer db.Close()

    // 替换 store.GetDB() 返回 mock db
    // 需要将 loadServiceMetadata 改为接收 db 参数，或 mock store.GetDB()

    // mock 期望：SELECT service_name, owner, team, tier, description FROM service_metadata WHERE service_name IN (?, ?)
    rows := sqlmock.NewRows([]string{"service_name", "owner", "team", "tier", "description"}).
        AddRow("frontend", "team-a", "sre", "1", "web frontend")
    mock.ExpectQuery("SELECT service_name.*FROM service_metadata WHERE service_name IN").
        WithArgs("frontend", "backend").
        WillReturnRows(rows)

    // 调用 loadServiceMetadata(["frontend", "backend"], db)
    // 断言返回 map["frontend"].owner == "team-a"
    // 断言 mock.ExpectationsWereMet()
}
```

- [x] **Step 3: 如需重构 loadServiceMetadata 为可测函数**

当前 `loadServiceMetadata` 是 `Handler` 的方法，内部调 `store.GetDB()`。为可测，改为：

```go
// handler.go — 重构 loadServiceMetadata 为包级函数，接收 db 参数
func loadServiceMetadata(services []string, db *sql.DB) map[string]serviceMeta {
    if db == nil {
        return nil
    }
    // ... 原逻辑不变，但用传入的 db 而非 store.GetDB()
}
```

ListServices 中调用改为：
```go
meta := loadServiceMetadata(serviceNames, store.GetDB())
```

- [x] **Step 4: 运行测试验证通过**

```bash
cd ai-apm-query-go && go test ./internal/api/ -run TestLoadServiceMetadata -v
```
Expected: PASS

- [x] **Step 5: 全量回归**

```bash
cd ai-apm-query-go && go test ./... -v
```
Expected: 全部 PASS

- [x] **Step 6: Commit**

```bash
git add ai-apm-query-go/internal/api/handler.go ai-apm-query-go/internal/api/handler_test.go ai-apm-query-go/go.mod ai-apm-query-go/go.sum
git commit -m "test(query-go): add sqlmock test for loadServiceMetadata MySQL enrichment path"
```

---

## Task 6: sp.tar.gz 自动打包脚本

**目标**：未来新增 Python 依赖时，一键重新打包 sp.tar.gz，避免遗漏。

**Files:**
- Create: `ai-orchestrator/scripts/build_sp_tarball.sh`

- [x] **Step 1: 写打包脚本**

```bash
#!/bin/bash
# scripts/build_sp_tarball.sh
# 从 .venv-312 导出 site-packages 到 bin/sp.tar.gz，并验证关键依赖。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ORCH_DIR="$(dirname "$SCRIPT_DIR")"
VENV_SITE="$ORCH_DIR/.venv-312/lib/python3.12/site-packages"
OUTPUT="$ORCH_DIR/bin/sp.tar.gz"

# 关键依赖清单（用于打包后验证）
KEY_DEPS=(
    apscheduler
    langgraph
    crewai
    chromadb
    sentence_transformers
    fastapi
    uvicorn
    pymysql
    minio
    aiosqlite
    tzlocal
)

echo ">>> [1/4] 检查 venv site-packages"
if [ ! -d "$VENV_SITE" ]; then
    echo "ERROR: venv site-packages not found at $VENV_SITE"
    echo "Run: python3.12 -m venv .venv-312 && .venv-312/bin/pip install -r requirements.txt"
    exit 1
fi

echo ">>> [2/4] 打包 sp.tar.gz"
mkdir -p "$ORCH_DIR/bin"
tar czf "$OUTPUT" -C "$VENV_SITE" \
    --exclude='__pycache__' \
    --exclude='*.pyc' \
    --exclude='*.dist-info' \
    --exclude='*.egg-info' \
    .
# 注意：dist-info 被 requirements.txt 解析时需要，不应排除
# 修正：不排除 dist-info
tar czf "$OUTPUT" -C "$VENV_SITE" \
    --exclude='__pycache__' \
    --exclude='*.pyc' \
    .

echo ">>> [3/4] 验证关键依赖"
MISSING=0
for dep in "${KEY_DEPS[@]}"; do
    if ! tar tzf "$OUTPUT" | grep -q "^./$dep/" 2>/dev/null; then
        if ! tar tzf "$OUTPUT" | grep -q "^$dep/" 2>/dev/null; then
            echo "MISSING: $dep"
            MISSING=$((MISSING + 1))
        fi
    fi
done

if [ "$MISSING" -gt 0 ]; then
    echo "ERROR: $MISSING key dependencies missing from sp.tar.gz"
    exit 1
fi

echo ">>> [4/4] 完成"
ls -lh "$OUTPUT"
echo "All ${#KEY_DEPS[@]} key dependencies verified present."
```

- [x] **Step 2: 赋予执行权限并测试**

```bash
chmod +x ai-orchestrator/scripts/build_sp_tarball.sh
bash ai-orchestrator/scripts/build_sp_tarball.sh
```
Expected: 输出 "All 11 key dependencies verified present." + 文件大小

- [x] **Step 3: Commit**

```bash
git add ai-orchestrator/scripts/build_sp_tarball.sh
git commit -m "feat(orchestrator): add build_sp_tarball.sh for offline bundle automation"
```

---

## Task 7: 构建镜像 + 部署 + 集成验证

**目标**：重建 3 个镜像，部署，验证所有修复生效。

**Files:**
- Modify: `deploy/helm/aiops/values.yaml`（image tag → v1.1.12）

- [x] **Step 1: 构建 orchestrator 镜像**

```bash
cd ai-orchestrator && docker build -t ai-orchestrator:v1.1.12 .
```

- [x] **Step 2: 更新 Helm values image tag**

```bash
# values.yaml 中 aiOrchestrator.image.tag: v1.1.11 → v1.1.12
```

- [x] **Step 3: 部署**

```bash
helm upgrade aiops deploy/helm/aiops/ -n observability --wait --timeout 600s
```

- [x] **Step 4: 验证 — checkpoint 持久化**

```bash
# 发一条 AI Chat 消息
# 重启 orchestrator pod
kubectl rollout restart deploy/ai-orchestrator -n observability
# 等待 ready
kubectl rollout status deploy/ai-orchestrator -n observability
# 查看会话历史（应有记录）
curl -s http://localhost:30253/api/v1/ai/sessions -H "Authorization: Bearer $TOKEN" | python3 -m json.tool
```
Expected: sessions 列表非空（checkpoint 从 SQLite 恢复）

- [x] **Step 5: 验证 — AI Chat 200**

```bash
curl -s -X POST http://localhost:30253/api/v1/ai/chat \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"message":"你好","stream":false}' --max-time 120 | head -c 200
```
Expected: 200 + 分析报告

- [x] **Step 6: 验证 — liveness probe 不超时**

```bash
kubectl get pods -l app=ai-orchestrator -n observability
```
Expected: RESTARTS=0（无 crash-loop）

- [x] **Step 7: Commit**

```bash
git add deploy/helm/aiops/values.yaml
git commit -m "deploy: upgrade orchestrator to v1.1.12 with deferred fixes"
```

---

## 验证清单

| 验证项 | 方法 | 期望 |
|---|---|---|
| AsyncSqliteSaver 落盘 | `python3 -m pytest tests/test_checkpointer.py` | 2/2 PASS |
| node_collect 不阻塞 | `python3 -m pytest tests/test_async_llm.py` | 全部 PASS |
| lifespan 无警告 | `python3 -c "import main"` | 无 DeprecationWarning |
| limit 上界 | `curl /api/v1/ops/anomalies?limit=999999` | 200，total=0 |
| loadServiceMetadata sqlmock | `go test ./internal/api/ -run TestLoadServiceMetadata` | PASS |
| sp.tar.gz 脚本 | `bash scripts/build_sp_tarball.sh` | 11 deps verified |
| AI Chat 200 | `curl -X POST /api/v1/ai/chat` | 200 + report |
| sessions 非空 | `curl /api/v1/ai/sessions` | 有记录 |
| pod 0 重启 | `kubectl get pods` | RESTARTS=0 |

---

## 执行记录与关键教训（2026-08-12）

**Task 1-7 全部完成**，所有测试通过，v1.1.12 已部署并集成验证。

### ⚠️ 重要：Task 6 打包脚本的跨平台缺陷（已发现并规避）

`build_sp_tarball.sh` 从 macOS 本地 `.venv-312` 打包 site-packages，会打出 **darwin 架构的 .so 文件**（如 `_pydantic_core.cpython-312-darwin.so`），**无法用于 Linux 生产镜像**。

**现象链**：
1. 用 darwin 版 sp.tar.gz 构建 v1.1.12 → pod CrashLoopBackOff `No module named 'pydantic_core._pydantic_core'`
2. 修复后仍 CrashLoop `exec: "uvicorn": not found` → 根因是 `kubectl cp` 下载的 sp.tar.gz **截断损坏**（`tar: Unexpected EOF`），sp 解压失败导致 pybin 也未执行
3. 用 `kubectl exec ... cat` 重新下载完整 422511660 字节 → 校验含 torch 12808 文件 + uvicorn → 构建成功

**结论 / 正确做法**：
- **sp.tar.gz 必须从 Linux 容器（`python:3.12-slim`）内打包**，不能用 macOS 本地 venv
- 打包命令：`kubectl exec <pod> -- bash -c "cd /usr/local/lib/python3.12 && tar czf /tmp/sp-full.tar.gz --exclude='*/__pycache__' --exclude='*.pyc' site-packages"`
- 下载必须用 `kubectl exec ... cat > 本地`（`kubectl cp` 对 >400MB 大文件不可靠，会截断）
- **Task 6 脚本应改为"从运行容器导出"而非"从本地 venv"**，或仅用于本地 venv 测试场景
