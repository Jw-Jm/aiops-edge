# P1d：Dashboard 全新替换 + NL→ClickHouse SQL

**日期**: 2026-08-08
**范围**: query-api（Go，Dashboard 图表接口）+ ai-orchestrator（Python，NL→SQL）+ observability-frontend（React，Dashboard 页 + NL2SQL 页）
**驱动**: 实施计划 P1d — "Dashboard + 告警自动调查 + NL→ClickHouse SQL"

## 1. 范围与边界（已确认）

| 子项 | 决策 | 说明 |
|------|------|------|
| Dashboard | **全新独立页替换 Overview** | KPI 卡片 + echarts 图表 + 告警环形 |
| 告警自动调查 | **不做** | 保持手动触发 RCA（用户确认）|
| NL→ClickHouse SQL | **交付** | LLM 生成 → 人工确认 → 执行（安全模式）|
| NL→SQL 执行位置 | ai-orchestrator 统一负责 | 生成 + 执行闭环（复用 `_llm` + `_ch_query`）|

**不做**：告警自动调查（auto RCA）、VictoriaMetrics 新查询。

---

## 2. Dashboard 设计

### 2.1 后端：扩展 `/api/v1/dashboard/stats`（query-api, Go）

现有 `DashboardStats` 返回 `{services, edges, total_calls, total_errors, error_rate, top_services}`。扩展为：

```go
type DashboardStats struct {
    Services     int         `json:"services"`
    Edges        int64       `json:"edges"`
    TotalCalls   int64       `json:"total_calls"`
    TotalErrors  int64       `json:"total_errors"`
    ErrorRate    float64     `json:"error_rate"`
    LatencyP95   float64     `json:"latency_p95"`      // 新增
    TopServices  []StatsItem `json:"top_services"`
    Trend        []TrendPoint `json:"trend"`            // 新增：近N小时调用/错误趋势
    AlertStats   AlertStats  `json:"alerts"`            // 新增：告警统计（环形图数据）
    TopErrors    []ErrorItem `json:"top_errors"`        // 新增：TOP 错误服务分布
}
```

**新增查询**（复用 `queryClickHouse` + `parseRows`）：

| 数据 | SQL | 说明 |
|------|-----|------|
| `latency_p95` | `quantile(0.95)(duration_ns)/1e6 FROM trace_spans WHERE ...` | P95 延迟 |
| `trend` | `toStartOfHour(start_time) t, count(), countIf(is_error=1) FROM trace_spans GROUP BY t ORDER BY t LIMIT 24` | 近 24h 趋势 |
| `alerts` | 从告警源统计（见 §2.2） | 环形图 |
| `top_errors` | `service_name, countIf(is_error=1) FROM trace_spans GROUP BY service_name ORDER BY errors DESC LIMIT 10` | 错误分布 |

### 2.2 告警统计来源（alerts 环形图）

**已确认**：告警事件 `alertEvents` 为**内存持久化**（`alerts.go`，存 `/tmp/observability-alert-events.json`，`AlertEvent{Status,Severity,Service,RuleName,ID,LastTimestamp,Count}`）。

因此 `AlertStats` 聚合**直接读内存 `alertEvents`**（加 `alertEventsMu.RLock()`），**不走 ClickHouse**：

```go
type AlertStats struct {
    Total     int            `json:"total"`
    Critical  int            `json:"critical"`
    Warning   int            `json:"warning"`
    Info      int            `json:"info"`
    ByService []AlertBySvc   `json:"by_service"`
}
```

聚合逻辑：遍历 `alertEvents`，按 `Severity` 计数（firing 状态），按 `Service` 分组。复用 `alerts.go` 已有的锁机制，新增 `biz.AggAlertStats(events []AlertEvent)`。

### 2.3 前端：重写 Overview 为 Dashboard

用 **echarts-for-react**（已装 3.0.2 + echarts 5.5.0，参考 ServiceDetail `theme="dark"`）：

- **顶部 KPI 卡片**：服务数 / 调用量 / 错误率 / P95 延迟（复用现有 4 卡 + 新增 P95）
- **主图**：
  - 调用量+错误趋势折线图（`trend`）
  - TOP 服务错误分布柱状图（`top_errors`）
  - 告警环形图（`alerts`，critical/warning 圆环）
  - TOP 服务调用量条形图（`top_services`）
- **时间范围切换**：近 1h / 6h / 24h（对应 trend SQL 的 LIMIT 窗口）
- 保留欢迎横幅/功能入口（或精简）

---

## 3. NL→ClickHouse SQL 设计

### 3.1 交互流程（生成-确认-执行，参考审批模式）

```
用户输入自然语言 ──► POST /api/v1/ai/nl2sql/translate
                        │
                        ▼
              LLM 生成 ClickHouse SQL + 意图说明
              （_llm 复用，system prompt 约束）
                        │
                        ▼
      返回 {sql, explanation, id}（待确认状态）
                        │  前端展示 SQL + 确认按钮
                        ▼
        POST /api/v1/ai/nl2sql/{id}/execute  ──► 执行 SQL（_ch_query）
                        │
                        ▼
              返回查询结果（表格数据）+ 落审计
```

**安全护栏**：
- SQL 必须 `SELECT` 开头（`^SELECT\s+`），禁止 `INSERT/UPDATE/DELETE/DROP/ALTER/;`
- 表名白名单：`observability.trace_spans` / `service_topology` / `log_records` / `inspection_reports`
- 强制 `LIMIT`（无则追加 `LIMIT 100`）
- `WHERE` 里可自动补 `tenant_id` 隔离（可选）

### 3.2 后端（ai-orchestrator/main.py）

**新增状态**：`_nl2sql_store`（内存 + 可复用 db 降级模式，借鉴 P1b 的 Store 模式）。每个翻译生成 `{id, sql, explanation, status: pending/executed/expired, created_at}`。

| 端点 | 方法 | 逻辑 |
|------|------|------|
| `/api/v1/ai/nl2sql/translate` | POST | 调 `_llm(cfg, NL2SQL_SYSTEM, question, role="SQL专家")` 生成 SQL → 安全校验 → 存 store → 返回 `{id, sql, explanation}` |
| `/api/v1/ai/nl2sql/{id}/execute` | POST | 校验 pending → `_ch_query(sql)` 执行 → 解析 TabSeparated → 返回 `{columns, rows}` → 落 `_audit_log` → 标 executed |
| `/api/v1/ai/nl2sql/{id}` | GET | 取翻译详情（供前端刷新）|

**TabSeparated 解析**（复用/新增 `_ch_query` 增强）：查询返回 JSONEachRow 更易解析，但 `_ch_query` 固定 TabSeparated。**改进**：`_ch_query` 加可选 `fmt` 参数，执行查询时用 `default_format=JSONEachRow`，直接解析 JSON 数组。

### 3.3 前端：NL2SQL 页面

- 复用 Logs 页的表格展示模式（AntD Table）
- 输入框（自然语言）→ "翻译" → 展示生成 SQL（只读高亮）+ 说明 → "执行" 按钮 → 结果表格
- 保留 "重置/新查询"
- 菜单挂"智能运维"分组

---

## 4. 配置与部署

- ai-orchestrator 无新增依赖（复用 pymysql/DBUtils + crewai LLM）
- query-api 无新增依赖
- Helm 无需改动（CLICKHOUSE_HOST/PORT 已配置）
- 前端无新增依赖（echarts 已装）

---

## 5. 测试

- **query-api**：Go 单测（沿用现有 handler 测试模式，mock ClickHouse）
- **ai-orchestrator**：`tests/` 新增 `test_nl2sql.py`（安全校验：拒绝 INSERT、强制 LIMIT、表白名单；`test_dashboard.py` 若需要）
- **前端**：`tsc --noEmit` + `npm run build`
- **冒烟**：NL2SQL translate→execute 全链路（LLM_MOCK 模式验证 SQL 生成 + 执行）

---

## 6. 风险

| 风险 | 概率 | 影响 | 缓解 |
|------|------|------|------|
| LLM 生成非法 SQL | 中 | 高 | 白名单校验 + SELECT 限制 + 人工确认 |
| 告警表名不确定 | 中 | 中 | 探索确认后再写 alert 聚合 SQL |
| ClickHouse 查询慢 | 低 | 中 | 强制 LIMIT + 时间窗口 |
| Overview 替换破坏现有入口 | 中 | 中 | 保留功能入口，重写渲染层 |

---

## 7. 自审

- [x] 无 TBD/TODO
- [x] 范围聚焦：Dashboard 全替换 + NL→SQL（排除 auto RCA）
- [x] 架构一致：复用现有 `_llm`/`_ch_query`/审批模式，无重复造轮子
- [x] 安全护栏明确：SQL 白名单 + SELECT + LIMIT + 人工确认
- [x] 无歧义：表/端点/交互流程均明确
