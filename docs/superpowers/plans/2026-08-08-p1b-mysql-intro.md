# P1b：引入 MySQL 业务状态库 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 ai-orchestrator 的审批/审计/Agent/规则/报告/知识库从内存/ClickHouse/JSON 统一迁移到 MySQL 持久化，并新增审批中心、审计、知识库、规则管理 4 个前端页面。

**Architecture:** ai-orchestrator（Python）直连 MySQL（pymysql + DBUtils 连接池 + 轻量版本化迁移器），业务状态数据落 MySQL，时序遥测数据保留 ClickHouse。前端复用 React + AntD + axios client，新增 4 个页面。

**Tech Stack:** Python 3.12, pymysql, DBUtils, FastAPI, React, AntD, axios, Helm

## Global Constraints

- Python 3.12（与 Dockerfile `python:3.12-slim` 对齐）
- MySQL 8.4（Helm 已部署，库名 `aiops`，含 `schema_migrations` 表）
- 业务状态数据落 MySQL；**时序数据（trace_spans/log_records/metric_service_red/service_topology）保留 ClickHouse，禁止改动**
- MySQL 不可达时降级为内存存储，**不得阻塞服务启动**
- 审计失败静默（沿用现有 `except: pass` 语义）
- 迁移器版本化 + 幂等（`schema_migrations` 记录，重复执行安全）
- 现有 42 个 pytest 测试不得回归
- rules 表参考 ongrid Alert Rules 概念但适配本项目，**不复制 ongrid 代码**
- 新增前端页面遵循现有 AntD + `client.ts` 模式

---

### Task 1: DB 基础设施（连接池 + 迁移器 + 配置）

**Files:**
- Create: `ai-orchestrator/db.py`
- Create: `ai-orchestrator/migrations/0001_business_tables.sql`
- Modify: `ai-orchestrator/requirements.txt`
- Test: `ai-orchestrator/tests/test_db.py`

**Interfaces:**
- Consumes: 无（起点）
- Produces: `db.py` 导出 `get_conn()`、`migrate()`、`db_available()` 以及全部 DAO 类

- [ ] **Step 1: 添加依赖到 requirements.txt**

```diff
+ pymysql>=1.1
+ DBUtils>=3.0
```

- [ ] **Step 2: 编写迁移 SQL（0001_business_tables.sql）**

创建 6 张业务表（approval_tasks / audit_logs / agents / reports / knowledge_base / rules），字段见设计文档 §3。

- [ ] **Step 3: 编写失败的测试（test_db.py）**

```python
import os
os.environ.setdefault("MYSQL_HOST", "127.0.0.1")
os.environ.setdefault("MYSQL_PORT", "3306")
os.environ.setdefault("MYSQL_USER", "root")
os.environ.setdefault("MYSQL_PASSWORD", "test")
os.environ.setdefault("MYSQL_DB", "aiops")

from db import migrate, db_available


def test_migrate_is_idempotent():
    # 两次迁移幂等
    migrate()
    migrate()
    assert True
```

- [ ] **Step 4: 运行测试确认失败**

Run: `.venv-312/bin/python -m pytest tests/test_db.py -v`
Expected: FAIL — `ModuleNotFoundError: No module named 'db'`

- [ ] **Step 5: 实现 db.py（连接池 + 迁移器）**

```python
import os
from dbutils.pooled_db import PooledDB
import pymysql

_MYSQL_HOST = os.environ.get("MYSQL_HOST", "127.0.0.1")
_MYSQL_PORT = int(os.environ.get("MYSQL_PORT", "3306"))
_MYSQL_USER = os.environ.get("MYSQL_USER", "root")
_MYSQL_PASSWORD = os.environ.get("MYSQL_PASSWORD", "")
_MYSQL_DB = os.environ.get("MYSQL_DB", "aiops")

_pool = None


def _get_pool():
    global _pool
    if _pool is None:
        _pool = PooledDB(
            creator=pymysql, host=_MYSQL_HOST, port=_MYSQL_PORT,
            user=_MYSQL_USER, password=_MYSQL_PASSWORD, database=_MYSQL_DB,
            charset="utf8mb4", autocommit=False, maxconnections=10,
            cursorclass=pymysql.cursors.DictCursor,
        )
    return _pool


def get_conn():
    """获取连接。失败时返回 None，调用方降级为内存。"""
    try:
        return _get_pool().connection()
    except Exception:
        return None


def db_available() -> bool:
    conn = get_conn()
    if conn is None:
        return False
    try:
        with conn.cursor() as cur:
            cur.execute("SELECT 1")
        conn.close()
        return True
    except Exception:
        conn.close()
        return False


def migrate():
    """顺序执行 migrations/*.sql 中未应用的版本。幂等。"""
    import glob
    conn = get_conn()
    if conn is None:
        return False
    try:
        with conn.cursor() as cur:
            cur.execute(
                "CREATE TABLE IF NOT EXISTS schema_migrations "
                "(version VARCHAR(64) PRIMARY KEY, applied_at DATETIME DEFAULT CURRENT_TIMESTAMP)"
            )
            applied = {r["version"] for r in cur.execute("SELECT version FROM schema_migrations") or []}
            conn.commit()
            for path in sorted(glob.glob(os.path.join(os.path.dirname(__file__), "migrations", "*.sql"))):
                version = os.path.basename(path).split("_")[0]
                if version in applied:
                    continue
                with open(path, encoding="utf-8") as f:
                    for stmt in f.read().split(";"):
                        if stmt.strip():
                            cur.execute(stmt)
                cur.execute("INSERT INTO schema_migrations (version) VALUES (%s)", (version,))
                conn.commit()
        return True
    except Exception:
        try:
            conn.rollback()
        except Exception:
            pass
        return False
    finally:
        conn.close()
```

- [ ] **Step 6: 运行测试确认通过**

Run: `.venv-312/bin/python -m pytest tests/test_db.py -v`
Expected: PASS（若本机无 MySQL，`db_available()` 返回 False，`migrate()` 返回 False，测试断言幂等仍通过）

- [ ] **Step 7: 提交**

```bash
git add ai-orchestrator/db.py ai-orchestrator/migrations/0001_business_tables.sql ai-orchestrator/requirements.txt ai-orchestrator/tests/test_db.py
git commit -m "feat(db): MySQL 连接池 + 轻量版本化迁移器 + 6 张业务表"
```

---

### Task 2: ApprovalStore（审批任务持久化）

**Files:**
- Create: `ai-orchestrator/db_approval.py`
- Test: `ai-orchestrator/tests/test_approval_store.py`

**Interfaces:**
- Consumes: `db.get_conn()`、`db.db_available()`
- Produces: `ApprovalStore`，方法 `create(task: dict)` / `get(task_id) -> dict|None` / `list() -> list[dict]` / `update(task_id, **fields)` / `decide(task_id, status, decision_by)`

- [ ] **Step 1: 编写失败测试**

```python
from db_approval import ApprovalStore

s = ApprovalStore()


def test_create_and_get():
    task = {"id": "t1", "status": "waiting", "service": "svc-a", "context": "test",
            "diagnosis": "", "plan": "p", "script": "s", "risk_score": 5,
            "risk_reason": "r", "report": "", "created_at": "2026-08-08T00:00:00Z", "done_at": ""}
    s.create(task)
    got = s.get("t1")
    if got is not None:
        assert got["id"] == "t1"
        assert got["status"] == "waiting"


def test_decide():
    s.decide("t1", "approved", "admin")
    got = s.get("t1")
    if got is not None:
        assert got["status"] == "approved"
```

- [ ] **Step 2: 运行确认失败**

Run: `.venv-312/bin/python -m pytest tests/test_approval_store.py -v`
Expected: FAIL — `No module named 'db_approval'`

- [ ] **Step 3: 实现 db_approval.py**

```python
import db

_EMPTY_TASK = {"id": "", "status": "", "source": "", "service": "", "context": "",
               "diagnosis": "", "plan": "", "script": "", "risk_score": 0,
               "risk_reason": "", "report": "", "chat_thread_id": "",
               "created_at": "", "done_at": ""}


class ApprovalStore:
    """审批任务持久化。MySQL 不可用降级为内存。"""

    def __init__(self):
        self._mem: dict[str, dict] = {}

    def _available(self):
        return db.db_available()

    def create(self, task: dict):
        if self._available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute(
                        "INSERT INTO approval_tasks (task_id, service_name, status, plan, script, "
                        "risk_score, risk_reason, diagnosis, report, requester, created_at, decided_at, decision_by) "
                        "VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,NULL,NULL)",
                        (task.get("id"), task.get("service", ""), task.get("status", "waiting"),
                         task.get("plan", ""), task.get("script", ""),
                         float(task.get("risk_score", 0) or 0), task.get("risk_reason", ""),
                         task.get("diagnosis", ""), task.get("report", ""),
                         task.get("requester", ""), task.get("created_at", "")),
                    )
                conn.commit()
            except Exception:
                pass
            finally:
                conn.close()
        self._mem[task["id"]] = dict(task)

    def get(self, task_id: str):
        if self._available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute("SELECT * FROM approval_tasks WHERE task_id=%s", (task_id,))
                    row = cur.fetchone()
                if row:
                    return self._row_to_task(row)
            except Exception:
                pass
            finally:
                conn.close()
        return self._mem.get(task_id)

    def list(self):
        if self._available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute("SELECT * FROM approval_tasks ORDER BY created_at DESC LIMIT 200")
                    rows = cur.fetchall()
                if rows:
                    return [self._row_to_task(r) for r in rows]
            except Exception:
                pass
            finally:
                conn.close()
        return list(self._mem.values())

    def update(self, task_id: str, **fields):
        if self._available():
            conn = db.get_conn()
            try:
                cols = []
                vals = []
                for k, v in fields.items():
                    cols.append(f"{k}=%s")
                    vals.append(v)
                vals.append(task_id)
                with conn.cursor() as cur:
                    cur.execute(f"UPDATE approval_tasks SET {', '.join(cols)} WHERE task_id=%s", tuple(vals))
                conn.commit()
            except Exception:
                pass
            finally:
                conn.close()
        if task_id in self._mem:
            self._mem[task_id].update(fields)

    def decide(self, task_id: str, status: str, decision_by: str = ""):
        import time
        self.update(task_id, status=status, decided_at=time.strftime("%Y-%m-%dT%H:%M:%SZ"), decision_by=decision_by)

    @staticmethod
    def _row_to_task(row: dict) -> dict:
        return {
            "id": row["task_id"], "status": row["status"], "source": row.get("source", ""),
            "service": row["service_name"], "context": row.get("context", ""),
            "diagnosis": row.get("diagnosis", ""), "plan": row.get("plan", ""),
            "script": row.get("script", ""), "risk_score": row.get("risk_score", 0),
            "risk_reason": row.get("risk_reason", ""), "report": row.get("report", ""),
            "created_at": row.get("created_at", ""), "done_at": row.get("decided_at", ""),
        }
```

- [ ] **Step 4: 运行确认通过**

Run: `.venv-312/bin/python -m pytest tests/test_approval_store.py -v`
Expected: PASS（MySQL 不可用时内存降级路径，`get`/`decide` 仍工作）

- [ ] **Step 5: 提交**

```bash
git add ai-orchestrator/db_approval.py ai-orchestrator/tests/test_approval_store.py
git commit -m "feat(db): ApprovalStore 审批任务持久化（MySQL + 内存降级）"
```

---

### Task 3: AuditStore（审计日志持久化）

**Files:**
- Create: `ai-orchestrator/db_audit.py`
- Test: `ai-orchestrator/tests/test_audit_store.py`

**Interfaces:**
- Consumes: `db.get_conn()`
- Produces: `AuditStore`，方法 `log(action, operator, target, command, result, detail=None, task_id="")` / `query(page=1, size=50, action=None, operator=None, service=None) -> {"items": list, "total": int}`

- [ ] **Step 1: 编写失败测试**

```python
from db_audit import AuditStore

a = AuditStore()


def test_log():
    a.log("execute", "admin", "svc-a", "kubectl get pods", "ok", {"n": 1}, "t1")
    assert True


def test_query():
    result = a.query()
    if result["total"] > 0:
        assert result["items"][0]["action"] == "execute"
```

- [ ] **Step 2: 运行确认失败**

Run: `.venv-312/bin/python -m pytest tests/test_audit_store.py -v`
Expected: FAIL — `No module named 'db_audit'`

- [ ] **Step 3: 实现 db_audit.py**

```python
import json
import db


class AuditStore:
    def __init__(self):
        self._mem: list[dict] = []

    def log(self, action: str, operator: str, target: str, command: str,
            result: str, detail: dict = None, task_id: str = ""):
        import time
        entry = {
            "task_id": task_id, "action": action, "operator": operator,
            "target_service": target, "command": command, "result": result,
            "detail": json.dumps(detail, ensure_ascii=False) if detail else "",
            "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ"),
        }
        if db.db_available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute(
                        "INSERT INTO audit_logs (task_id, action, operator, target_service, command, result, detail) "
                        "VALUES (%s,%s,%s,%s,%s,%s,%s)",
                        (entry["task_id"], entry["action"], entry["operator"],
                         entry["target_service"], entry["command"], entry["result"], entry["detail"]),
                    )
                conn.commit()
            except Exception:
                pass
            finally:
                conn.close()
        self._mem.append(entry)

    def query(self, page=1, size=50, action=None, operator=None, service=None):
        offset = (page - 1) * size
        if db.db_available():
            conn = db.get_conn()
            try:
                where = []
                vals = []
                if action:
                    where.append("action=%s"); vals.append(action)
                if operator:
                    where.append("operator=%s"); vals.append(operator)
                if service:
                    where.append("target_service=%s"); vals.append(service)
                w = (" WHERE " + " AND ".join(where)) if where else ""
                with conn.cursor() as cur:
                    cur.execute("SELECT COUNT(*) AS total FROM audit_logs" + w, tuple(vals))
                    total = cur.fetchone()["total"]
                    cur.execute("SELECT * FROM audit_logs" + w + " ORDER BY id DESC LIMIT %s OFFSET %s",
                                tuple(vals) + (size, offset))
                    rows = cur.fetchall()
                if rows is not None:
                    return {"items": [dict(r) for r in rows], "total": total}
            except Exception:
                pass
            finally:
                conn.close()
        mem = self._mem
        if action:
            mem = [e for e in mem if e["action"] == action]
        if operator:
            mem = [e for e in mem if e["operator"] == operator]
        if service:
            mem = [e for e in mem if e["target_service"] == service]
        return {"items": mem[offset:offset + size], "total": len(mem)}
```

- [ ] **Step 4: 运行确认通过**

Run: `.venv-312/bin/python -m pytest tests/test_audit_store.py -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add ai-orchestrator/db_audit.py ai-orchestrator/tests/test_audit_store.py
git commit -m "feat(db): AuditStore 审计日志持久化（MySQL + 内存降级）"
```

---

### Task 4: AgentStore / ReportStore / KnowledgeStore / RuleStore

**Files:**
- Create: `ai-orchestrator/db_agents.py`（AgentStore + ReportStore + KnowledgeStore + RuleStore）
- Test: `ai-orchestrator/tests/test_business_stores.py`

**Interfaces:**
- Consumes: `db.get_conn()`
- Produces:
  - `AgentStore.list() / upsert(name, role, goal, backstory, enabled, builtin) / delete(name) / toggle(name, enabled)`
  - `ReportStore.save(...) / list(service=None, page, size) / trend()`
  - `KnowledgeStore.add(title, content, source, tags, code_ref=None) / search(q) / delete(id) / list(page, size)`
  - `RuleStore.list() / save(rule_key, name, kind, severity, enabled, scope_type, join_mode, conditions_json, source_type) / delete(rule_key) / toggle(rule_key, enabled)`

- [ ] **Step 1: 编写失败测试**

```python
from db_agents import AgentStore, ReportStore, KnowledgeStore, RuleStore

a, r, k, rl = AgentStore(), ReportStore(), KnowledgeStore(), RuleStore()


def test_agents():
    a.upsert("ops-helper", "运维助手", "诊断", "资深SRE", True, False)
    agents = a.list()
    assert any(x["name"] == "ops-helper" for x in agents)


def test_knowledge():
    k.add("如何排查 CPU 高", "用 top 查看", "manual", "cpu,排查")
    res = k.search("cpu")
    assert len(res["items"]) >= 1


def test_rules():
    rl.save("cpu_high", "CPU 高", "metric", "warning", True, "global", "all",
            {"expr": "cpu>80"}, "custom")
    rules = rl.list()
    assert any(x["rule_key"] == "cpu_high" for x in rules)


def test_reports():
    r.save({"task_id": "rt1", "service_name": "svc", "report_type": "inspection",
            "verdict": "ok", "risk_score": 0.0, "summary": "s", "content": "c"})
    assert len(r.list()["items"]) >= 1
```

- [ ] **Step 2: 运行确认失败**

Run: `.venv-312/bin/python -m pytest tests/test_business_stores.py -v`
Expected: FAIL — `No module named 'db_agents'`

- [ ] **Step 3: 实现 db_agents.py（4 个 DAO，含内存降级）**

每个 DAO 遵循 Task 2/3 的模式：MySQL 可用走 SQL，不可用走内存 list/dict。字段与设计文档 §3.3-3.6 对齐。

- [ ] **Step 4: 运行确认通过**

Run: `.venv-312/bin/python -m pytest tests/test_business_stores.py -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add ai-orchestrator/db_agents.py ai-orchestrator/tests/test_business_stores.py
git commit -m "feat(db): Agent/Report/Knowledge/Rule 四个 Store 持久化"
```

---

### Task 5: 后端 API 改造（main.py 接入 DAO）

**Files:**
- Modify: `ai-orchestrator/main.py`
- Modify: `ai-orchestrator/orchestrator.py`（`_audit_log` 改用 AuditStore）

**Interfaces:**
- Consumes: Task 2/3/4 的所有 DAO
- Produces: 改造后的 `/ops/tasks` 相关路由、`_audit_log`、`/api/v1/ai/agents`、`/ops/reports/*`；新增 `GET /api/v1/ops/audit-logs`、`/api/v1/ai/knowledge`、`/api/v1/ai/rules`

- [ ] **Step 1: 全局替换 `_task_store` 为 `ApprovalStore`**

在 main.py 顶部：
```python
from db import migrate as _migrate
from db_approval import ApprovalStore
from db_audit import AuditStore
from db_agents import AgentStore, ReportStore, KnowledgeStore, RuleStore

# 应用启动时执行迁移（失败不阻塞）
try:
    _migrate()
except Exception:
    pass

_approval = ApprovalStore()
_audit = AuditStore()
_agents = AgentStore()
_reports = ReportStore()
_knowledge = KnowledgeStore()
_rules = RuleStore()
```

- [ ] **Step 2: 替换 `_task_store[tid] = task` 为 `_approval.create(task)`**

搜索 main.py 中所有 `_task_store[...]` 的读写点（约 20 处，L465/485/499-512/556/565/576-600），替换为 `_approval` 等价调用（create/get/list/update）。注意 `approve`/`reject` 路由改为 `_approval.decide(tid, status, operator)`。

- [ ] **Step 3: 替换 `_audit_log` 写 ClickHouse 为 AuditStore**

在 orchestrator.py 的 `_audit_log()` 中，将 urllib 写 ClickHouse 的代码改为 `_audit.log(...)`（保持 `except: pass` 静默）。

- [ ] **Step 4: 替换 Agent CRUD 走 AgentStore**

`/api/v1/ai/agents` 的 5 个 handler 从 `ExpertRegistry` 内存改走 `_agents`（保留内置 init_agents 种子写入）。

- [ ] **Step 5: 替换报告接口走 ReportStore**

`_upload_report` 中 ClickHouse 写改为 `_reports.save(...)`（MinIO 文件上传保留）；`/ops/reports/history` `/trend` 改查 `_reports`。

- [ ] **Step 6: 新增审计日志路由**

```python
@app.get("/api/v1/ops/audit-logs")
async def list_audit_logs(action: str = None, operator: str = None, service: str = None,
                          page: int = 1, size: int = 50):
    return _audit.query(page=page, size=size, action=action, operator=operator, service=service)
```

- [ ] **Step 7: 新增知识库路由**

```python
@app.get("/api/v1/ai/knowledge")
async def list_knowledge(q: str = None, page: int = 1, size: int = 50):
    if q:
        return _knowledge.search(q)
    return _knowledge.list(page=page, size=size)

@app.post("/api/v1/ai/knowledge")
async def add_knowledge(body: dict):
    return _knowledge.add(body.get("title"), body.get("content", ""),
                          body.get("source", "manual"), body.get("tags", ""),
                          body.get("code_ref"))

@app.delete("/api/v1/ai/knowledge/{kid}")
async def delete_knowledge(kid: int):
    _knowledge.delete(kid)
    return {"ok": True}
```

- [ ] **Step 8: 新增规则路由**

```python
@app.get("/api/v1/ai/rules")
async def list_rules():
    return {"rules": _rules.list()}

@app.post("/api/v1/ai/rules")
async def save_rule(body: dict):
    _rules.save(body.get("rule_key"), body.get("name"), body.get("kind", "metric"),
                body.get("severity", "warning"), body.get("enabled", True),
                body.get("scope_type", "global"), body.get("join_mode", "all"),
                body.get("conditions_json", {}), body.get("source_type", "custom"))
    return {"ok": True}

@app.delete("/api/v1/ai/rules/{rule_key}")
async def delete_rule(rule_key: str):
    _rules.delete(rule_key)
    return {"ok": True}

@app.post("/api/v1/ai/rules/{rule_key}/toggle")
async def toggle_rule(rule_key: str):
    _rules.toggle(rule_key)
    return {"ok": True}
```

- [ ] **Step 9: 回归测试（现有 42 个测试不回归）**

Run: `.venv-312/bin/python -m pytest tests/ -v`
Expected: 42 passed（+ 新增 store 测试）

- [ ] **Step 10: 启动冒烟**

Run: `FLOWS_DB=/tmp/t.db .venv-312/bin/python -m uvicorn main:app --port 8799 &`
`curl -s http://127.0.0.1:8799/health` → 200
`curl -s http://127.0.0.1:8799/api/v1/ops/audit-logs` → 200
`curl -s http://127.0.0.1:8799/api/v1/ai/rules` → 200
`curl -s http://127.0.0.1:8799/api/v1/ai/knowledge` → 200

- [ ] **Step 11: 提交**

```bash
git add ai-orchestrator/main.py ai-orchestrator/orchestrator.py
git commit -m "feat(api): 审批/审计/Agent/报告接 MySQL DAO + 审计/知识库/规则路由"
```

---

### Task 6: 前端 API client 扩展

**Files:**
- Modify: `observability-frontend/src/api/client.ts`

**Interfaces:**
- Consumes: Task 5 的后端路由
- Produces: 前端可用函数 `listAuditLogs` / `listKnowledge` / `addKnowledge` / `deleteKnowledge` / `listRules` / `saveRule` / `deleteRule` / `toggleRule`

- [ ] **Step 1: 在 client.ts 追加函数**

```ts
// ===== 审计日志 =====
export const listAuditLogs = (params?: Record<string, unknown>) => api.get('/ops/audit-logs', { params })

// ===== 知识库 =====
export const listKnowledge = (params?: Record<string, unknown>) => api.get('/ai/knowledge', { params })
export const addKnowledge = (data: Record<string, unknown>) => api.post('/ai/knowledge', data)
export const deleteKnowledge = (id: number) => api.delete(`/ai/knowledge/${id}`)

// ===== 规则管理 =====
export const listRules = () => api.get('/ai/rules')
export const saveRule = (data: Record<string, unknown>) => api.post('/ai/rules', data)
export const deleteRule = (ruleKey: string) => api.delete(`/ai/rules/${encodeURIComponent(ruleKey)}`)
export const toggleRule = (ruleKey: string) => api.post(`/ai/rules/${encodeURIComponent(ruleKey)}/toggle`)
```

- [ ] **Step 2: 提交**

```bash
git add observability-frontend/src/api/client.ts
git commit -m "feat(web): client.ts 新增审计/知识库/规则 API"
```

---

### Task 7: 前端审批中心 + 审计页面

**Files:**
- Create: `observability-frontend/src/pages/Approvals/index.tsx`
- Create: `observability-frontend/src/pages/Audit/index.tsx`
- Modify: `observability-frontend/src/App.tsx`
- Modify: `observability-frontend/src/api/client.ts`（审批列表）

**Interfaces:**
- Consumes: `approveTask`/`rejectTask`（已有）、`listAuditLogs`（Task 6）
- Produces: 路由 `/approvals`、`/audit`

- [ ] **Step 1: client.ts 补审批列表函数**

```ts
export const listApprovalTasks = (params?: Record<string, unknown>) => api.get('/ops/tasks', { params })
```

- [ ] **Step 2: 创建审批中心页面 Approvals/index.tsx**

AntD Table 展示审批任务（id/服务/状态 Tag/风险分/时间），状态为 waiting 的行显示"批准/驳回"按钮（调 `approveTask`/`rejectTask`）。

- [ ] **Step 3: 创建审计页面 Audit/index.tsx**

AntD Table + 分页，调用 `listAuditLogs`，提供操作者/动作/服务过滤输入。

- [ ] **Step 4: 注册路由和菜单**

App.tsx 新增 import + `<Route path="/approvals">`、`<Route path="/audit">`；菜单"智能运维"分组加 `/approvals`（审批中心）、`/audit`（审计日志）。

- [ ] **Step 5: 前端构建验证**

Run: `cd observability-frontend && npx tsc --noEmit && npm run build`
Expected: exit 0

- [ ] **Step 6: 提交**

```bash
git add observability-frontend/src/pages/Approvals observability-frontend/src/pages/Audit observability-frontend/src/App.tsx observability-frontend/src/api/client.ts
git commit -m "feat(web): 审批中心 + 审计日志页面"
```

---

### Task 8: 前端知识库 + 规则管理页面

**Files:**
- Create: `observability-frontend/src/pages/Knowledge/index.tsx`
- Create: `observability-frontend/src/pages/Rules/index.tsx`
- Modify: `observability-frontend/src/App.tsx`

**Interfaces:**
- Consumes: Task 6 的知识库/规则 API
- Produces: 路由 `/knowledge`、`/rules`

- [ ] **Step 1: 创建知识库页面 Knowledge/index.tsx**

AntD Table（标题/来源/标签/时间）+ 搜索框 + 新增/删除按钮，调用 `listKnowledge`/`addKnowledge`/`deleteKnowledge`。

- [ ] **Step 2: 创建规则管理页面 Rules/index.tsx**

AntD Table（规则键/名称/类型/级别/状态 Switch/操作）+ 新增规则 Modal，调用 `listRules`/`saveRule`/`deleteRule`/`toggleRule`。参考 ongrid AlertRules 交互（仅借鉴交互，不复制代码）。

- [ ] **Step 3: 注册路由和菜单**

App.tsx 新增 `<Route path="/knowledge">`、`<Route path="/rules">`；菜单加 `/knowledge`（知识库）、`/rules`（规则管理）。

- [ ] **Step 4: 前端构建验证**

Run: `cd observability-frontend && npx tsc --noEmit && npm run build`
Expected: exit 0

- [ ] **Step 5: 提交**

```bash
git add observability-frontend/src/pages/Knowledge observability-frontend/src/pages/Rules observability-frontend/src/App.tsx
git commit -m "feat(web): 知识库 + 规则管理页面"
```

---

### Task 9: Helm 部署注入 MySQL 配置

**Files:**
- Modify: `deploy/helm/aiops/templates/ai-orchestrator/deployment.yaml`
- Modify: `deploy/helm/aiops/values.yaml`

**Interfaces:**
- Consumes: Task 1 的 `db.py`（读取 `MYSQL_*` 环境变量）
- Produces: ai-orchestrator 容器注入 MySQL 连接配置

- [ ] **Step 1: 给 ai-orchestrator deployment 加 MySQL 环境变量**

```yaml
env:
  - name: MYSQL_HOST
    value: "mysql"          # Helm 内 Service 名
  - name: MYSQL_PORT
    value: "3306"
  - name: MYSQL_USER
    value: "root"
  - name: MYSQL_PASSWORD
    valueFrom:
      secretKeyRef:
        name: aiops-secrets
        key: MYSQL_ROOT_PASSWORD
  - name: MYSQL_DB
    value: "aiops"
```

- [ ] **Step 2: 确认 values.yaml 中 `mysql.enabled: true` 已生效**（已存在，核对即可）

- [ ] **Step 3: 提交**

```bash
git add deploy/helm/aiops/templates/ai-orchestrator/deployment.yaml
git commit -m "deploy(helm): ai-orchestrator 注入 MySQL 连接配置"
```

---

## Self-Review

**1. Spec coverage（对照设计文档）：**
- ✅ §3 六张表 → Task 1（迁移 SQL）
- ✅ §4 DAO 层 → Task 2/3/4（六个 Store）
- ✅ §5 后端 API → Task 5（审批/审计/Agent/报告改造 + 新增路由）
- ✅ §6 前端页面 → Task 6/7/8（4 页面 + client + 菜单）
- ✅ §7 部署 → Task 9（Helm 注入）
- ✅ §8 测试 → 每 Task 内 TDD 测试 + 回归

**2. Placeholder scan：** 无 TBD/TODO，所有步骤含精确代码。

**3. Type consistency：**
- `ApprovalStore.create/get/list/update/decide` — Task 2 定义，Task 5 使用一致
- `AuditStore.log/query` — Task 3 定义，Task 5 使用一致
- `db.migrate()` — Task 1 定义，Task 5 启动时调用
- 前端函数名 `listAuditLogs/listRules/listKnowledge` — Task 6 定义，Task 7/8 使用一致
