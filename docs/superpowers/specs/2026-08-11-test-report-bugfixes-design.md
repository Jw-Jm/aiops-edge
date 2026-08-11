# 测试报告 Bug 修复设计

**日期**：2026-08-11
**状态**：approved（用户已确认方案）
**来源**：[AIOps 平台测试报告](../../) 修正后确认的 7 个 P0/P1 bug

## 背景与目标

对 AIOps 智能可观测平台执行深度测试后，确认 7 个真实 bug（P0-1, P0-3, P0-4, P1-1, P1-3, P1-4, P1-5），原报告中的 P0-2 和 P1-2 经复核撤销（误判）。本设计描述如何修复这 7 个 bug 并重新测试验证。

**成功标准**：
1. AI Chat 端点 200 响应，流式输出正常
2. 服务目录返回 ≥10 条服务（与拓扑节点数一致）
3. orchestrator 24 小时内 0 次非预期重启
4. MySQL/CH 24 小时内 0 次重启
5. `/metrics/query?query=up` 不报错
6. `/capacity/forecast`（无参）不报错
7. `/ops/anomalies` 返回非空趋势（scheduler 运行 5min 后）

---

## Bug 1：P0-1 orchestrator crash-loop（async 重构）

**根因**：`orchestrator.py` 的 `_llm()` 调用 crewai 的 `Crew.kickoff()` 是同步阻塞，卡住 uvicorn event loop，导致 `/health` liveness probe 5s 超时 → kubelet kill → 502。

**方案 C1**：用 `asyncio.to_thread()` 包裹所有同步 LLM 调用，让 FastAPI event loop 保持响应。

### 改动点

#### `ai-orchestrator/orchestrator.py`

```python
import asyncio

# 原: def _llm(prompt, ...): return chat(prompt, ...)
# 新: 
async def _llm_async(prompt, ...):
    """LLM 调用丢到线程池，不阻塞 event loop"""
    return await asyncio.to_thread(chat, prompt, ...)

# 节点内调用改 await _llm_async(...)
async def node_collect(state):
    ...
    text = await _llm_async(prompt)
    ...

async def node_crewai(state):
    ...
    result = await asyncio.to_thread(crew.kickoff)
    ...

async def node_rca(state):
    ...
    rca_result = await asyncio.to_thread(full_rca_analysis, ...)
    ...

async def node_plan(state):
    ...
    plan = await _llm_async(prompt)
    ...
```

LangGraph 节点本就支持 async（`graph.ainvoke()`），改节点为 `async def` 即可。

#### `ai-orchestrator/main.py`

```python
# 原: def ai_chat(...): brain.execute_sync(...)
# 新:
async def ai_chat(...):
    ...
    # 同步阻塞调用丢线程池
    result = await asyncio.to_thread(brain.execute_sync, ...)
```

`_run_diagnosis` 在 `threading.Thread` 内运行，不阻塞 event loop — 保留不变。

#### `deploy/helm/aiops/templates/ai-orchestrator/deployment.yaml`

双保险：probe 也调宽。

```yaml
startupProbe:
  httpGet: { path: /health, port: 8080 }
  initialDelaySeconds: 10
  periodSeconds: 10
  timeoutSeconds: 5
  failureThreshold: 12    # 120s 启动容错（ChromaDB 初始化）
readinessProbe:
  httpGet: { path: /health, port: 8080 }
  periodSeconds: 15
  timeoutSeconds: 10       # 5→10
  failureThreshold: 3
livenessProbe:
  httpGet: { path: /health, port: 8080 }
  periodSeconds: 30        # 15→30
  timeoutSeconds: 10        # 5→10
  failureThreshold: 5       # 3→5（150s 容错）
```

### 验证

1. `kubectl rollout restart deploy/ai-orchestrator`
2. 等 pod ready
3. `curl -X POST /api/v1/ai/chat -d '{"message":"你好","stream":false}' --max-time 60` 返回 200 + 内容
4. `kubectl get pods -l app=ai-orchestrator` 重启次数不增长

---

## Bug 2：P0-3 服务目录数据库划分重设计

**根因**：`service_catalog` 表把"动态发现的服务名"（应来自 ClickHouse）和"静态用户富化元数据"（owner/team/tier）混在 MySQL 一张表，导致 ingest-go 写 trace 后 MySQL 无法自动同步。

**新设计**：

| 数据 | 存储位置 | 写入方 |
|---|---|---|
| 服务名（动态发现） | ClickHouse `trace_spans` | ingest-go（已存在） |
| 服务富化元数据 | MySQL `service_metadata`（新表） | 用户手动（admin UI） |
| `/api/v1/catalog/services` 响应 | query-go 实时 JOIN | CH `DISTINCT service_name` LEFT JOIN MySQL `service_metadata` |

### 改动点

#### `ai-apm-query-go/internal/store/mysql.go`

新增 `service_metadata` 表（替代 `service_catalog` 的富化职责）：

```go
// EnsureSchema 中:
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
```

保留旧 `service_catalog` 表（兼容期），但新写入只走 `service_metadata`。

#### `ai-apm-query-go/internal/api/handler.go` 或 `catalog.go`

`/api/v1/catalog/services` handler 重写：

```go
func (h *Handler) ListServices(w http.ResponseWriter, r *http.Request) {
    // 1. 从 ClickHouse 拿动态服务列表
    chSQL := `SELECT DISTINCT service_name 
              FROM observability.trace_spans 
              WHERE date >= today()-7 
                AND service_name != '' 
              ORDER BY service_name`
    chRows, _ := h.queryClickHouse(ctx, chSQL)
    services := parseServiceNames(chRows)
    
    // 2. 从 MySQL 拿富化元数据
    meta := h.loadServiceMetadata(services)  // SELECT * FROM service_metadata WHERE service_name IN (...)
    
    // 3. LEFT JOIN 返回
    result := []map[string]interface{}{}
    for _, svc := range services {
        m := meta[svc]  // 可能为空
        result = append(result, map[string]interface{}{
            "service_name": svc,
            "owner":        m.Owner,
            "team":         m.Team,
            "tier":         m.Tier,
            "description":  m.Description,
            "source":       "trace",  // 标记来源
        })
    }
    respondJSON(w, 200, map[string]interface{}{
        "services": result, "total": len(result),
    })
}
```

#### Migration

旧 `service_catalog` 表如有数据，迁移到 `service_metadata`：

```sql
INSERT IGNORE INTO service_metadata (service_name, owner, team, tier, description)
SELECT service_name, owner, team, tier, description FROM service_catalog
WHERE service_name IS NOT NULL;
```

在 `EnsureSchema` 中执行（幂等）。

### 验证

1. `curl /api/v1/catalog/services` 返回 ≥10 服务
2. 服务列表数与拓扑节点数一致
3. 新服务（发新 trace 后）自动出现在列表中

---

## Bug 3：P0-4 服务列表空态文案

**根因**：`ServiceObservability.tsx` L325 文案"暂无拓扑数据"误导（拓扑有 13 节点）。

### 改动点

#### `observability-frontend/src/pages/observability/ServiceObservability.tsx`

```tsx
// 原: <Empty text="暂无拓扑数据" />
// 新:
{services.length === 0 ? (
  <Empty description="服务目录为空，可前往拓扑图查看">
    <Button type="primary" onClick={() => setActiveTab('topology')}>
      查看拓扑图
    </Button>
  </Empty>
) : (
  // 原表格渲染
)}
```

### 验证

1. Bug 2 修复前：服务列表 tab 显示新空态 + "查看拓扑图"按钮
2. Bug 2 修复后：服务列表显示数据

---

## Bug 4：P1-1 MySQL/CH 资源不足

**根因**：单节点 14 pod 挤占，MySQL 1Gi / CH 1Gi 内存不足导致 OOMKilled 重启。

### 改动点

#### `deploy/helm/aiops/values.yaml`

```yaml
mysql:
  resources:
    requests:
      cpu: "500m"
      memory: "2Gi"     # 1Gi→2Gi
    limits:
      cpu: "2"
      memory: "4Gi"     # 不变

clickhouse:
  resources:
    requests:
      cpu: "500m"
      memory: "2Gi"     # 1Gi→2Gi
    limits:
      cpu: "2"
      memory: "4Gi"     # 不变

aiOrchestrator:
  resources:
    requests:
      cpu: "500m"
      memory: "1500Mi"  # 1Gi→1.5Gi
    limits:
      cpu: "2"
      memory: "3Gi"     # 不变
```

### 验证

1. `kubectl rollout restart deploy/mysql` (statefulset)
2. `kubectl get pods -l app=mysql` 24h 内 0 重启
3. `kubectl describe pod mysql-0 | grep -i oom` 无 OOMKilled

---

## Bug 5：P1-3 异常趋势空（detector 持久化 + scheduler）

**根因**：`detector.py` 只存内存 `history` dict，无 MySQL 持久化，无定时调度。

### 改动点

#### MySQL 新表

`ai-apm-query-go/internal/store/mysql.go` EnsureSchema 加：

```sql
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
```

#### `ai-orchestrator/detector.py`

`AnomalyDetector.detect()` 确认异常时写入 MySQL：

```python
def detect(self, service, metric, value):
    ...
    if confirmed:
        self._persist_anomaly(confirmed)
    return results

def _persist_anomaly(self, confirmed):
    """异常确认时写入 MySQL anomaly_events 表"""
    if not db_available():
        return
    conn = get_conn()
    if conn is None:
        return
    try:
        with conn.cursor() as cur:
            cur.execute(
                "INSERT INTO anomaly_events (service_name, metric, value, method, severity, score) "
                "VALUES (%s,%s,%s,%s,%s,%s)",
                (confirmed.service, confirmed.metric, confirmed.current_value,
                 confirmed.method, confirmed.severity, confirmed.score)
            )
        conn.commit()
    except Exception:
        pass
    finally:
        conn.close()
```

#### `ai-orchestrator/main.py`

加 APScheduler 定时扫描：

```python
from apscheduler.schedulers.asyncio import AsyncIOScheduler

scheduler = AsyncIOScheduler()

@app.on_event("startup")
async def start_scheduler():
    """每 5 分钟自动扫描异常"""
    scheduler.add_job(scheduled_anomaly_scan, 'interval', minutes=5, id='anomaly_scan')
    scheduler.start()

async def scheduled_anomaly_scan():
    """定时异常扫描（不触发 LLM 诊断，只检测+持久化）"""
    from detector import detector
    from tools import get_service_list
    ...
    # 复用 scan_anomalies 的检测逻辑，但不创建 _task_store 任务
```

#### `ai-orchestrator/main.py` `/api/v1/ops/anomalies` handler 改为查 MySQL

```python
@app.get("/api/v1/ops/anomalies")
async def list_anomalies(service: str = "", limit: int = 50):
    """查询历史异常事件（从 MySQL anomaly_events 表）"""
    if not db_available():
        return {"anomaly_trends": []}
    conn = get_conn()
    if conn is None:
        return {"anomaly_trends": []}
    try:
        with conn.cursor(pymysql.cursors.DictCursor) as cur:
            sql = "SELECT * FROM anomaly_events"
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
```

### 验证

1. `kubectl rollout restart deploy/ai-orchestrator`
2. 等 5 分钟（scheduler 跑一次）
3. `curl /api/v1/ops/anomalies` 返回非空数组
4. MySQL `SELECT COUNT(*) FROM anomaly_events` > 0

---

## Bug 6：P1-4 容量预测参数校验

**根因**：`capacity.go` L114 空 metric 报错。

### 改动点

#### `ai-apm-query-go/internal/api/capacity.go`

```go
// 原:
if metric == "" {
    respondError(w, http.StatusBadRequest, "metric is required")
    return
}

// 新: 默认 cpu
if metric == "" {
    metric = "cpu"
}
```

### 验证

1. `curl /api/v1/capacity/forecast` 不报错，返回 cpu 预测数据

---

## Bug 7：P1-5 metrics/query 强制 service

**根因**：`handler.go` L623-626 要求 service 参数。

### 改动点

#### `ai-apm-query-go/internal/api/handler.go` QueryMetrics

```go
// 原:
service := r.URL.Query().Get("service")
if service == "" {
    respondError(w, http.StatusBadRequest, "service parameter required")
    return
}
sql := fmt.Sprintf("... WHERE tenant_id=%s%s AND service_name=%s ...", tid, clusterClause, chQuote(service))

// 新: service 可选
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
```

### 验证

1. `curl /api/v1/metrics/query?query=up` 不报错（注：此端点是 CH SQL 聚合，不是 PromQL 透传 — query 参数无实际作用，但 service 不再强制）
2. `curl /api/v1/metrics/query?service=frontend` 仍正常返回

---

## 实施顺序与依赖

```
Bug 4 (resources)     ────────────► helm upgrade (独立)
Bug 1 (async refactor) ────────────► 重建 orchestrator 镜像
Bug 5 (detector+scheduler) ────────► 重建 orchestrator 镜像（与 Bug 1 合并）
Bug 2 (catalog redesign) ──────────► 重建 query-go 镜像
Bug 6 (capacity default) ──────────► 重建 query-go 镜像（与 Bug 2 合并）
Bug 7 (metrics optional service) ──► 重建 query-go 镜像（与 Bug 2 合并）
Bug 3 (empty state UI) ────────────► 重建 frontend 镜像
```

**镜像构建批次**：
1. orchestrator: Bug 1 + Bug 5
2. query-go: Bug 2 + Bug 6 + Bug 7
3. frontend: Bug 3
4. helm values: Bug 1 (probe) + Bug 4 (resources)

## 风险与缓解

| 风险 | 缓解 |
|---|---|
| async 重构遗漏阻塞点 | grep 所有 `chat(` 调用，逐一包 `asyncio.to_thread` |
| `service_catalog` → `service_metadata` 迁移丢数据 | `INSERT IGNORE` 幂等 + 保留旧表不删 |
| APScheduler 与 FastAPI lifecycle 冲突 | 用 `AsyncIOScheduler` + `@app.on_event("startup")` |
| helm upgrade 滚动更新期间短暂 502 | 按 query-go→orchestrator→frontend 顺序，每个等 ready 再下一个 |

## 不在本次范围

- P0-2（已撤销）：LLM 实际已配置
- P1-2（已撤销）：405 是正确 REST 设计
- P2-1/P2-2/P2-3：合法空态
- P2-4：VM scrape 配置
- P2-5：拓扑节点详情参数名
- P3：UX 改进（告警 count 命名、AI 报告置信度等）
