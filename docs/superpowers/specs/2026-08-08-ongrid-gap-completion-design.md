# ongrid 差距完全补齐设计

**日期**: 2026-08-08
**范围**: 指标监控（VM 采集）、告警系统（DB 化 + 全类型 + incident）、用户 RBAC（scope）、设备（实时指标/WebSSH）、集群（事件）、拓扑、日志、AI 工具，共 8 块
**技术栈**: query-api（Go, MySQL + ClickHouse + VictoriaMetrics）+ ai-orchestrator（Python）+ observability-frontend（React）+ deploy（Helm）

## 0. 决策汇总（已与用户确认）

| 块 | 决策 |
|----|------|
| 指标监控 | **启用 VM 采集**：vmagent 抓取 node-exporter/ipmi-exporter/ingest `/metrics` 到 VictoriaMetrics，PromQL + 告警规则有数据 |
| 告警系统 | **规则迁移 MySQL + 全类型**：metric_raw/threshold/log 直接做，anomaly/forecast/burn_rate 基于 VM 时序；incident/event 分层 + Silence 静默；通知通道仅 **Webhook**（不接 IM）|
| 根因调查 | **改为手动**：不做自动 RCA 工作流 |
| 用户管理 | **角色 + 范围(scope) 两级**：admin/user 角色 + scope 字段（按服务/集群/设备授权）+ 权限过滤，不含组织树 |
| 拓扑/设备/集群 | 基础 CRUD 已有，补齐实时指标/WebSSH/集群事件 |
| 日志 | VictoriaLogs + shipper 已具备，补 ongrid 查询能力（聚合/上下文）|
| AI | 20 工具/7 Skill/4 Expert 已具备，补 ongrid 特有工具（incident/通知）|

---

## 1. 块 A：指标监控（VM 采集链路）— 最大前置

### 1.1 现状
VM 单机版已部署（v1.101.0, port 8428, retention 30d），但**无 vmagent/scrape 配置**，node-exporter/ipmi-exporter/ingest 的 `/metrics` 均未被抓取。query-api 已有 `/api/v1/metrics/query_range` PromQL 代理到 VM。

### 1.2 目标
补齐采集链路，使 PromQL 查询、告警规则（anomaly/forecast/burn_rate）、设备实时指标都有数据。

### 1.3 设计

**新增 vmagent deployment**（`deploy/helm/aiops/templates/vmagent/deployment.yaml`）+ scrape_configs：

```yaml
scrape_configs:
  - job_name: node-exporter
    static_configs:
      - targets: ['node-exporter:9100']   # DaemonSet 已暴露 hostPort, cluster 内 DNS
  - job_name: ipmi-exporter
    static_configs:
      - targets: ['ipmi-exporter:8888']   # DaemonSet, 本地 /dev/ipmi0
  - job_name: ingest
    static_configs:
      - targets: ['ingest:9090']          # ingest /metrics (ai_ingest_*)
```

- vmagent `-remoteWrite.url=http://victoria-metrics:8428/api/v1/write`
- scrape 间隔 15s
- 挂 ConfigMap 承载 `scrape_configs.yaml`

**values.yaml 追加**：`vmagent.enabled: true`、`scrapeInterval: 15s`

### 1.4 验证
- `curl vm:8428/api/v1/query?query=up` 返回 node-exporter/ipmi-exporter/ingest 的 up=1
- `/api/v1/metrics/query_range?query=node_load1` 有数据

---

## 2. 块 B：告警系统（DB 化 + 全类型 + incident + Webhook）

### 2.1 现状
rule(threshold/mutation)/event(ack/resolve)/silence 已有，但**纯 JSON 文件持久化**，无 incident 实体，通知仅单个 webhook 环境变量。

### 2.2 目标
规则/事件/静默迁移 MySQL；规则多类型（metric_raw/threshold/log 直接做 + anomaly/forecast/burn_rate 基于 VM 时序）；新增 incident/event 分层 + timeline；Silence 静默；通知通道仅 Webhook。

### 2.3 数据库表（query-api 迁移，复用 store.EnsureSchema）

#### `alert_rules` — 规则
```sql
CREATE TABLE IF NOT EXISTS alert_rules (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  name VARCHAR(128) NOT NULL,
  service VARCHAR(128) DEFAULT '',
  rule_type ENUM('metric_raw','threshold','anomaly','forecast','burn_rate','log','mutation') NOT NULL,
  metric VARCHAR(128) DEFAULT '',          -- metric_raw/threshold/anomaly/forecast/burn_rate 用
  condition VARCHAR(32) DEFAULT '',        -- gt/gte/lt/lte/eq
  threshold DOUBLE DEFAULT 0,
  duration_sec INT DEFAULT 300,            -- 持续时长
  severity ENUM('critical','warning','info') DEFAULT 'warning',
  params TEXT,                             -- JSON：anomaly 基线窗口 / forecast 窗口 / burn_rate 预算 / log 查询
  enabled TINYINT DEFAULT 1,
  webhook_url VARCHAR(512) DEFAULT '',     -- 规则级 webhook（覆盖全局）
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

#### `alert_events` — 事件（每次评估命中的瞬时事件）
```sql
CREATE TABLE IF NOT EXISTS alert_events (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  rule_id BIGINT,
  rule_name VARCHAR(128),
  incident_id BIGINT DEFAULT 0,            -- 关联 incident
  service VARCHAR(128) DEFAULT '',
  severity ENUM('critical','warning','info') DEFAULT 'warning',
  message VARCHAR(512) DEFAULT '',
  metric_value DOUBLE DEFAULT 0,
  threshold DOUBLE DEFAULT 0,
  status ENUM('firing','acknowledged','resolved') DEFAULT 'firing',
  first_at DATETIME,
  last_at DATETIME,
  count INT DEFAULT 1,                      -- 降噪聚合计数
  acked_at DATETIME,
  acked_by VARCHAR(64) DEFAULT '',
  resolved_at DATETIME,
  resolved_by VARCHAR(64) DEFAULT '',
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

#### `alert_incidents` — 事件（生命周期聚合，分层）
```sql
CREATE TABLE IF NOT EXISTS alert_incidents (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  title VARCHAR(255) NOT NULL,
  rule_id BIGINT,
  service VARCHAR(128) DEFAULT '',
  severity ENUM('critical','warning','info') DEFAULT 'warning',
  status ENUM('open','acknowledged','resolved') DEFAULT 'open',
  started_at DATETIME,
  resolved_at DATETIME,
  summary TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

#### `alert_silences` — 静默
```sql
CREATE TABLE IF NOT EXISTS alert_silences (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  rule_id BIGINT DEFAULT 0,                -- 0 = 匹配全部规则
  service VARCHAR(128) DEFAULT '',
  matchers VARCHAR(255) DEFAULT '',
  comment VARCHAR(255) DEFAULT '',
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  expires_at DATETIME
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

#### `alert_events_timeline` — 事件时间线（incident 内操作留痕）
```sql
CREATE TABLE IF NOT EXISTS alert_events_timeline (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  incident_id BIGINT NOT NULL,
  event_id BIGINT,
  action ENUM('created','firing','acknowledged','resolved','commented','notified') DEFAULT 'created',
  actor VARCHAR(64) DEFAULT 'system',
  detail VARCHAR(512) DEFAULT '',
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 2.4 规则类型评估逻辑

| 类型 | 数据源 | 逻辑 |
|------|--------|------|
| `metric_raw` | VM PromQL | 查询 metric 原始值，比对 condition/threshold |
| `threshold` | VM PromQL（已有 ClickHouse 聚合保留）| 聚合值（error_rate/latency_p99/call_count）+ K8s 指标比对 |
| `log` | VictoriaLogs LogsQL | 按 params 查询日志，匹配数 > threshold 告警 |
| `anomaly` | VM 时序 | 滑动基线窗口（如过去 7d 均值±3σ），当前值偏离基线告警 |
| `forecast` | VM 时序 | 简单线性回归/EMA 预测未来窗口值，超阈值告警 |
| `burn_rate` | VM 时序 | error 请求率 / 总量，SLO 错误预算消耗率（如 1h/5m burn rate）超阈值告警 |

> 前三种（metric_raw/threshold/log）直接实现；anomaly/forecast/burn_rate 用 Go 实现 VM 时序拉取 + 简单统计计算。

### 2.5 Incident 生命周期
- 同一 rule+service 的 firing 事件聚合为一个 incident（`open`）
- 事件 ack → incident `acknowledged`；事件 resolve → incident `resolved`（记录 resolved_at）
- 所有操作写 `alert_events_timeline`，前端 incident 详情展示时间线

### 2.6 通知（仅 Webhook）
- 通知通道：**全局 Webhook URL（env `ALERT_WEBHOOK_URL`）+ 规则级 webhook_url 覆盖**
- firing 时发送 Webhook；resolve 时也发送（可选）
- Webhook payload: `{incident_id, rule_name, service, severity, status, message, timestamp}`
- 复用现有 `sendWebhook`，扩展为支持规则级 URL

### 2.7 数据迁移
- 启动时若旧 JSON 文件存在（`/tmp/observability-alerts.json`），迁移 rule/event/silence 到 MySQL 后标记归档

---

## 3. 块 C：用户管理（角色 + scope）

### 3.1 现状
users 表仅 admin/user 两级，无 scope。JWT HS256。

### 3.2 设计
- **users 表新增 `scope` 字段**（`VARCHAR(512) DEFAULT ''`）：格式 `{"services":["a","b"],"clusters":["c1"],"devices":["d1"]}`，admin 角色 scope 为空 = 全量
- **权限过滤**：user 角色的查询接口（catalog/devices/clusters/alerts）按 scope 过滤数据；admin 不受限
- **JWT 增加 scope claim**：登录时写入，前端据 scope 控制操作按钮
- **接口**：
  - `/api/v1/users/{id}` PUT 支持编辑 scope
  - `/api/v1/me` 返回 scope
- 不含组织树/细粒度资源级 RBAC

---

## 4. 块 D：设备管理增强（实时指标 + WebSSH）

### 4.1 现状
devices 表 CRUD 已有，无实时指标/远程操作。

### 4.2 设计
- **设备实时指标**：`/api/v1/devices/{id}/metrics?query=node_load1&range=1h` → 用 device.hostname 关联 VM 抓取的 node-exporter 指标（PromQL label `instance` 匹配 hostname）
- **WebSSH 复用**：现有 WebShell（xterm+WebSocket+白名单）已具备，devices 页集成入口（跳转 WebShell 并预填 host）

---

## 5. 块 E：集群管理增强（事件）

### 5.1 现状
clusters 表 CRUD + 节点列表 + K8s 自动发现已有。

### 5.2 设计
- **集群事件**：`/api/v1/clusters/{id}/events` → 从 K8s `events` 资源拉取（kubectl get events），展示异常事件
- 其余（健康/升级）本轮不做（手动运维），保留现状

---

## 6. 块 F：拓扑增强（目录 CRUD）

### 6.1 现状
真实 trace 聚合拓扑已有，无 topology_node/node_type/relation_type 目录 CRUD。

### 6.2 设计
- **拓扑目录表**（query-api MySQL）：
```sql
CREATE TABLE IF NOT EXISTS topology_nodes (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  name VARCHAR(128) NOT NULL,
  node_type VARCHAR(64) DEFAULT 'service',  -- service/device/cluster
  relation_type VARCHAR(64) DEFAULT '',      -- depends/calls/runs_on
  target VARCHAR(128) DEFAULT '',            -- 关联对象
  metadata TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```
- **接口**：`/api/v1/topology/nodes` CRUD，前端拓扑页可叠加目录节点/边

---

## 7. 块 G：日志增强（聚合/上下文）

### 7.1 现状
VictoriaLogs + query-api 内嵌 shipper + 查询代理已具备。

### 7.2 设计（轻量补齐）
- **日志聚合查询**：`/api/v1/logs/aggregate?service=&field=level&window=1h` → LogsQL 分组聚合（ERROR 数按 level/时间桶）
- **日志上下文**：`/api/v1/logs/context?trace_id=` 已具备，补 `?log_id=` 返回前后 N 条
- 其余 ongrid 日志能力（标注/收藏）本轮不做

---

## 8. 块 H：AI 工具补齐（incident/通知）

### 8.1 现状
20 工具/7 Skill/4 Expert 已具备。

### 8.2 设计
ai-orchestrator 新增工具（走 ToolDef Class 元数据 + require_approval 门控）：
- **`incident_query`**（safe）：查询 incident 列表/详情 + timeline
- **`incident_ack`**（mutating）：ack incident（用户手动确认）
- **`incident_resolve`**（mutating）：resolve incident
- **`notification_send`**（mutating, require_approval）：通过 Webhook 发送告警/通知

挂到新 Skill `skill.incident`（告警处置增强，替代现有 alert_ops 部分）+ 注册到 Expert `diagnosis`。

---

## 9. 部署变更（deploy/helm）

| 变更 | 文件 |
|------|------|
| 新增 vmagent | `templates/vmagent/deployment.yaml` + `scrape_configs` ConfigMap |
| 告警迁移配置 | query-api env `ALERT_DB_MODE=mysql`（默认）|
| values | `vmagent.enabled/scrapeInterval` |

---

## 10. 测试策略

- **Go（query-api）**：
  - `alerts_mysql_test.go`：rule/event/incident/silence CRUD + timeline + 各类型评估（mock VM/Logs）
  - `alerts_anomaly_test.go`：anomaly/forecast/burn_rate 计算逻辑单测
  - `users_scope_test.go`：scope 过滤
  - `topology_nodes_test.go`：目录 CRUD
  - `logs_aggregate_test.go`：聚合查询
- **Python（ai-orchestrator）**：`test_incident_tools.py`（新工具注册 + 调用）
- **前端**：`tsc --noEmit` + `npm run build`
- **冒烟**：VM 采集 up=1、告警 rule 全类型评估、incident 生命周期、scope 过滤、拓扑目录、日志聚合

---

## 11. 实施顺序（分块依赖）

1. **块 A（VM 采集）** — 前置，所有时序数据源
2. **块 B（告警 DB 化 + 全类型 + incident）** — 依赖 A 的 VM 数据
3. **块 C（用户 scope）** — 独立
4. **块 D/E（设备实时指标 / 集群事件）** — 依赖 A
5. **块 F/G（拓扑目录 / 日志聚合）** — 独立
6. **块 H（AI incident 工具）** — 依赖 B 的 incident API

---

## 12. 风险

| 风险 | 概率 | 影响 | 缓解 |
|------|------|------|------|
| VM 抓取目标不可达（hostPort/headless DNS）| 中 | 高 | static_configs 用 ClusterIP/headless，验证 target 可解析 |
| anomaly/forecast 计算误报 | 中 | 中 | 基线窗口可配，默认宽松阈值，参数可调 |
| 告警 JSON→MySQL 迁移丢数据 | 低 | 中 | 迁移前归档，迁移失败保留 JSON 可回退 |
| scope 过滤破坏现有 admin 数据 | 低 | 高 | admin 不受限，user 默认空 scope = 无数据可见（需配置）|
| 多仓库协同回归 | 中 | 高 | 每块 TDD + 独立测试 + 全量冒烟 |

---

## 13. 自审

- [x] 无 TBD/TODO
- [x] 8 块全覆盖，决策与用户确认一致（VM 采集 / 告警全类型+DB+incident+Webhook / 手动 RCA / 角色+scope）
- [x] 统一数据归属：告警/拓扑目录迁 MySQL（复用 store.EnsureSchema），时序走 VM
- [x] 告警 incident/event 分层 + timeline + Silence 静默明确
- [x] 通知通道仅 Webhook（与 IM 跳过一致）
- [x] 复用现有 vmagent 目标、WebSSH、skill_registry，无重复造轮子
- [x] 实施顺序依赖清晰，块 A 前置
