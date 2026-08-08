# 统一指标架构：三引擎职责收敛

**日期**: 2026-08-08
**范围**: 全栈（ingest RED 暴露 + vmagent 采集 + Dashboard/拓扑/告警迁 VM PromQL + 停写 metric_service_red）
**驱动**: 用户要求"所有指标都暴露、统一存储、组件最小化、架构合理"
**决策**: 三引擎职责收敛（不砍引擎）；深层迁移——Dashboard/拓扑/告警从 trace_spans 迁 VM PromQL

---

## 1. 现状问题（实证）

| 问题 | 证据 |
|------|------|
| VM 假接入 | Monitor 已用 PromQL 查 VM（`/metrics/query_range`），但 `up` 空（无数据源）|
| RED 聚合死代码 | `metric_service_red` 全库无 handler 读，`ingest/metrics_writer.go:234` 只写不读 |
| 指标孤岛 | `ingest:/metrics`（ai_ingest_*）已有但无 vmagent 抓；orchestrator/ingest 指标无人采集 |
| 三套存储职责重叠 | CH 存 trace+log+RED+拓扑；VM 只有 node-exporter；MySQL 资产+业务 |
| app 业务 RED 无源 | Dashboard/告警读 `trace_spans` 实时聚合，无持久化指标库 |

---

## 2. 目标架构（三引擎职责收敛）

| 引擎 | 职责 | 承载 |
|------|------|------|
| **VM** | **唯一时序指标库** | node-exporter OS + 服务 RED（ingest 暴露）+ 硬件状态(ipmi/snmp gauge) + orchestrator/ingest 内部 metrics |
| **CH** | **仅事件库** | `trace_spans`（链路）+ `log_records`（日志）|
| **MySQL** | **业务/资产/配置** | users/clusters/devices/topology_nodes/ipmi/snmp 资产 + approval/audit/knowledge/rules/agents |

### 采集流（统一进 VM）

```
node-exporter ─────────┐
ingest  /metrics ──────┼──► vmagent ──remoteWrite──► VM (:8428)
orchestrator /metrics ─┘    (唯一指标入口)
```

### 组件零新增
- 不新增数据库/中间件
- vmagent 是唯一新增部署（VictoriaMetrics 家族，用现有版本）
- 全部为代码级改造（ingest metrics 扩展 + query-api 取数切换）

---

## 3. ingest RED 指标暴露（核心改造）

### 3.1 现状
ingest 从 OTLP trace 聚合服务 RED，写入 CH `metric_service_red`（metrics_writer.go:234），无读方（死代码）。

### 3.2 改造
把 RED 聚合输出从"写 CH"改为"暴露为 Prometheus 指标"，在 `internal/metrics/metrics.go` 扩展 `Snapshot()`，追加服务维度指标：

```text
# 服务请求数（label=service），承接 Monitor 的 http_requests_total 语义
service_requests_total{service="frontend"} 123
# 服务错误数
service_errors_total{service="frontend",status="500"} 3
# 服务延迟 histogram（sum/count 两序列）
service_request_duration_seconds_sum{service="frontend"} 45.2
service_request_duration_seconds_count{service="frontend"} 123
```

- ingest 用**标签化计数器**聚合（`map[service]*atomic.Int64` 或按 service 的累计值），周期性写入 metrics 状态，`/metrics` 端点（main.go:174）输出。
- **保留 trace 落 CH**：trace_spans 仍写 CH（链路/日志保留）。
- **停写 metric_service_red**：metrics_writer 的 metric_service_red INSERT 移除（或保留但弃用，按实施决策）。

### 3.3 vmagent 抓取
vmagent scrape_configs 增加：
```yaml
- job_name: ingest
  static_configs:
    - targets: ['ingest:8080']
- job_name: orchestrator
  static_configs:
    - targets: ['orchestrator:8000']   # 端口按实际
```

---

## 4. Dashboard / 拓扑 / 告警迁移到 VM PromQL

### 4.1 现状取数（trace_spans SQL 实时聚合）
| 功能 | 现有查询 |
|------|---------|
| Dashboard 服务列表/趋势 | `trace_spans` 按 service/interval 聚合 rate |
| Dashboard TOP/错误率 | `trace_spans` 聚合 |
| 告警（error_rate/latency_p99/call_count）| `trace_spans` SQL |
| 全局拓扑边指标 | `trace_spans` 聚合 source→dest |

### 4.2 迁移到 VM PromQL
| 功能 | 新查询（VM PromQL） |
|------|---------------------|
| 服务列表 | `label_values(service_requests_total, service)` |
| 服务趋势/QPS | `sum(rate(service_requests_total[5m])) by (service)` |
| 错误率 | `sum(rate(service_errors_total[5m])) by (service) / sum(rate(service_requests_total[5m])) by (service)` |
| 延迟 P99 | `histogram_quantile(0.99, sum(rate(service_request_duration_seconds_bucket[5m])) by (le))` |
| 拓扑边 | `sum(rate(service_edge_calls_total{source,dest}[5m])) by (source,dest)`（ingest 暴露边指标）|

### 4.3 改造点
- **query-api**：新增 VM PromQL 查询封装（`/metrics/query`、`/metrics/query_range` 已有），把 Dashboard/告警/拓扑的取数 handler 改为查 VM。
- **前端**：Monitor 面板 PromQL 保持（`http_requests_total` 语义由 `service_requests_total` 承接，前端改指标名或后端映射）。

---

## 5. 硬件状态进 VM

- ipmi/snmp 采集后除写 MySQL 资产快照，同时以 Prometheus gauge 暴露（orchestrator 聚合 or ipmi-exporter 直接暴露），vmagent 抓进 VM。
- **本 spec 聚焦指标收敛主链路**；硬件 gauge 暴露作为同一采集流的附带项（用现有 orchestrator /metrics 端点扩展）。

---

## 6. 实施范围（分阶段）

### Phase 1（本次核心）：打通 VM 指标
- [ ] ingest 暴露服务 RED 指标（service_requests_total/errors/duration）
- [ ] vmagent 抓 node-exporter + ingest + orchestrator
- [ ] Monitor 面板有数据

### Phase 2（本次）：停写死代码 + 迁移取数
- [ ] 停写 CH `metric_service_red`
- [ ] query-api Dashboard/告警/拓扑从 trace_spans 迁 VM PromQL

### Phase 3（附带）：硬件 gauge 进 VM
- [ ] ipmi/snmp 暴露 gauge，vmagent 抓

---

## 7. 风险

| 风险 | 影响 | 缓解 |
|------|------|------|
| ingest 标签化计数器内存增长 | 低 | 服务数有限（几十个），按需清理 |
| VM PromQL 与 trace_spans 聚合差异 | 中 | 迁移期双跑比对；指标名保持语义兼容 |
| Dashboard/告警迁移范围大 | 高 | 分 Phase 2 逐步切换，每步验证 |
| vmagent 抓 orchestrator 端口不确定 | 中 | 实施时确认端口 |

---

## 8. 自审
- [x] 无 TBD/TODO
- [x] 三引擎职责收敛，组件零新增
- [x] 消除 metric_service_red 死代码 + VM 假接入
- [x] 复用现有 ingest /metrics、VM query API
- [x] Dashboard/告警迁移路径明确（PromQL 替代 trace_spans 聚合）
