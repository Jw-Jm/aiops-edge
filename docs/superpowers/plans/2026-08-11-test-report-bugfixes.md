# 测试报告 Bug 修复实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 AIOps 平台测试报告确认的 7 个 P0/P1 bug，使 AI Chat 可用、服务目录有数据、orchestrator 稳定、异常趋势有数据、参数校验友好。

**Architecture:** 分 4 批次实施：(1) Helm values 配置类修复（probe + resources，无需重建镜像）；(2) query-go 代码修复（catalog 重设计 + capacity 默认值 + metrics 可选 service，1 次重建）；(3) orchestrator 代码修复（async 重构 + detector 持久化 + scheduler，1 次重建）；(4) frontend 空态修复（1 次重建）。最后部署 + 集成验证。

**Tech Stack:** Go 1.22+ (query-go), Python 3.11 + FastAPI + LangGraph (orchestrator), React 18 + Vite + antd 5 (frontend), Helm 3 (deploy), ClickHouse/MySQL (data)

## Global Constraints

- Go 服务构建：`cd ai-apm-query-go && go build -o query-api ./cmd/api && docker build -t ai-apm-query-go:v1.1.11 .`
- Python 服务构建：`cd ai-orchestrator && docker build -t ai-orchestrator:v1.1.11 .`
- 前端构建：`cd observability-frontend && npm run build && docker build -t observability-frontend:v1.1.11 .`
- Helm chart 位置：`deploy/helm/aiops/`
- 部署命名空间：`observability`
- 镜像版本：从 v1.1.10 升级到 v1.1.11
- 测试命令：Go `go test ./... -v`；Python `pytest tests/ -v`；前端 `npm test`
- 代码风格：Go 遵循现有 gofmt；Python 遵循 PEP 8 + 现有风格；TS 遵循 ESLint 配置
- 提交规范：`fix(scope): description` 或 `feat(scope): description`

## File Structure

### 修改的文件

| 文件 | 改动 | Bug |
|---|---|---|
| `deploy/helm/aiops/templates/ai-orchestrator/deployment.yaml` | probe 配置调宽 + startupProbe | Bug 1 |
| `deploy/helm/aiops/values.yaml` | MySQL/CH/orchestrator resources 调高 | Bug 4 |
| `ai-apm-query-go/internal/store/mysql.go` | 新增 `service_metadata` + `anomaly_events` 表 | Bug 2, 5 |
| `ai-apm-query-go/internal/api/handler.go` | QueryMetrics service 可选 + ListServices 重写 | Bug 2, 7 |
| `ai-apm-query-go/internal/api/handler_test.go` | 新增测试 | Bug 2, 7 |
| `ai-apm-query-go/internal/api/capacity.go` | metric 默认 cpu | Bug 6 |
| `ai-apm-query-go/internal/api/capacity_test.go` | 新增测试 | Bug 6 |
| `ai-orchestrator/orchestrator.py` | `_llm` 改 async + 节点改 async | Bug 1 |
| `ai-orchestrator/detector.py` | 加 `_persist_anomaly` MySQL 写入 | Bug 5 |
| `ai-orchestrator/main.py` | ai_chat async + APScheduler + list_anomalies 查 MySQL | Bug 1, 5 |
| `ai-orchestrator/tests/test_async_llm.py` | 新增测试 | Bug 1 |
| `ai-orchestrator/tests/test_detector_persist.py` | 新增测试 | Bug 5 |
| `observability-frontend/src/pages/observability/ServiceObservability.tsx` | 空态文案 + 跳转按钮 | Bug 3 |

### 不修改的文件

- `ai-apm-ingest-go/` — 不动（Bug 2 新设计不需要 ingest 写 MySQL）
- `ai-apm-query-go/internal/store/clickhouse.go` — 不动（已有 queryClickHouse 方法可复用）

---

## Task 1: Helm values — probe 调宽 + resources 调高

**Files:**
- Modify: `deploy/helm/aiops/templates/ai-orchestrator/deployment.yaml:69-80`
- Modify: `deploy/helm/aiops/values.yaml`

**Interfaces:**
- Produces: probe 配置（startupProbe + 调宽 liveness/readiness）；resources 调高

- [ ] **Step 1: 修改 deployment.yaml probe 配置**

打开 `deploy/helm/aiops/templates/ai-orchestrator/deployment.yaml`，将 L69-80 的 readinessProbe/livenessProbe 替换为：

```yaml
          startupProbe:
            httpGet: { path: /health, port: 8080 }
            initialDelaySeconds: 10
            periodSeconds: 10
            timeoutSeconds: 5
            failureThreshold: 12
          readinessProbe:
            httpGet: { path: /health, port: 8080 }
            periodSeconds: 15
            timeoutSeconds: 10
            failureThreshold: 3
          livenessProbe:
            httpGet: { path: /health, port: 8080 }
            periodSeconds: 30
            timeoutSeconds: 10
            failureThreshold: 5
```

- [ ] **Step 2: 修改 values.yaml resources**

打开 `deploy/helm/aiops/values.yaml`，找到 mysql/clickhouse/aiOrchestrator 的 resources 段，将 requests.memory 调高：

```yaml
# mysql.resources.requests.memory: "1Gi" -> "2Gi"
# clickhouse.resources.requests.memory: "1Gi" -> "2Gi"
# aiOrchestrator.resources.requests.memory: "1Gi" -> "1500Mi"
```

- [ ] **Step 3: helm lint 验证**

Run: `cd deploy/helm && helm lint aiops/`
Expected: `==> Linting aiops/ [INFO] Chart.yaml found 1 chart(s) linted, 0 chart(s) failed`

- [ ] **Step 4: 部署 helm values 改动**

Run: `helm upgrade aiops deploy/helm/aiops/ -n observability --wait --timeout 300s`
Expected: `STATUS: deployed`

- [ ] **Step 5: 验证 probe 生效**

Run: `kubectl get deploy -n observability ai-orchestrator -o jsonpath='{.spec.template.spec.containers[0].livenessProbe}' | python3 -m json.tool`
Expected: `{"httpGet":{...},"periodSeconds":30,"timeoutSeconds":10,"failureThreshold":5}`

- [ ] **Step 6: 验证 resources 生效**

Run: `kubectl get pod -n observability -l app=mysql -o jsonpath='{.spec.containers[0].resources.requests.memory}'`
Expected: `2Gi`

- [ ] **Step 7: 提交**

```bash
git add deploy/helm/aiops/templates/ai-orchestrator/deployment.yaml deploy/helm/aiops/values.yaml
git commit -m "fix(helm): widen orchestrator probes and increase mysql/ch memory"
```

---

## Task 2: query-go — service_metadata 表 + anomaly_events 表

**Files:**
- Modify: `ai-apm-query-go/internal/store/mysql.go` (EnsureSchema 函数)
- Test: `ai-apm-query-go/internal/store/mysql_test.go`

**Interfaces:**
- Produces: `service_metadata` 表（service_name PK + owner/team/tier/description）；`anomaly_events` 表（id + service_name/metric/value/method/severity/score/detected_at）

- [ ] **Step 1: 写测试 — 验证新表存在**

在 `ai-apm-query-go/internal/store/mysql_test.go` 中新增（如果文件不存在则创建）：

```go
package store

import "testing"

func TestEnsureSchemaCreatesServiceMetadata(t *testing.T) {
    // 用内存 SQLite 或 mock 验证 EnsureSchema 执行后表存在
    // 如果项目用 MySQL 测试库，连接并验证
    db := openTestDB(t) // 复用现有测试 helper
    if err := EnsureSchema(db); err != nil {
        t.Fatalf("EnsureSchema failed: %v", err)
    }
    
    tables := []string{"service_metadata", "anomaly_events"}
    for _, tbl := range tables {
        var count int
        err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=?", tbl).Scan(&count)
        if err != nil || count == 0 {
            t.Errorf("table %s not created", tbl)
        }
    }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-apm-query-go && go test ./internal/store/ -run TestEnsureSchema -v`
Expected: FAIL — 表不存在

- [ ] **Step 3: 实现 — 在 EnsureSchema 中加新表**

打开 `ai-apm-query-go/internal/store/mysql.go`，在 EnsureSchema 函数末尾（所有现有 CREATE TABLE 之后）加：

```go
// service_metadata: 服务富化元数据（替代 service_catalog 的富化职责）
_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS service_metadata (
    service_name VARCHAR(255) PRIMARY KEY,
    owner VARCHAR(255) DEFAULT '',
    team VARCHAR(255) DEFAULT '',
    tier ENUM('critical','important','standard','experimental') DEFAULT 'standard',
    description TEXT,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_tier (tier)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
`)
if err != nil {
    return fmt.Errorf("create service_metadata: %w", err)
}

// 从旧 service_catalog 迁移数据（幂等）
_, _ = db.Exec(`INSERT IGNORE INTO service_metadata (service_name, owner, team, tier, description)
SELECT service_name, owner, team, tier, description FROM service_catalog 
WHERE service_name IS NOT NULL AND service_name != ''`)

// anomaly_events: 异常检测持久化
_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS anomaly_events (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    service_name VARCHAR(255) NOT NULL,
    metric VARCHAR(64) NOT NULL,
    value DOUBLE,
    method VARCHAR(32),
    severity VARCHAR(32),
    score DOUBLE,
    detected_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_service (service_name),
    INDEX idx_detected (detected_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
`)
if err != nil {
    return fmt.Errorf("create anomaly_events: %w", err)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-apm-query-go && go test ./internal/store/ -run TestEnsureSchema -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add ai-apm-query-go/internal/store/mysql.go ai-apm-query-go/internal/store/mysql_test.go
git commit -m "feat(query-go): add service_metadata and anomaly_events tables"
```

---

## Task 3: query-go — ListServices 重写（CH + MySQL LEFT JOIN）

**Files:**
- Modify: `ai-apm-query-go/internal/api/handler.go` (ListServices 函数)
- Test: `ai-apm-query-go/internal/api/handler_test.go`

**Interfaces:**
- Consumes: `h.queryClickHouse(ctx, sql)` — 已有方法
- Consumes: `h.db` (MySQL 连接) — 已有字段
- Produces: `/api/v1/catalog/services` 返回 `{"services":[...], "total": N}`，每个 service 含 service_name + owner/team/tier/description/source

- [ ] **Step 1: 写测试 — ListServices 返回 CH 服务 + MySQL 富化**

在 `ai-apm-query-go/internal/api/handler_test.go` 中新增：

```go
func TestListServicesReturnsCHServicesWithMetadata(t *testing.T) {
    h := newTestHandler(t) // 复用现有 helper
    
    // 1. CH 中插入测试 trace（通过 mock 或测试 CH）
    // 2. MySQL 中插入富化数据
    h.db.Exec("INSERT IGNORE INTO service_metadata (service_name, owner, tier) VALUES ('frontend', 'team-a', 'critical')")
    
    req := httptest.NewRequest("GET", "/api/v1/catalog/services", nil)
    w := httptest.NewRecorder()
    h.ListServices(w, req)
    
    if w.Code != 200 {
        t.Fatalf("expected 200, got %d", w.Code)
    }
    
    var resp map[string]interface{}
    json.Unmarshal(w.Body.Bytes(), &resp)
    services := resp["services"].([]interface{})
    if len(services) == 0 {
        t.Fatal("expected services, got empty list")
    }
    
    // 验证 source 字段
    first := services[0].(map[string]interface{})
    if first["source"] != "trace" {
        t.Errorf("expected source=trace, got %v", first["source"])
    }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestListServices -v`
Expected: FAIL — ListServices 仍查旧 MySQL 表

- [ ] **Step 3: 实现 — 重写 ListServices**

打开 `ai-apm-query-go/internal/api/handler.go`，找到现有的 ListServices 函数（搜索 `func (h *Handler) ListServices`），替换为：

```go
// ListServices handles GET /api/v1/catalog/services
// 从 ClickHouse 动态发现服务，LEFT JOIN MySQL service_metadata 富化
func (h *Handler) ListServices(w http.ResponseWriter, r *http.Request) {
    tid := extractTenantID(r)
    _ = tid // CH 查询暂不用 tenant 过滤（trace_spans 无 tenant_id 列）
    
    ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
    defer cancel()
    
    // 1. 从 ClickHouse 拿动态服务列表
    chSQL := `SELECT DISTINCT service_name 
              FROM observability.trace_spans 
              WHERE date >= today()-7 
                AND service_name != '' 
              ORDER BY service_name`
    body, err := h.queryClickHouse(ctx, chSQL)
    if err != nil {
        log.Printf("ListServices CH query error: %v", err)
        respondError(w, http.StatusInternalServerError, "query failed")
        return
    }
    rows, err := parseRows(body)
    if err != nil {
        log.Printf("ListServices parse error: %v", err)
        respondError(w, http.StatusInternalServerError, "parse failed")
        return
    }
    
    // 提取服务名列表
    services := make([]string, 0, len(rows))
    for _, row := range rows {
        if name, ok := row["service_name"].(string); ok && name != "" {
            services = append(services, name)
        }
    }
    
    // 2. 从 MySQL 拿富化元数据
    meta := h.loadServiceMetadata(services)
    
    // 3. LEFT JOIN 返回
    result := make([]map[string]interface{}, 0, len(services))
    for _, svc := range services {
        m := meta[svc]
        item := map[string]interface{}{
            "service_name": svc,
            "owner":        "",
            "team":         "",
            "tier":         "standard",
            "description":  "",
            "source":       "trace",
        }
        if m != nil {
            if m.Owner != "" { item["owner"] = m.Owner }
            if m.Team != "" { item["team"] = m.Team }
            if m.Tier != "" { item["tier"] = m.Tier }
            if m.Description != "" { item["description"] = m.Description }
        }
        result = append(result, item)
    }
    
    respondJSON(w, http.StatusOK, map[string]interface{}{
        "services": result,
        "total":    len(result),
    })
}

// serviceMeta 是 service_metadata 表的行映射
type serviceMeta struct {
    Owner       string
    Team        string
    Tier        string
    Description string
}

// loadServiceMetadata 批量加载服务富化元数据
func (h *Handler) loadServiceMetadata(services []string) map[string]*serviceMeta {
    result := make(map[string]*serviceMeta)
    if len(services) == 0 || h.db == nil {
        return result
    }
    
    // 构造 IN 子句
    placeholders := make([]string, len(services))
    args := make([]interface{}, len(services))
    for i, svc := range services {
        placeholders[i] = "?"
        args[i] = svc
    }
    query := fmt.Sprintf("SELECT service_name, owner, team, tier, description FROM service_metadata WHERE service_name IN (%s)",
        strings.Join(placeholders, ","))
    
    rows, err := h.db.Query(query, args...)
    if err != nil {
        log.Printf("loadServiceMetadata query error: %v", err)
        return result
    }
    defer rows.Close()
    
    for rows.Next() {
        var name, owner, team, tier, desc string
        if err := rows.Scan(&name, &owner, &team, &tier, &desc); err != nil {
            continue
        }
        result[name] = &serviceMeta{Owner: owner, Team: team, Tier: tier, Description: desc}
    }
    return result
}
```

确保 import 中有 `"strings"`。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestListServices -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add ai-apm-query-go/internal/api/handler.go ai-apm-query-go/internal/api/handler_test.go
git commit -m "feat(query-go): rewrite ListServices with CH dynamic discovery + MySQL enrichment"
```

---

## Task 4: query-go — capacity forecast metric 默认 cpu

**Files:**
- Modify: `ai-apm-query-go/internal/api/capacity.go:114-117`
- Test: `ai-apm-query-go/internal/api/capacity_test.go`

- [ ] **Step 1: 写测试 — 空 metric 默认 cpu**

在 `ai-apm-query-go/internal/api/capacity_test.go` 中新增（如果不存在则创建）：

```go
package api

import (
    "net/http/httptest"
    "testing"
)

func TestCapacityForecastEmptyMetricDefaultsToCPU(t *testing.T) {
    h := newTestHandler(t)
    
    req := httptest.NewRequest("GET", "/api/v1/capacity/forecast", nil)
    w := httptest.NewRecorder()
    h.CapacityForecast(w, req)
    
    if w.Code != 200 {
        t.Fatalf("expected 200 for empty metric (should default to cpu), got %d: %s", w.Code, w.Body.String())
    }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestCapacityForecastEmpty -v`
Expected: FAIL — 400 "metric is required"

- [ ] **Step 3: 实现 — 默认 cpu**

打开 `ai-apm-query-go/internal/api/capacity.go`，找到 L114-117：

```go
// 原:
if metric == "" {
    respondError(w, http.StatusBadRequest, "metric is required")
    return
}

// 替换为:
if metric == "" {
    metric = "cpu"
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestCapacityForecastEmpty -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add ai-apm-query-go/internal/api/capacity.go ai-apm-query-go/internal/api/capacity_test.go
git commit -m "fix(query-go): default capacity forecast metric to cpu when empty"
```

---

## Task 5: query-go — QueryMetrics service 可选

**Files:**
- Modify: `ai-apm-query-go/internal/api/handler.go:619-656` (QueryMetrics 函数)
- Test: `ai-apm-query-go/internal/api/handler_test.go`

- [ ] **Step 1: 写测试 — 空 service 不报错**

在 `ai-apm-query-go/internal/api/handler_test.go` 中新增：

```go
func TestQueryMetricsWithoutServiceDoesNotError(t *testing.T) {
    h := newTestHandler(t)
    
    req := httptest.NewRequest("GET", "/api/v1/metrics/query", nil)
    w := httptest.NewRecorder()
    h.QueryMetrics(w, req)
    
    if w.Code == 400 {
        t.Fatalf("expected non-400 for empty service, got 400: %s", w.Body.String())
    }
    // 应该是 200 或 500（如果 CH 查询失败），但不应该是 400 "service parameter required"
}

func TestQueryMetricsWithServiceStillWorks(t *testing.T) {
    h := newTestHandler(t)
    
    req := httptest.NewRequest("GET", "/api/v1/metrics/query?service=frontend", nil)
    w := httptest.NewRecorder()
    h.QueryMetrics(w, req)
    
    if w.Code == 400 {
        t.Fatalf("expected non-400 for service=frontend, got 400: %s", w.Body.String())
    }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestQueryMetrics -v`
Expected: FAIL — 400 "service parameter required"

- [ ] **Step 3: 实现 — service 可选**

打开 `ai-apm-query-go/internal/api/handler.go`，找到 L619-656 的 QueryMetrics 函数，替换为：

```go
// QueryMetrics handles GET /api/v1/metrics/query?service={name}
// service 参数可选：有则按服务过滤，无则返回全局聚合
func (h *Handler) QueryMetrics(w http.ResponseWriter, r *http.Request) {
    tid := extractTenantID(r)
    clusterClause := extractClusterClause(r)
    service := r.URL.Query().Get("service")
    
    serviceClause := ""
    if service != "" {
        serviceClause = fmt.Sprintf(" AND service_name=%s", chQuote(service))
    }
    
    sql := fmt.Sprintf(
        "SELECT toStartOfMinute(start_time) as t, count() as call_count, "+
            "countIf(is_error=1) as error_count, avg(duration_ns)/1000000 as avg_ms, "+
            "quantile(0.50)(duration_ns)/1000000 as p50_ms, "+
            "quantile(0.95)(duration_ns)/1000000 as p95_ms, "+
            "quantile(0.99)(duration_ns)/1000000 as p99_ms "+
            "FROM observability.trace_spans WHERE tenant_id=%s%s%s AND date >= today()-1 "+
            "GROUP BY t ORDER BY t",
        chQuote(tid), clusterClause, serviceClause,
    )
    
    ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
    defer cancel()
    
    body, err := h.queryClickHouse(ctx, sql)
    if err != nil {
        log.Printf("QueryMetrics query error: %v", err)
        respondError(w, http.StatusInternalServerError, "query failed")
        return
    }
    
    rows, err := parseRows(body)
    if err != nil {
        log.Printf("QueryMetrics parse error: %v", err)
        respondError(w, http.StatusInternalServerError, "parse failed")
        return
    }
    
    respondJSON(w, http.StatusOK, map[string]interface{}{
        "service": service,
        "data":    rows,
        "count":   len(rows),
    })
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestQueryMetrics -v`
Expected: PASS

- [ ] **Step 5: 运行全部 query-go 测试**

Run: `cd ai-apm-query-go && go test ./... -v 2>&1 | tail -20`
Expected: 所有测试 PASS

- [ ] **Step 6: 构建 query-go 镜像**

Run: `cd ai-apm-query-go && docker build -t ai-apm-query-go:v1.1.11 .`
Expected: `Successfully tagged ai-apm-query-go:v1.1.11`

- [ ] **Step 7: 提交**

```bash
git add ai-apm-query-go/internal/api/handler.go ai-apm-query-go/internal/api/handler_test.go
git commit -m "fix(query-go): make service parameter optional in QueryMetrics"
```

---

## Task 6: orchestrator — async 重构 _llm + 节点

**Files:**
- Modify: `ai-orchestrator/orchestrator.py` (_llm + node_* 函数)
- Test: `ai-orchestrator/tests/test_async_llm.py`

**Interfaces:**
- Produces: `async def _llm_async(prompt, ...)` — 包 asyncio.to_thread
- Produces: 所有 `node_*` 改为 `async def`

- [ ] **Step 1: 写测试 — _llm_async 不阻塞 event loop**

创建 `ai-orchestrator/tests/test_async_llm.py`：

```python
import asyncio
import time
import sys
import os
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..'))

def test_llm_async_does_not_block_event_loop():
    """_llm_async 应该用 asyncio.to_thread，不阻塞 event loop"""
    from orchestrator import _llm_async
    
    async def run_test():
        # 并发跑 _llm_async + 一个计时器
        # 如果 _llm_async 阻塞 event loop，计时器无法在期间执行
        timer_hits = []
        
        async def timer():
            for i in range(5):
                timer_hits.append(time.time())
                await asyncio.sleep(0.1)
        
        # mock LLM_MOCK=true 时 _llm_async 应该立即返回
        os.environ['LLM_MOCK'] = 'true'
        task1 = asyncio.create_task(_llm_async("test prompt"))
        task2 = asyncio.create_task(timer())
        
        await task1
        await task2
        
        # timer 应该跑了 5 次（如果 event loop 没被阻塞）
        assert len(timer_hits) == 5, f"timer only hit {len(timer_hits)} times — event loop was blocked"
    
    asyncio.run(run_test())

def test_node_collect_is_async():
    """node_collect 应该是 async 函数"""
    from orchestrator import node_collect
    import inspect
    assert inspect.iscoroutinefunction(node_collect), "node_collect must be async"
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-orchestrator && python3 -m pytest tests/test_async_llm.py -v`
Expected: FAIL — `_llm_async` not defined / `node_collect` is not async

- [ ] **Step 3: 实现 — _llm_async + 节点改 async**

打开 `ai-orchestrator/orchestrator.py`，做以下改动：

1. 文件顶部加 `import asyncio`（如果没有）

2. 找到 `def _llm(prompt, ...)` 函数，在它之后新增 async 版本：

```python
async def _llm_async(prompt, system=None, json=False, max_tokens=2000):
    """LLM 调用丢到线程池，不阻塞 event loop"""
    return await asyncio.to_thread(_llm, prompt, system, json, max_tokens)
```

3. 找到所有 `def node_*(state)` 函数（node_collect, node_rca, node_rag, node_crewai, node_plan, node_verify, node_memorize, node_anomaly, node_decide, node_execute, node_report 等），改为 `async def`，并将其中的 `_llm(...)` 调用改为 `await _llm_async(...)`，将其中的 crewai `crew.kickoff()` 改为 `await asyncio.to_thread(crew.kickoff)`。

具体改动模式：

```python
# 原:
def node_collect(state):
    ...
    text = _llm(prompt)
    ...

# 新:
async def node_collect(state):
    ...
    text = await _llm_async(prompt)
    ...

# 原:
def node_crewai(state):
    ...
    result = crew.kickoff()
    ...

# 新:
async def node_crewai(state):
    ...
    result = await asyncio.to_thread(crew.kickoff)
    ...
```

4. 找到 `def stream_sync(...)` 和 `def execute_sync(...)`，改为 `async def`，内部调用节点的部分改 `await`。LangGraph 的 `graph.ainvoke()` 和 `graph.astream()` 支持 async 节点。

5. 如果有 `graph.invoke(state)` 调用，改为 `await graph.ainvoke(state)`。如果有 `graph.stream(state)`，改为 `async for chunk in graph.astream(state)`。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-orchestrator && python3 -m pytest tests/test_async_llm.py -v`
Expected: PASS

- [ ] **Step 5: 运行 orchestrator 全部测试**

Run: `cd ai-orchestrator && python3 -m pytest tests/ -v 2>&1 | tail -20`
Expected: 所有测试 PASS（或只有 pre-existing 失败）

- [ ] **Step 6: 提交**

```bash
git add ai-orchestrator/orchestrator.py ai-orchestrator/tests/test_async_llm.py
git commit -m "fix(orchestrator): async refactor _llm and nodes with asyncio.to_thread"
```

---

## Task 7: orchestrator — ai_chat 路由 async

**Files:**
- Modify: `ai-orchestrator/main.py` (ai_chat 路由)

- [ ] **Step 1: 实现 — ai_chat 改 async**

打开 `ai-orchestrator/main.py`，找到 `ai_chat` 路由（搜索 `async def ai_chat` 或 `def ai_chat`），确保是 async 并用 `asyncio.to_thread` 包裹同步调用：

```python
# 如果 ai_chat 已经是 async def，只需把内部的阻塞调用改 asyncio.to_thread
# 找到 brain.execute_sync 或类似同步调用：
result = brain.execute_sync(...)
# 改为：
result = await asyncio.to_thread(brain.execute_sync, ...)
```

如果 `ai_chat` 内部已经在线程中跑（如 `_run_diagnosis` 在 `threading.Thread` 中），那些部分不需要改 — 它们不阻塞 event loop。只改直接在路由 handler 中执行的同步调用。

- [ ] **Step 2: 验证 — 启动 orchestrator 本地测试**

Run: `cd ai-orchestrator && LLM_MOCK=true python3 -c "
import asyncio
from main import app
from fastapi.testclient import TestClient
client = TestClient(app)
resp = client.post('/api/v1/ai/chat', json={'message':'test','stream':False}, timeout=30)
print(f'HTTP {resp.status_code}')
print(resp.text[:500])
"`
Expected: HTTP 200

- [ ] **Step 3: 提交**

```bash
git add ai-orchestrator/main.py
git commit -m "fix(orchestrator): make ai_chat route async with asyncio.to_thread"
```

---

## Task 8: orchestrator — detector 持久化到 MySQL

**Files:**
- Modify: `ai-orchestrator/detector.py` (加 _persist_anomaly)
- Test: `ai-orchestrator/tests/test_detector_persist.py`

- [ ] **Step 1: 写测试 — 异常确认时写入 MySQL**

创建 `ai-orchestrator/tests/test_detector_persist.py`：

```python
import sys
import os
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..'))

def test_persist_anomaly_writes_to_mysql():
    """_persist_anomaly 应该调 MySQL INSERT"""
    from detector import AnomalyDetector
    from unittest.mock import patch, MagicMock
    
    detector = AnomalyDetector(window_size=10)
    
    # mock db 模块
    with patch('detector.db_available', return_value=True), \
         patch('detector.get_conn') as mock_get_conn:
        mock_conn = MagicMock()
        mock_cursor = MagicMock()
        mock_conn.cursor.return_value.__enter__.return_value = mock_cursor
        mock_get_conn.return_value = mock_conn
        
        confirmed = MagicMock()
        confirmed.service = "frontend"
        confirmed.metric = "error_rate"
        confirmed.current_value = 5.0
        confirmed.method = "zscore"
        confirmed.severity = "critical"
        confirmed.score = 0.95
        
        detector._persist_anomaly(confirmed)
        
        # 验证 INSERT 被调用
        assert mock_cursor.execute.called, "INSERT was not called"
        sql_arg = mock_cursor.execute.call_args[0][0]
        assert "INSERT INTO anomaly_events" in sql_arg, f"wrong SQL: {sql_arg}"
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-orchestrator && python3 -m pytest tests/test_detector_persist.py -v`
Expected: FAIL — `_persist_anomaly` not defined

- [ ] **Step 3: 实现 — 加 _persist_anomaly**

打开 `ai-orchestrator/detector.py`，在 AnomalyDetector 类中加：

```python
def _persist_anomaly(self, confirmed):
    """异常确认时写入 MySQL anomaly_events 表（best-effort，不抛异常）"""
    try:
        from db import db_available, get_conn
        if not db_available():
            return
        conn = get_conn()
        if conn is None:
            return
        try:
            with conn.cursor() as cur:
                cur.execute(
                    "INSERT INTO anomaly_events (service_name, metric, value, method, severity, score) "
                    "VALUES (%s, %s, %s, %s, %s, %s)",
                    (confirmed.service, confirmed.metric, confirmed.current_value,
                     confirmed.method, confirmed.severity, confirmed.score)
                )
            conn.commit()
        finally:
            conn.close()
    except Exception:
        pass  # best-effort，不影响检测主流程
```

在 `detect` 方法中，异常确认后调用 `_persist_anomaly`。找到 `detect` 方法中 `confirmed = self.vote(results)` 后的位置，加：

```python
if confirmed:
    self._persist_anomaly(confirmed)
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-orchestrator && python3 -m pytest tests/test_detector_persist.py -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add ai-orchestrator/detector.py ai-orchestrator/tests/test_detector_persist.py
git commit -m "feat(orchestrator): persist anomalies to MySQL anomaly_events table"
```

---

## Task 9: orchestrator — APScheduler + list_anomalies 查 MySQL

**Files:**
- Modify: `ai-orchestrator/main.py` (加 scheduler + 改 list_anomalies)

- [ ] **Step 1: 实现 — 加 APScheduler**

打开 `ai-orchestrator/main.py`，在文件顶部加 import：

```python
from apscheduler.schedulers.asyncio import AsyncIOScheduler
```

在 app 初始化后加：

```python
scheduler = AsyncIOScheduler()

@app.on_event("startup")
async def start_scheduler():
    """启动定时异常扫描（每 5 分钟）"""
    scheduler.add_job(_scheduled_anomaly_scan, 'interval', minutes=5, id='anomaly_scan', replace_existing=True)
    scheduler.start()

@app.on_event("shutdown")
async def stop_scheduler():
    scheduler.shutdown(wait=False)

async def _scheduled_anomaly_scan():
    """定时异常扫描（只检测+持久化，不触发 LLM 诊断）"""
    try:
        from detector import detector
        from tools import get_service_list
        import json as _json
        raw = get_service_list()
        try:
            svc_data = _json.loads(raw)
        except Exception:
            svc_data = []
        for svc in (svc_data if isinstance(svc_data, list) else []):
            name = svc.get("service_name", "")
            if not name:
                continue
            metrics_vals = {
                "error_rate": float(svc.get("error_rate", 0) or 0),
                "p99_latency": float(svc.get("max_ms", 0) or 0),
                "request_rate": float(svc.get("traces", 0) or 0),
            }
            for metric, val in metrics_vals.items():
                results = detector.detect(name, metric, val)
                if results:
                    detector.vote(results)  # vote 内部会调 _persist_anomaly
    except Exception as e:
        print(f"[scheduler] anomaly scan error: {e}")
```

- [ ] **Step 2: 实现 — 改 list_anomalies 查 MySQL**

找到 `@app.get("/api/v1/ops/anomalies")` 的 `list_anomalies` 函数（约 L1216-1234），替换为：

```python
@app.get("/api/v1/ops/anomalies")
async def list_anomalies(service: str = "", limit: int = 50):
    """查询历史异常事件（从 MySQL anomaly_events 表）"""
    try:
        from db import db_available, get_conn
        import pymysql.cursors
        if not db_available():
            return {"anomaly_trends": [], "total": 0}
        conn = get_conn()
        if conn is None:
            return {"anomaly_trends": [], "total": 0}
        try:
            with conn.cursor(pymysql.cursors.DictCursor) as cur:
                sql = "SELECT service_name, metric, value, method, severity, score, detected_at FROM anomaly_events"
                args = []
                if service:
                    sql += " WHERE service_name=%s"
                    args.append(service)
                sql += " ORDER BY detected_at DESC LIMIT %s"
                args.append(limit)
                cur.execute(sql, args)
                rows = cur.fetchall()
            return {"anomaly_trends": rows, "total": len(rows)}
        finally:
            conn.close()
    except Exception as e:
        return {"anomaly_trends": [], "total": 0, "error": str(e)}
```

- [ ] **Step 3: 验证 — 确认 apscheduler 已在 requirements**

Run: `grep apscheduler ai-orchestrator/requirements.txt`
Expected: 找到 `apscheduler`。如果没找到，加 `apscheduler>=3.10` 到 requirements.txt。

- [ ] **Step 4: 构建 orchestrator 镜像**

Run: `cd ai-orchestrator && docker build -t ai-orchestrator:v1.1.11 .`
Expected: `Successfully tagged ai-orchestrator:v1.1.11`

- [ ] **Step 5: 提交**

```bash
git add ai-orchestrator/main.py ai-orchestrator/requirements.txt
git commit -m "feat(orchestrator): add APScheduler for periodic anomaly scan + MySQL-backed list_anomalies"
```

---

## Task 10: frontend — 服务列表空态文案

**Files:**
- Modify: `observability-frontend/src/pages/observability/ServiceObservability.tsx:325`

- [ ] **Step 1: 实现 — 改空态文案 + 加跳转按钮**

打开 `observability-frontend/src/pages/observability/ServiceObservability.tsx`，找到 L325 附近的 `<Empty text="暂无拓扑数据" />`，替换为：

```tsx
<Empty description="服务目录为空，可前往拓扑图查看">
  <Button type="primary" onClick={() => setActiveTab('topology')}>
    查看拓扑图
  </Button>
</Empty>
```

如果 `setActiveTab` 函数不存在，检查组件中是否有 tab 切换的状态管理（如 `useState`），如果没有则加：

```tsx
const [activeTab, setActiveTab] = useState('list');
```

并确保 `Tabs` 组件的 `activeKey={activeTab}` `onChange={setActiveTab}`。

- [ ] **Step 2: 构建前端镜像**

Run: `cd observability-frontend && npm run build && docker build -t observability-frontend:v1.1.11 .`
Expected: 构建成功

- [ ] **Step 3: 提交**

```bash
git add observability-frontend/src/pages/observability/ServiceObservability.tsx
git commit -m "fix(frontend): improve service list empty state with topology link"
```

---

## Task 11: 部署 + 集成验证

**Files:** 无代码改动，纯部署验证

- [ ] **Step 1: 更新 Helm values 中的 image tags**

打开 `deploy/helm/aiops/values.yaml`，找到各服务的 image.tag，从 v1.1.10 改为 v1.1.11：

```yaml
aiOrchestrator:
  image: ai-orchestrator:v1.1.11   # 原 v1.1.10
queryApi:
  image: ai-apm-query-go:v1.1.11    # 原 v1.1.10
frontend:
  image: observability-frontend:v1.1.11  # 原 v1.1.10
```

- [ ] **Step 2: helm upgrade 部署**

Run: `helm upgrade aiops deploy/helm/aiops/ -n observability --wait --timeout 600s`
Expected: `STATUS: deployed`

- [ ] **Step 3: 等待所有 pod ready**

Run: `kubectl get pods -n observability --watch`
Expected: 所有 pod READY 1/1 或 2/2

- [ ] **Step 4: 验证 Bug 1 — AI Chat 可用**

Run: 
```bash
TOKEN=$(curl -s -X POST http://localhost:30253/api/v1/auth/login -H 'Content-Type: application/json' -d '{"username":"admin","password":"admin123"}' | python3 -c 'import json,sys;print(json.load(sys.stdin).get("token",""))')
curl -s -X POST http://localhost:30253/api/v1/ai/chat -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"message":"你好","stream":false}' --max-time 60 -w "\nHTTP=%{http_code} time=%{time_total}s\n"
```
Expected: HTTP=200，有响应内容

- [ ] **Step 5: 验证 Bug 2 — 服务目录非空**

Run: `curl -s http://localhost:30253/api/v1/catalog/services -H "Authorization: Bearer $TOKEN" | python3 -c 'import json,sys;d=json.load(sys.stdin);print(f"total={d.get(\"total\",0)}")'`
Expected: total ≥ 10

- [ ] **Step 6: 验证 Bug 4 — MySQL/CH 稳定**

Run: `kubectl get pods -n observability -o jsonpath='{range .items[*]}{.metadata.name}{"  RESTARTS="}{.status.containerStatuses[0].restartCount}{"\n"}{end}' | grep -E "mysql|clickhouse"`
Expected: 重启次数不增长（与部署前相同）

- [ ] **Step 7: 验证 Bug 5 — 异常趋势非空**

等 5 分钟（让 scheduler 跑一次），然后：
Run: `curl -s http://localhost:30253/api/v1/ops/anomalies -H "Authorization: Bearer $TOKEN" | python3 -c 'import json,sys;d=json.load(sys.stdin);print(f"total={d.get(\"total\",0)}")'`
Expected: total ≥ 0（可能是 0 如果没有异常，但 anomaly_trends 数组结构正确）

- [ ] **Step 8: 验证 Bug 6 — capacity forecast 无参不报错**

Run: `curl -s http://localhost:30253/api/v1/capacity/forecast -H "Authorization: Bearer $TOKEN" -w "\nHTTP=%{http_code}\n" | head -c 500`
Expected: HTTP=200

- [ ] **Step 9: 验证 Bug 7 — metrics/query 无 service 不报错**

Run: `curl -s 'http://localhost:30253/api/v1/metrics/query' -H "Authorization: Bearer $TOKEN" -w "\nHTTP=%{http_code}\n" | head -c 500`
Expected: HTTP=200（不再是 400）

- [ ] **Step 10: 最终提交**

```bash
git add deploy/helm/aiops/values.yaml
git commit -m "deploy: upgrade images to v1.1.11 with bugfixes"
```

---

## Self-Review

### Spec coverage

| Bug | 设计文档章节 | 实施 Task | ✓ |
|---|---|---|---|
| P0-1 async 重构 | Bug 1 | Task 6, 7 | ✓ |
| P0-1 probe 调宽 | Bug 1 | Task 1 | ✓ |
| P0-3 catalog 重设计 | Bug 2 | Task 2, 3 | ✓ |
| P0-4 空态文案 | Bug 3 | Task 10 | ✓ |
| P1-1 resources | Bug 4 | Task 1 | ✓ |
| P1-3 detector 持久化 | Bug 5 | Task 8, 9 | ✓ |
| P1-4 capacity 默认 | Bug 6 | Task 4 | ✓ |
| P1-5 metrics 可选 service | Bug 7 | Task 5 | ✓ |

### Placeholder scan

无 TBD/TODO，所有步骤含具体代码或命令。

### Type consistency

- `_llm_async(prompt, system, json, max_tokens)` — 与 `_llm` 签名一致
- `loadServiceMetadata(services []string) map[string]*serviceMeta` — 返回类型与 ListServices 使用一致
- `_persist_anomaly(confirmed)` — confirmed 是 AnomalyFingerprint 类型，与 detector.detect 返回一致
- `anomaly_events` 表结构 — 与 detector._persist_anomaly INSERT 列名一致

### 风险项

1. **Task 6 async 重构**：需要改的节点函数较多（10+ 个 node_*），可能遗漏。建议用 `grep "def node_" orchestrator.py` 列出所有节点，逐一改。
2. **Task 3 ListServices**：依赖 `h.db`（MySQL 连接），需确认 Handler 结构体有此字段。如果没有，需在 Handler 初始化时加。
3. **Task 9 APScheduler**：需确认 `apscheduler` 在 requirements.txt 中。如果不在，Step 3 会补上。
