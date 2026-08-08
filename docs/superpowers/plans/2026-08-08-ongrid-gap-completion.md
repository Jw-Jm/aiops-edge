# ongrid 差距完全补齐 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补齐 ongrid 8 块差距：① VM 采集链路，② 告警 DB 化 + 全类型 + incident + Webhook，③ 用户 scope，④ 设备实时指标/WebSSH，⑤ 集群事件，⑥ 拓扑目录，⑦ 日志聚合，⑧ AI incident 工具。自动 RCA 改手动。

**Architecture:** query-api（Go, MySQL 迁移告警/拓扑目录 + VM PromQL 计算 + WebSSH 复用）；ai-orchestrator（Python, 新 incident 工具 + Skill）；deploy（Helm, 新增 vmagent）；前端（React, 告警/用户/设备/集群/拓扑/日志页面增强）。

**Tech Stack:** Go, MySQL, ClickHouse, VictoriaMetrics, VictoriaLogs, React, AntD, echarts, Helm

## Global Constraints

- 复用 `internal/store`（EnsureSchema/GetDB/DAO 模式）与 `internal/api`（auth/RequireRole）
- 告警/拓扑目录表由 EnsureSchema 建表（幂等），数据从 JSON 文件迁移到 MySQL
- VM 采集用 vmagent + static_configs；anomaly/forecast/burn_rate 用 Go 拉取 VM 时序 + 简单统计
- 通知通道仅 Webhook（全局 env + 规则级覆盖），不接 IM
- 自动 RCA 不做，改为手动 incident 操作
- 用户 scope 用 JSON 字段过滤，admin 不受限，不含组织树
- 现有全部测试（Go test + pytest + tsc/build）不回归

---

## Phase 1：VM 采集链路（前置）

### Task 1: vmagent 部署 + scrape_configs

**Files:**
- Create: `deploy/helm/aiops/templates/vmagent/deployment.yaml`
- Create: `deploy/helm/aiops/templates/vmagent/configmap.yaml`
- Modify: `deploy/helm/aiops/values.yaml`
- Test: `helm lint` + `helm template`

**Interfaces:**
- Consumes: VM service (`victoria-metrics:8428`), node-exporter(9100)/ipmi-exporter(8888)/ingest(9090)
- Produces: 采集链路 `up` 指标

- [ ] **Step 1: values.yaml 加 vmagent 配置**

```yaml
vmagent:
  enabled: true
  image: victoriametrics/vmagent:v1.101.0
  scrapeInterval: 15s
  remoteWriteUrl: http://victoria-metrics:8428/api/v1/write
```

- [ ] **Step 2: configmap.yaml 定义 scrape_configs**

```yaml
scrape_configs:
  - job_name: node-exporter
    scrape_interval: 15s
    static_configs:
      - targets: ['node-exporter:9100']
  - job_name: ipmi-exporter
    scrape_interval: 15s
    static_configs:
      - targets: ['ipmi-exporter:8888']
  - job_name: ingest
    scrape_interval: 15s
    static_configs:
      - targets: ['ingest:9090']
```

> 注：targets 用 headless service/ClusterIP DNS；若 DaemonSet pod 非固定 DNS，需验证 node-exporter 服务暴露方式。

- [ ] **Step 3: deployment.yaml 部署 vmagent**

挂 ConfigMap `-promhttpListenAddr=:8429` `-remoteWrite.url=...`。

- [ ] **Step 4: helm lint + template 验证**

Run: `cd deploy/helm/aiops && helm lint . && helm template . | grep -A20 vmagent`
Expected: exit 0, vmagent manifest 生成

- [ ] **Step 5: 部署并验证 up 指标**

Run: `kubectl -n observability rollout status deploy/vmagent` 后
`curl vm:8428/api/v1/query?query=up` 返回 node-exporter/ipmi-exporter/ingest up=1

- [ ] **Step 6: 提交**

```bash
git add deploy/helm/aiops
git commit -m "feat(deploy): vmagent 采集 node-exporter/ipmi-exporter/ingest 到 VM"
```

---

## Phase 2：告警 DB 化 + 全类型 + incident + Webhook

### Task 2: 告警表迁移 MySQL + DAO

**Files:**
- Modify: `ai-apm-query-go/internal/store/mysql.go`（EnsureSchema 加 5 表）
- Create: `ai-apm-query-go/internal/store/alerts.go`（AlertRuleDAO/EventDAO/IncidentDAO/SilenceDAO/TimelineDAO）
- Modify: `ai-apm-query-go/internal/store/alerts_migrate.go`（JSON→MySQL 迁移）
- Test: `ai-apm-query-go/internal/store/alerts_test.go`

**Interfaces:**
- Consumes: `GetDB()`
- Produces: 5 个 DAO

- [ ] **Step 1: EnsureSchema 加 5 表**

`alert_rules`/`alert_events`/`alert_incidents`/`alert_silences`/`alert_events_timeline`（见设计文档 §2.3）。

- [ ] **Step 2: 写失败测试**

```go
// store/alerts_test.go
func TestAlertRuleDAO(t *testing.T) {
	if GetDB() == nil { t.Skip("MySQL not available") }
	d := &AlertRuleDAO{}
	id, _ := d.Create(&AlertRule{Name: "r1", RuleType: "metric_raw", Severity: "warning"})
	if id <= 0 { t.Fatal("create failed") }
}
```

- [ ] **Step 3: 运行确认失败**

Run: `cd ai-apm-query-go && go build ./...`
Expected: FAIL — AlertRuleDAO 未定义

- [ ] **Step 4: 实现 5 个 DAO**（List/Create/Update/Delete/Get，复用 UserDAO 模式）

- [ ] **Step 5: JSON→MySQL 迁移**

启动时若 `/tmp/observability-alerts.json` 存在，迁移 rule/event/silence 后标记归档 `.migrated`。

- [ ] **Step 6: 运行确认通过**

Run: `cd ai-apm-query-go && go build ./... && go test ./internal/store/`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add ai-apm-query-go/internal/store
git commit -m "feat(query-api): 告警 5 表迁移 MySQL + DAO + JSON 迁移"
```

### Task 3: 告警全类型规则评估（Go, VM 计算）

**Files:**
- Create: `ai-apm-query-go/internal/api/alerts_eval.go`（规则评估引擎）
- Modify: `ai-apm-query-go/internal/api/alerts.go`（改为从 MySQL 读规则 + 触发评估）
- Test: `ai-apm-query-go/internal/api/alerts_eval_test.go`

**Interfaces:**
- Consumes: VM PromQL, VictoriaLogs, MySQL DAO
- Produces: 6 种规则类型评估结果

- [ ] **Step 1: 写失败测试（各类型计算）**

```go
func TestEvalAnomaly(t *testing.T) {
	// 构造 baseline 序列 + 当前值，验证偏离告警
}
func TestEvalBurnRate(t *testing.T) { ... }
func TestEvalForecast(t *testing.T) { ... }
```

- [ ] **Step 2: 实现 metric_raw/threshold**

VM PromQL 查询值，比对 condition/threshold；保留 K8s 指标逻辑。

- [ ] **Step 3: 实现 log 类型**

VictoriaLogs LogsQL 匹配计数 > threshold。

- [ ] **Step 4: 实现 anomaly**

拉取窗口（如 7d）序列 → 均值±3σ 基线 → 当前值偏离告警。

- [ ] **Step 5: 实现 forecast**

EMA/线性回归预测未来窗口 → 超阈值告警。

- [ ] **Step 6: 实现 burn_rate**

error 请求率/总量 → SLO 错误预算消耗率超阈值告警。

- [ ] **Step 7: 运行确认通过**

Run: `cd ai-apm-query-go && go build ./... && go test ./internal/api/`
Expected: PASS

- [ ] **Step 8: 提交**

```bash
git add ai-apm-query-go/internal/api/alerts_eval.go
git commit -m "feat(query-api): 告警全类型评估（metric_raw/threshold/log/anomaly/forecast/burn_rate）"
```

### Task 4: Incident 生命周期 + timeline + Webhook 通知

**Files:**
- Modify: `ai-apm-query-go/internal/api/alerts.go`（incident 聚合 + ack/resolve + timeline）
- Modify: `ai-apm-query-go/internal/api/alerts.go`（sendWebhook 扩展规则级 URL + incident payload）
- Test: `ai-apm-query-go/internal/api/incident_test.go`

**Interfaces:**
- Consumes: EventDAO/IncidentDAO/TimelineDAO
- Produces: incident CRUD + timeline + webhook

- [ ] **Step 1: 写失败测试（incident 生命周期）**

- [ ] **Step 2: 事件→incident 聚合**

同 rule+service firing 事件聚合为一个 incident（open）。

- [ ] **Step 3: ack/resolve 联动**

event ack→incident acknowledged；resolve→resolved；写 timeline。

- [ ] **Step 4: timeline API**

`/api/v1/alerts/incidents/{id}/timeline` GET。

- [ ] **Step 5: Webhook 扩展**

`{incident_id, rule_name, service, severity, status, message}`；规则级 webhook_url 覆盖全局 env。

- [ ] **Step 6: 路由注册 + 运行确认**

Run: `cd ai-apm-query-go && go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add ai-apm-query-go/internal/api
git commit -m "feat(query-api): incident 生命周期 + timeline + Webhook 通知"
```

---

## Phase 3：用户 scope（角色+范围）

### Task 5: users 表 scope 字段 + 权限过滤

**Files:**
- Modify: `ai-apm-query-go/internal/store/mysql.go`（users 加 scope 列）
- Modify: `ai-apm-query-go/internal/store/users.go`（User struct + DAO）
- Modify: `ai-apm-query-go/internal/api/auth.go`（JWT scope claim + RequireScope）
- Modify: `ai-apm-query-go/internal/api/catalog.go`/`devices.go`/`clusters.go`/`alerts.go`（scope 过滤）
- Test: `ai-apm-query-go/internal/api/users_scope_test.go`

**Interfaces:**
- Consumes: UserDAO + JWT
- Produces: scope 过滤的读接口

- [ ] **Step 1: 写失败测试（scope 过滤）**

```go
func TestScopeFilter(t *testing.T) {
	// user scope={"services":["a"]} 只返回 service a 的 catalog
}
```

- [ ] **Step 2: users 表加 scope 列 + User struct 加 Scope 字段**

- [ ] **Step 3: JWT 增加 scope claim + RequireScope 中间件**

- [ ] **Step 4: catalog/devices/clusters/alerts 读接口按 scope 过滤**

admin scope 为空 = 全量。

- [ ] **Step 5: `/api/v1/me` 返回 scope + users PUT 支持编辑 scope**

- [ ] **Step 6: 运行确认通过**

Run: `cd ai-apm-query-go && go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add ai-apm-query-go
git commit -m "feat(query-api): 用户 scope 权限过滤（admin 全量 / user 按范围）"
```

---

## Phase 4：设备实时指标 / 集群事件 / 拓扑目录 / 日志聚合

### Task 6: 设备实时指标 + 集群事件

**Files:**
- Modify: `ai-apm-query-go/internal/api/devices.go`（`/devices/{id}/metrics` PromQL）
- Modify: `ai-apm-query-go/internal/api/clusters.go`（`/clusters/{id}/events` kubectl）
- Test: `ai-apm-query-go/internal/api/device_metrics_test.go`、`cluster_events_test.go`

**Interfaces:**
- Consumes: VM PromQL + kubectl
- Produces: 设备实时指标、集群事件

- [ ] **Step 1: 写失败测试**

- [ ] **Step 2: 设备实时指标**

`/devices/{id}/metrics?query=&range=` → 用 device.hostname 匹配 VM instance label → PromQL。

- [ ] **Step 3: 集群事件**

`/clusters/{id}/events` → `kubectl get events --sort-by=.lastTimestamp` 返回异常事件。

- [ ] **Step 4: 路由注册 + 运行确认**

Run: `cd ai-apm-query-go && go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add ai-apm-query-go/internal/api
git commit -m "feat(query-api): 设备实时指标（VM）+ 集群事件"
```

### Task 7: 拓扑目录 CRUD + 日志聚合

**Files:**
- Modify: `ai-apm-query-go/internal/store/mysql.go`（topology_nodes 表）
- Create: `ai-apm-query-go/internal/store/topology.go`（TopologyNodeDAO）
- Create: `ai-apm-query-go/internal/api/topology_nodes.go`（CRUD）
- Modify: `ai-apm-query-go/internal/api/handler.go`（日志聚合 `/api/v1/logs/aggregate`）
- Test: `ai-apm-query-go/internal/api/topology_nodes_test.go`、`logs_aggregate_test.go`

**Interfaces:**
- Consumes: EnsureSchema + VictoriaLogs LogsQL
- Produces: 拓扑目录 CRUD + 日志聚合

- [ ] **Step 1: 写失败测试**

- [ ] **Step 2: topology_nodes 表 + DAO + CRUD 路由**

- [ ] **Step 3: 日志聚合**

`/api/v1/logs/aggregate?service=&field=level&window=1h` → LogsQL 分组聚合。

- [ ] **Step 4: 路由注册 + 运行确认**

Run: `cd ai-apm-query-go && go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add ai-apm-query-go
git commit -m "feat(query-api): 拓扑目录 CRUD + 日志聚合"
```

---

## Phase 5：AI incident 工具

### Task 8: ai-orchestrator incident/notification 工具 + Skill

**Files:**
- Create: `ai-orchestrator/skills/incident_skill.py`（4 工具 + skill.incident）
- Modify: `ai-orchestrator/skills/__init__.py`（注册 skill）
- Modify: `ai-orchestrator/skills/experts.py`（挂到 diagnosis expert）
- Test: `ai-orchestrator/tests/test_incident_tools.py`

**Interfaces:**
- Consumes: query-api `/api/v1/alerts/incidents` + webhook
- Produces: 4 工具

- [ ] **Step 1: 写失败测试**

```python
def test_incident_tools_registered():
    from skill_registry import ToolRegistry
    assert 'incident_query' in ToolRegistry._tools
```

- [ ] **Step 2: 实现 4 工具**

`incident_query`(safe)/`incident_ack`(mutating)/`incident_resolve`(mutating)/`notification_send`(mutating, require_approval)，走 ToolDef Class 元数据。

- [ ] **Step 3: 注册 skill.incident + 挂 diagnosis expert**

- [ ] **Step 4: 运行确认通过**

Run: `cd ai-orchestrator && python -m pytest tests/test_incident_tools.py`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add ai-orchestrator
git commit -m "feat(ai): incident/notification 工具 + skill.incident"
```

---

## Phase 6：前端增强 + 全量验证

### Task 9: 前端告警/用户/设备/集群/拓扑/日志页面增强

**Files:**
- Modify: `observability-frontend/src/api/client.ts`（incident/scope/log-aggregate/topology-nodes API）
- Create: `observability-frontend/src/pages/Alerts/Incidents.tsx`（incident 列表 + timeline + ack/resolve）
- Create: `observability-frontend/src/pages/Alerts/Rules.tsx`（全类型规则配置）
- Modify: `observability-frontend/src/pages/Users/index.tsx`（scope 编辑）
- Modify: `observability-frontend/src/pages/Devices/index.tsx`（实时指标）
- Modify: `observability-frontend/src/pages/Clusters/index.tsx`（事件）
- Modify: `observability-frontend/src/pages/Topology/index.tsx`（目录叠加）
- Modify: `observability-frontend/src/pages/Logs/index.tsx`（聚合）
- Test: `tsc --noEmit` + `npm run build`

**Interfaces:**
- Consumes: 后端 API
- Produces: 页面增强

- [ ] **Step 1: client.ts 加 API**

- [ ] **Step 2: Incidents 页（列表 + timeline + ack/resolve 手动处置）**

- [ ] **Step 3: Rules 页（全类型规则配置，含 params）**

- [ ] **Step 4: Users/Devices/Clusters/Topology/Logs 页面增强**

- [ ] **Step 5: tsc + build 验证**

Run: `cd observability-frontend && node_modules/.bin/tsc --noEmit && npm run build`
Expected: exit 0

- [ ] **Step 6: 提交**

```bash
git add observability-frontend/src
git commit -m "feat(web): 告警 incident/规则 + 用户 scope + 设备指标 + 集群事件 + 拓扑目录 + 日志聚合"
```

### Task 10: 全量部署 + 回归冒烟

**Files:**
- Modify: `deploy/helm/aiops/values.yaml`（如需要）
- Test: 全量冒烟

- [ ] **Step 1: 重建部署全部组件**

Run: 构建镜像 + `helm upgrade --install aiops ./deploy/helm/aiops -n observability`

- [ ] **Step 2: 全量回归**

Run: 各仓库测试（go test + pytest + tsc/build）+ 冒烟：
- VM `up` 含 node-exporter/ipmi-exporter/ingest
- 告警 6 类型规则评估 + incident 生命周期 + webhook
- 用户 scope 过滤生效
- 设备实时指标 / 集群事件 / 拓扑目录 / 日志聚合
- AI incident 工具调用

- [ ] **Step 3: 提交**

```bash
git add deploy/helm/aiops
git commit -m "feat(deploy): 补齐 ongrid 差距全量部署"
```

---

## Self-Review

**1. Spec coverage（对照设计文档 2026-08-08-ongrid-gap-completion-design.md）：**
- ✅ §1 VM 采集 → Task 1
- ✅ §2 告警 DB 化+全类型+incident+Webhook → Task 2/3/4
- ✅ §3 用户 scope → Task 5
- ✅ §4 设备实时指标 / §5 集群事件 → Task 6
- ✅ §6 拓扑目录 / §7 日志聚合 → Task 7
- ✅ §8 AI incident 工具 → Task 8
- ✅ §9 部署 + 前端 → Task 9/10

**2. Placeholder scan：** 无 TBD/TODO（除实现时需验证的 DaemonSet DNS 目标）。

**3. Type consistency：**
- `AlertRuleDAO/EventDAO/IncidentDAO/SilenceDAO/TimelineDAO` — Task 2 定义，Task 3/4 使用
- `RequireScope` — Task 5 定义，catalog/devices/clusters/alerts 使用
- `incident_query/incident_ack/incident_resolve/notification_send` — Task 8 定义，skill.incident 使用

**4. 依赖顺序：** Phase1(VM) → Phase2(告警) 依赖 VM 数据；Phase4 设备依赖 VM；Phase5 AI 依赖 Phase2 incident API；前端依赖后端全量。

**5. 手动 RCA：** 无自动 RCA 工作流；incident ack/resolve 为用户手动操作（前端 + AI 工具均手动触发）。
