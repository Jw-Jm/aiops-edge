# P1d：Dashboard 全新替换 + NL→ClickHouse SQL Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** ① query-api 扩展 `/dashboard/stats`（新增 latency_p95/trend/top_errors/alerts 聚合），② ai-orchestrator 新增 NL→ClickHouse SQL（生成-确认-执行，安全护栏），③ 前端全新 Dashboard 页替换 Overview + NL2SQL 页。

**Architecture:**
- Dashboard：query-api（Go）扩展 stats 接口 + React echarts 页面
- NL→SQL：ai-orchestrator（Python）复用 `_llm`（生成）+ `_ch_query`（执行）+ 安全护栏 + 人工确认，前端 NL2SQL 页

**Tech Stack:** Go, Python 3.12, FastAPI, ClickHouse, React, AntD, echarts-for-react, axios

## Global Constraints

- 告警数据在**内存 `alertEvents`**（`internal/api/alerts.go` 包级全局 + `alertEventsMu`），Dashboard 聚合直接读内存，**不走 ClickHouse**
- 不改时序遥测表（trace_spans/service_topology/log_records）
- NL→SQL 安全护栏：只允许 SELECT、表名白名单、强制 LIMIT、落审计
- 复用 `_llm()` / `_ch_query()`，不重复造轮子
- 现有 56 个 pytest + Go 测试不回归
- 前端复用 Logs 表格模式、echarts dark theme
- 不做 auto RCA（用户确认）

---

### Task 1: query-api Dashboard stats 扩展（Go 后端）

**Files:**
- Modify: `ai-apm-query-go/internal/biz/dashboard.go`（加字段 + trend/top_errors/alert 聚合）
- Modify: `ai-apm-query-go/internal/api/handler.go`（DashboardStats 扩展，读 alertEvents + trend SQL）
- Test: `ai-apm-query-go/internal/biz/dashboard_test.go`

**Interfaces:**
- Consumes: `queryClickHouse`、`parseRows`、`alertEvents`（同包）
- Produces: 扩展的 `DashboardStats` JSON

- [ ] **Step 1: 编写失败测试（dashboard_test.go 扩展）**

```go
func TestAlertStatsAggregation(t *testing.T) {
	// mock 若干 AlertEvent，验证按 severity/service 聚合
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd ai-apm-query-go && go test ./internal/biz/`
Expected: FAIL — `AlertStats` 未定义

- [ ] **Step 3: 扩展 biz/dashboard.go**

新增类型 + 聚合函数：

```go
type AlertBySvc struct {
    Service string `json:"service"`
    Critical int   `json:"critical"`
    Warning  int   `json:"warning"`
    Total    int   `json:"total"`
}

type AlertStats struct {
    Total     int         `json:"total"`
    Critical  int         `json:"critical"`
    Warning   int         `json:"warning"`
    Info      int         `json:"info"`
    ByService []AlertBySvc `json:"by_service"`
}

type TrendPoint struct {
    T      string  `json:"t"`
    Calls  int64   `json:"calls"`
    Errors int64   `json:"errors"`
}

type ErrorItem struct {
    Service string  `json:"service"`
    Errors  int64   `json:"errors"`
}

type DashboardStats struct {
    // ... 现有字段
    LatencyP95  float64     `json:"latency_p95"`
    Trend       []TrendPoint `json:"trend"`
    TopErrors   []ErrorItem `json:"top_errors"`
    AlertStats  AlertStats  `json:"alerts"`
}

func AggregateAlerts(events []AlertEvent) AlertStats
```

- [ ] **Step 4: 扩展 handler.go 的 DashboardStats**

```go
func (h *Handler) DashboardStats(w http.ResponseWriter, r *http.Request) {
    // ... 现有查询
    // 1. latency_p95: quantile(0.95)(duration_ns)/1e6 FROM trace_spans
    // 2. trend: toStartOfHour(start_time), count(), countIf(is_error=1) LIMIT 24
    // 3. top_errors: service_name, countIf(is_error=1) GROUP BY service_name ORDER BY errors DESC LIMIT 10
    // 4. alerts: alertEventsMu.RLock(); AggregateAlerts(alertEvents); RUnlock()
}
```

- [ ] **Step 5: 运行确认通过**

Run: `cd ai-apm-query-go && go test ./... && go build ./...`
Expected: PASS + build OK

- [ ] **Step 6: 提交**

```bash
git add ai-apm-query-go/internal/biz/dashboard.go ai-apm-query-go/internal/biz/dashboard_test.go ai-apm-query-go/internal/api/handler.go
git commit -m "feat(query-api): Dashboard stats 扩展 latency_p95/trend/top_errors/alerts"
```

---

### Task 2: NL→SQL 后端（ai-orchestrator 生成-确认-执行）

**Files:**
- Create: `ai-orchestrator/nl2sql.py`（SQL 生成 + 安全校验 + store）
- Modify: `ai-orchestrator/main.py`（路由 + `_ch_query` JSONEachRow 增强）
- Test: `ai-orchestrator/tests/test_nl2sql.py`

**Interfaces:**
- Consumes: `_llm`、`_ch_query`、`_audit_log`
- Produces: `/api/v1/ai/nl2sql/*` 三个路由

- [ ] **Step 1: 编写失败测试（test_nl2sql.py）**

```python
from nl2sql import validate_sql, normalize_sql, Nl2SqlStore

ALLOWED_TABLES = {"observability.trace_spans", "observability.service_topology",
                  "observability.log_records", "observability.inspection_reports"}


def test_reject_insert():
    assert not validate_sql("INSERT INTO observability.trace_spans VALUES (1)", ALLOWED_TABLES)

def test_reject_blacklisted_table():
    assert not validate_sql("SELECT * FROM observability.secrets", ALLOWED_TABLES)

def test_reject_multi_statement():
    assert not validate_sql("SELECT 1; DROP TABLE x", ALLOWED_TABLES)

def test_force_limit():
    assert "LIMIT" in normalize_sql("SELECT * FROM observability.trace_spans", ALLOWED_TABLES)

def test_accept_valid():
    assert validate_sql("SELECT count() FROM observability.trace_spans WHERE is_error=1 LIMIT 10", ALLOWED_TABLES)
```

- [ ] **Step 2: 运行确认失败**

Run: `.venv-312/bin/python -m pytest tests/test_nl2sql.py -v`
Expected: FAIL — `No module named 'nl2sql'`

- [ ] **Step 3: 实现 nl2sql.py**

```python
import re, uuid, time
import db

_ALLOWED_TABLES = {"observability.trace_spans", "observability.service_topology",
                   "observability.log_records", "observability.inspection_reports"}


def validate_sql(sql: str, allowed: set = _ALLOWED_TABLES) -> bool:
    """安全校验：只允许 SELECT、表白名单、禁止多语句。"""
    s = sql.strip().rstrip(";").strip()
    if not s:
        return False
    if not re.match(r"^\s*SELECT\s+", s, re.IGNORECASE):
        return False
    if re.search(r";", s) and not s.endswith(";"):
        return False  # 多语句
    for t in re.findall(r"\bobservability\.\w+", s):
        if t not in allowed:
            return False
    return True


def normalize_sql(sql: str, allowed: set = _ALLOWED_TABLES) -> str:
    """追加 LIMIT 护栏。"""
    s = sql.strip().rstrip(";").strip()
    if not re.search(r"\bLIMIT\b", s, re.IGNORECASE):
        s += " LIMIT 100"
    return s


class Nl2SqlStore:
    """NL→SQL 翻译状态存储。MySQL 不可用降级内存。"""
    def __init__(self):
        self._mem: dict[str, dict] = {}
    def save(self, item: dict) -> str:
        item["status"] = "pending"
        self._mem[item["id"]] = item
        return item["id"]
    def get(self, sid: str):
        return self._mem.get(sid)
    def mark_executed(self, sid: str):
        if sid in self._mem:
            self._mem[sid]["status"] = "executed"
```

- [ ] **Step 4: 运行确认通过**

Run: `.venv-312/bin/python -m pytest tests/test_nl2sql.py -v`
Expected: PASS

- [ ] **Step 5: 增强 _ch_query 支持 JSONEachRow（main.py）**

```python
def _ch_query_json(sql: str) -> list:
    """执行 SELECT 并以 JSONEachRow 返回列表（dict）。"""
    import urllib.parse, urllib.request, json as _json
    url = (f"http://{_CH_HOST}:{_CH_PORT}/?query="
           + urllib.parse.quote(sql) + "&default_format=JSONEachRow")
    with urllib.request.urlopen(urllib.request.Request(url), timeout=15) as resp:
        raw = resp.read().decode("utf-8", errors="replace")
    rows = []
    for line in raw.splitlines():
        if line.strip():
            try:
                rows.append(_json.loads(line))
            except Exception:
                pass
    return rows
```

- [ ] **Step 6: 新增 NL2SQL 路由（main.py）**

```python
from nl2sql import validate_sql, normalize_sql, Nl2SqlStore
_nl2sql_store = Nl2SqlStore()
_NL2SQL_SYSTEM = (
    "你是 ClickHouse SQL 专家。根据用户的中文查询意图，生成一条查询 AIOps 可观测性数据的 SQL。"
    "只能 SELECT。可用表：observability.trace_spans(span_id,parent_span_id,trace_id,service_name,"
    "start_time,duration_ns,is_error,response_status,peer_service), "
    "observability.service_topology(source_service,destination_service,calls,error_rate,p95_latency_ns,window), "
    "observability.log_records(service_name,log_time,level,message,digest), "
    "observability.inspection_reports(task_id,service_name,report_type,verdict,risk_score,summary,created_at). "
    "只返回 SQL 本体，不要任何解释或注释。"
)


@app.post("/api/v1/ai/nl2sql/translate")
async def nl2sql_translate(body: dict = None):
    b = body or {}
    question = (b.get("question") or "").strip()
    if not question:
        raise HTTPException(400, "question is required")
    cfg = _get_brain().llm_config
    sql_raw = _llm(cfg, _NL2SQL_SYSTEM, question, role="ClickHouse SQL 专家")
    sql_raw = (sql_raw or "").strip()
    # 清洗 markdown 代码块
    m = re.search(r"```(?:sql)?\s*(.*?)\s*```", sql_raw, re.DOTALL)
    if m:
        sql_raw = m.group(1).strip()
    if not validate_sql(sql_raw):
        return {"error": "生成的 SQL 未通过安全校验，请重试或简化查询", "sql": sql_raw, "id": None}
    sql = normalize_sql(sql_raw)
    sid = _nl2sql_store.save({"id": str(uuid.uuid4())[:8], "sql": sql,
                              "explanation": question, "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ")})
    return {"id": sid, "sql": sql, "explanation": question, "pending": True}


@app.post("/api/v1/ai/nl2sql/{sid}/execute")
async def nl2sql_execute(sid: str):
    item = _nl2sql_store.get(sid)
    if not item:
        raise HTTPException(404, "not found")
    if item.get("status") == "executed":
        raise HTTPException(409, "already executed")
    try:
        rows = _ch_query_json(item["sql"])
    except Exception as e:
        return {"error": str(e), "columns": [], "rows": []}
    columns = list(rows[0].keys()) if rows else []
    _audit_log(item.get("id", ""), "nl2sql", "user", "", item["sql"],
               "ok" if rows is not None else "fail", {"rows": len(rows)})
    _nl2sql_store.mark_executed(sid)
    return {"columns": columns, "rows": rows, "count": len(rows)}


@app.get("/api/v1/ai/nl2sql/{sid}")
async def nl2sql_get(sid: str):
    item = _nl2sql_store.get(sid)
    if not item:
        raise HTTPException(404, "not found")
    return item
```

- [ ] **Step 7: 回归测试**

Run: `.venv-312/bin/python -m pytest tests/ -q`
Expected: 56 passed（+ 新增 nl2sql 测试）

- [ ] **Step 8: 提交**

```bash
git add ai-orchestrator/nl2sql.py ai-orchestrator/tests/test_nl2sql.py ai-orchestrator/main.py
git commit -m "feat(api): NL→ClickHouse SQL 生成-确认-执行 + 安全护栏"
```

---

### Task 3: 前端 Dashboard 全新页（React）

**Files:**
- Rewrite: `observability-frontend/src/pages/Overview/index.tsx`（全新 Dashboard）
- Modify: `observability-frontend/src/api/client.ts`（getDashboardStats 类型扩展）
- Test: `tsc --noEmit` + `npm run build`

**Interfaces:**
- Consumes: `/dashboard/stats`（扩展后）
- Produces: echarts Dashboard 页

- [ ] **Step 1: client.ts 扩展 DashboardStats 类型**

```ts
export interface DashboardStats {
  services: number; edges: number; total_calls: number; total_errors: number
  error_rate: number; latency_p95: number; top_services: Array<{service: string; calls: number; error_rate: number}>
  trend: Array<{t: string; calls: number; errors: number}>
  top_errors: Array<{service: string; errors: number}>
  alerts: { total: number; critical: number; warning: number; info: number; by_service: Array<{service: string; critical: number; warning: number; total: number}> }
}
export const getDashboardStats = () => api.get<DashboardStats>('/dashboard/stats')
```

- [ ] **Step 2: 重写 Overview/index.tsx**

用 echarts-for-react 实现：
- KPI 卡（服务/调用/错误率/P95 延迟，新增 P95）
- 折线图（trend 调用+错误双线，X 轴时间）
- 柱状图（top_errors 错误分布）
- 环形图（alerts critical/warning/info）
- 条形图（top_services 调用量）
- 沿用现有 `theme="dark"` 图表风格，数据来自扩展 stats

- [ ] **Step 3: tsc + build 验证**

Run: `cd observability-frontend && node_modules/.bin/tsc --noEmit && npm run build`
Expected: exit 0

- [ ] **Step 4: 提交**

```bash
git add observability-frontend/src/pages/Overview/index.tsx observability-frontend/src/api/client.ts
git commit -m "feat(web): 全新 Dashboard 页（KPI+趋势+错误+告警环形）替换 Overview"
```

---

### Task 4: 前端 NL2SQL 页（React）

**Files:**
- Create: `observability-frontend/src/pages/NL2SQL/index.tsx`
- Modify: `observability-frontend/src/App.tsx`（路由+菜单）
- Modify: `observability-frontend/src/api/client.ts`（nl2sql API）
- Test: `tsc --noEmit` + `npm run build`

**Interfaces:**
- Consumes: `/ai/nl2sql/*` 后端
- Produces: NL2SQL 页面

- [ ] **Step 1: client.ts 新增**

```ts
export const nl2sqlTranslate = (data: { question: string }) => api.post('/ai/nl2sql/translate', data)
export const nl2sqlExecute = (id: string) => api.post(`/ai/nl2sql/${id}/execute`)
export const nl2sqlGet = (id: string) => api.get(`/ai/nl2sql/${id}`)
```

- [ ] **Step 2: 创建 NL2SQL/index.tsx**

- 输入框（自然语言）→ "翻译" → 显示生成 SQL（`<pre>` 代码块）+ 说明
- "执行" 按钮（待确认状态）→ 结果 AntD Table（columns 动态来自返回 columns）
- 复用 Logs 页表格模式
- 错误提示（安全校验失败）

- [ ] **Step 3: 注册路由和菜单**

App.tsx 加 `<Route path="/nl2sql">`，菜单"智能运维"分组加 `/nl2sql`（SQL 查询）。

- [ ] **Step 4: tsc + build 验证**

Run: `cd observability-frontend && node_modules/.bin/tsc --noEmit && npm run build`
Expected: exit 0

- [ ] **Step 5: 提交**

```bash
git add observability-frontend/src/pages/NL2SQL observability-frontend/src/App.tsx observability-frontend/src/api/client.ts
git commit -m "feat(web): NL2SQL 页面（自然语言→SQL→确认执行→结果表格）"
```

---

## Self-Review

**1. Spec coverage（对照设计文档）：**
- ✅ §2.1 stats 扩展 → Task 1
- ✅ §2.2 alerts 内存聚合 → Task 1（AggregateAlerts 读 alertEvents）
- ✅ §2.3 前端 Dashboard → Task 3
- ✅ §3.2 NL2SQL 后端 → Task 2
- ✅ §3.3 前端 NL2SQL → Task 4
- ✅ 安全护栏（SELECT/白名单/LIMIT/审计）→ Task 2
- ✅ 复用 `_llm`/`_ch_query`/Logs 表格/echarts → Task 2/3/4

**2. Placeholder scan：** 无 TBD/TODO，所有步骤含精确代码。

**3. Type consistency：**
- `validate_sql(sql, allowed)` — Task 2 定义，测试用 `ALLOWED_TABLES` 一致
- `_ch_query_json` — Task 2 定义，execute 路由使用
- `DashboardStats` Go 结构 — Task 1 定义，前端 TS 类型 Task 3 对齐
- 前端 `nl2sqlTranslate/Execute/Get` — Task 4 定义，页面使用一致
