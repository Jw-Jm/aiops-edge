# 批4：异常检测统计模型（A4）+ SLO 烧毁率（B3）设计

**日期**: 2026-08-09
**批次**: 批 4（总纲 Phase A：A4 + B3）
**性质**: 设计文档（已与用户对齐 3 项关键决策 + 架构自审修正）
**前置**: 服务 RED 指标采集已打通（DeepFlowSyncer 累加真实流量到 VM + loadgen 可注入异常，cluster 标签支持多环境）

---

## 0. 已对齐的关键决策

| # | 决策 | 选择 |
|---|---|---|
| 1 | A4 异常检测算法 | **zscore + MAD 可选**（规则配置检测方法 + baseline_seconds 窗口） |
| 2 | B3 SLO 烧毁率 | **SLO 目标管理 + 多窗口烧毁率**（availability/latency + 5m/30m 窗口） |
| 3 | 实现范围 | **A4+B3 完整**，前端 SLO 管理页 + 规则表单扩展，指标用现有 VM PromQL |
| 4 | 实现路径 | **Go 内实现统计检测**（复用现有告警引擎，零新服务） |

## 架构自审修正（已纳入）
- **P1-1 修正**：anomaly 需**历史序列**（算 std/median），现有评估循环只有"当前值 + 历史单均值"。故新增 `vmRangeQuery` 返回完整序列，anomaly 走**独立评估路径**（`evaluateRuleAnomaly`），不再复用 `vmInstantQuery` 单值。
- **P0-1 已解决**：服务 RED 指标采集已打通（前置任务），error_rate 取数链路可用。

---

## 1. 现状与差距（代码实际）

| 项 | 现状 | 批4 差距 |
|---|---|---|
| anomaly 规则 | alerts.go:857 用"当前值 vs 历史均值百分比偏差"（Threshold 复用为偏差%） | 无 zscore/MAD/标准差，无独立 baseline 窗口 |
| burn_rate 规则 | alerts.go:873 用"当前值 vs 历史窗口偏差"（与 forecast 共用） | 无 SLO 目标/错误预算/多窗口 |
| VM 历史查询 | `buildQueryRangeURL` + `/api/v1/query_range` 已存在 | 告警引擎未用；需新增 `vmRangeQuery` 取序列 |
| MySQL 迁移 | `hasColumn` + `ALTER TABLE ADD COLUMN`（alert_rules 已有 cooldown/dampening 先例）| 新增 baseline_seconds/anomaly_method/slo_id |
| 服务 RED 指标 | 已打通（DeepFlowSyncer 真实流量 + loadgen 注入，cluster 标签）| 已就绪 |

---

## 2. 总体架构

在 query-api（Go）告警引擎内落地，复用 `evaluateAlerts` 实时循环、降噪（cooldown/dampening）、持久化、webhook。新增统计纯函数模块 + VM range query + SLO 目标表。

```
evaluateAlerts() 定时循环
  ├─ evaluateRule() → value (VM instant)
  ├─ [anomaly 类型] → evaluateRuleAnomaly:
  │     vmRangeQuery(baseline 窗口) → []float64 序列 → ComputeAnomaly(zscore/MAD) → 异常?
  ├─ [burn_rate 类型] → evaluateRuleBurnRate:
  │     查 slo_targets 目标 → vmRangeQuery(5m/30m) → ComputeBurnRate 多窗口 → 烧毁>阈值?
  └─ 命中 → 降噪 → 持久化 → webhook
```

## 3. 模块设计

### 3.1 新增 `internal/api/anomaly.go` — 统计纯函数

- `ZScore(series []float64, current, threshold float64) (z, anomalous bool)`：均值+标准差，`|z|>threshold` 异常（默认 3）
- `MAD(series []float64, current, threshold float64) (score float64, anomalous bool)`：`|current-median|/(1.4826*MAD) > threshold`（默认 3.5），含 MAD=0 兜底
- `ComputeAnomaly(series []float64, current float64, method string, threshold float64) (score float64, anomalous bool)`：统一入口（zscore/mad 分发）
- `median(nums []float64) float64` / `mean` / `stddev` 辅助

### 3.2 `internal/api/victoriametrics.go` — 新增 `vmRangeQuery`

- `vmRangeQuery(promQL string, window time.Duration) ([]float64, error)`：调 `buildQueryRangeURL(query, start, end, step)`，解析 VM 返回 `data.result[0].values` 的 `[timestamp, value]` 为 `[]float64`
- 兼容 metricPromQL 生成的服务 RED 表达式

### 3.3 SLO 目标（B3）

**`slo_targets` 表**（MySQL，query-api 拥有，与 alert_rules 同库）：
- `id, name, service, slo_type`(availability|latency)、`target`(如 99.9)、`window_seconds`(如 2592000=30d)、`enabled`
- CRUD API：`GET/POST/PUT/DELETE /api/v1/slo`

**burn_rate 改造**（alerts.go:873 替换 forecast 共用）：
- `burn_rate` 规则：查 SLO 目标 → 计算实际错误率（`metricPromQL` error_rate）→ 多窗口烧毁率
- `ComputeBurnRate(errRate, sloTarget float64) float64`：`burn_rate = 实际错误率 / 目标错误率`（目标错误率 = 1 - slo.target）
- **多窗口**：基于 `window_seconds` 推导短窗（1% 预算窗口）和中窗（如 6%），分别算 burn_rate；任一超过规则阈值（默认 14.4 / 2.4）告警

### 3.4 AlertRule 字段 + 迁移

`alert_rules` 新增列（hasColumn 幂等）：
- `baseline_seconds INT DEFAULT 900`（anomaly 基线窗口）
- `anomaly_method VARCHAR(16) DEFAULT 'zscore'`（zscore|mad）
- `slo_id VARCHAR(64) DEFAULT ''`（burn_rate 引用 slo_targets）

### 3.5 前端

- **SLO 管理页**：新增 `/slo` 路由 + 侧边栏菜单项，SLO 目标 CRUD 表格（name/service/type/target/window/enabled）
- **规则表单扩展**（Alerts/index.tsx）：
  - anomaly 类型：baseline_seconds + anomaly_method(zscore/mad) + threshold
  - burn_rate 类型：SLO 目标下拉选择器 + 烧毁率阈值

---

## 4. 测试（TDD）

**Go 单测**（anomaly_test.go / alerts_test.go / slo_test.go）：
- ZScore：已知序列均值/标准差 → z 值正确；异常判定
- MAD：含离群点序列 → 稳健（离群点不放大）
- ComputeAnomaly：zscore/mad 分发
- vmRangeQuery：解析 VM values → []float64
- ComputeBurnRate：错误率/目标错误率 → burn_rate
- SLO CRUD handler
- evaluateAlerts anomaly/burn_rate 分支

**前端 tsc**：表单扩展类型正确

## 5. 数据与合规

- 全自研（zscore/MAD/burn_rate 是标准数学）
- SLO 目标表归 query-api（平台基础数据，与 alert_rules 同库，符合数据所有权契约）
- 组件最小化：零新服务，Go 标准库 math

## 6. 自审

- [x] 覆盖总纲 A4（异常检测统计）+ B3（SLO 烧毁率）
- [x] 基于当前代码实际（非旧文档）
- [x] 架构自审修正已纳入（vmRangeQuery 序列 + 独立 anomaly 评估路径）
- [x] 前置（服务 RED 指标）已解决
- [x] 复用现有引擎（evaluateAlerts/降噪/迁移/VM），组件最小化
- [x] 3 项关键决策已与用户对齐
