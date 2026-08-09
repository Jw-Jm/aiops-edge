# 批 2：4 重降噪栈 + 规则类型补齐 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** B1 补齐告警降噪栈（dedupe/cooldown/dampening）+ B2 补齐规则类型（log/trace_latency/trace_error_rate）。

**Architecture:** 复用现有 `evaluateAlerts`（L770-900）与 `evaluateRule`（L1041-1129）。B1 在事件聚合处加 signature 去重、cooldown 冷却、dampening 连续确认；B2 在 evaluateRule 的 metric 分流加 log（CH 日志查询）与 trace_latency/trace_error_rate（CH trace 查询）。

**Tech Stack:** Go(query-api), MySQL, ClickHouse

## Global Constraints

- 告警引擎在 `ai-apm-query-go/internal/api/alerts.go`
- 现有聚合 key：`RuleID + Service`（L847），窗口 `alertGroupInterval`（5m）
- 现有 Type：threshold/mutation/anomaly/forecast/burn_rate/metric_raw
- 现有 metric 分流：K8s / VM(raw) / CH(error_rate|latency_p99|call_count)
- 全部自研，不复制 ongrid 代码；TDD；频繁提交

---

## Task 1: AlertEvent 加 signature + dedupe 去重

**Files:**
- Modify: `ai-apm-query-go/internal/api/alerts.go`（AlertEvent 加 Signature；evaluateAlerts 聚合加 signature 维度）
- Modify: `ai-apm-query-go/internal/store/alerts.go`（AlertEvent DAO 加 signature 列）
- Modify: `ai-apm-query-go/internal/store/mysql.go`（alert_events 加 signature 列 + 兼容 ALTER）
- Test: `ai-apm-query-go/internal/api/alerts_test.go`

**Interfaces:**
- Consumes: `AlertEvent`（现有）
- Produces: `AlertEvent.Signature string`；dedupe 聚合 key 为 `RuleID+Service+Signature`

- [ ] **Step 1: 写失败测试**

创建 `ai-apm-query-go/internal/api/alerts_test.go`：

```go
package api

import "testing"

// TestDedupeSignature 验证相同 rule+service+signature 的事件在窗口内被合并（Count++ 而非新增）
func TestDedupeSignature(t *testing.T) {
	// 同一 rule/service/signature，5m 窗口内应合并
	sig := "svc-error:500"
	e1 := eventSignature("rule1", "svc", sig)
	e2 := eventSignature("rule1", "svc", sig)
	if e1 != e2 {
		t.Fatal("same signature should dedupe")
	}
	e3 := eventSignature("rule1", "svc", "svc-error:502")
	if e1 == e3 {
		t.Fatal("different signature should not dedupe")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestDedupeSignature`
Expected: FAIL — `eventSignature` 未定义

- [ ] **Step 3: alerts.go 加 eventSignature + Signature 字段**

```go
// eventSignature 生成事件指纹（rule+service+detail 维度），用于 dedupe。
func eventSignature(ruleID, service, detail string) string {
	return ruleID + ":" + service + ":" + detail
}
```

`AlertEvent` struct 加 `Signature string \`json:"signature,omitempty"\``。

- [ ] **Step 4: evaluateAlerts 聚合加 signature 维度**

L844-855 的查找条件从 `e.RuleID == rule.ID && e.Service == rule.Service` 改为同时匹配 `e.Signature == sig`；新建事件时设置 `Signature`。`sig` 来自 rule 的 detail（如 `rule.Metric` 或消息前缀）。

- [ ] **Step 5: store/mysql 加 signature 列**

`internal/store/alerts.go` AlertEvent 加 `Signature string`；LoadAll/ReplaceAll 的 SQL 加 signature 列；`internal/store/mysql.go` alert_events 加 `signature VARCHAR(128) DEFAULT '',` + 兼容 ALTER。

- [ ] **Step 6: 测试 + 提交**

Run: `cd ai-apm-query-go && go build ./... && go test ./internal/api/ ./internal/store/`
Expected: PASS

```bash
git add -A
git commit -m "feat(alerts): 事件 signature 去重（rule+service+detail 维度）"
```

---

## Task 2: cooldown 触发冷却

**Files:**
- Modify: `ai-apm-query-go/internal/api/alerts.go`（AlertRule 加 Cooldown；evaluateAlerts 加冷却判断）
- Modify: `ai-apm-query-go/internal/store/alerts.go`（AlertRule DAO 加 cooldown）
- Modify: `ai-apm-query-go/internal/store/mysql.go`（alert_rules 加 cooldown 列）
- Test: `ai-apm-query-go/internal/api/alerts_test.go`

**Interfaces:**
- Consumes: `AlertRule`（现有）
- Produces: `AlertRule.Cooldown int`（分钟）；冷却期内不重复触发（即使窗口外）

- [ ] **Step 1: 写失败测试**

```go
func TestCooldownBlocksRepeat(t *testing.T) {
	// 规则冷却 10m，刚触发过，应立即再次评估时应被冷却拦截
	c := AlertRule{Cooldown: 10}
	if !inCooldown(c, time.Now().Add(-2*time.Minute), time.Now()) {
		t.Fatal("recent trigger should be in cooldown")
	}
	if inCooldown(c, time.Now().Add(-20*time.Minute), time.Now()) {
		t.Fatal("old trigger should not be in cooldown")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/ -run TestCooldownBlocksRepeat`
Expected: FAIL — `inCooldown` 未定义

- [ ] **Step 3: alerts.go 加 Cooldown + inCooldown**

```go
func inCooldown(rule AlertRule, lastTrigger time.Time, now time.Time) bool {
	if rule.Cooldown <= 0 {
		return false
	}
	return now.Sub(lastTrigger) < time.Duration(rule.Cooldown)*time.Minute
}
```

`AlertRule` 加 `Cooldown int \`json:"cooldown,omitempty"\``。

- [ ] **Step 4: evaluateAlerts 冷却判断**

规则 breach 后，若该 rule 有最近触发时间（记录 `lastRuleTrigger map[ruleID]time.Time`），且 `inCooldown` 返回 true → 跳过（不产生新事件/不升级）。

- [ ] **Step 5: store/mysql 加 cooldown 列**

AlertRule DAO 加 Cooldown；mysql.go alert_rules 加 `cooldown INT DEFAULT 0,` + 兼容 ALTER。

- [ ] **Step 6: 测试 + 提交**

Run: `go build ./... && go test ./internal/api/ ./internal/store/`
Expected: PASS

```bash
git add -A
git commit -m "feat(alerts): 规则 cooldown 触发冷却（冷却期内不重复告警）"
```

---

## Task 3: dampening 连续确认

**Files:**
- Modify: `ai-apm-query-go/internal/api/alerts.go`（AlertRule 加 Dampening；evaluateAlerts 加连续 N 次确认）
- Modify: `ai-apm-query-go/internal/store/alerts.go`（AlertRule DAO 加 dampening）
- Modify: `ai-apm-query-go/internal/store/mysql.go`（alert_rules 加 dampening 列）
- Test: `ai-apm-query-go/internal/api/alerts_test.go`

**Interfaces:**
- Consumes: `AlertRule`（现有）
- Produces: `AlertRule.Dampening int`（连续 N 次 breach 才告警）；`ruleStreak map[ruleID]int`

- [ ] **Step 1: 写失败测试**

```go
func TestDampeningStreak(t *testing.T) {
	// 规则 dampening=3，需连续 3 次 breach 才产生事件
	d := AlertRule{Dampening: 3}
	streak := 2 // 当前连续 breach 次数
	if shouldAlertAfterDampening(d, streak) {
		t.Fatal("streak 2 < dampening 3, should not alert")
	}
	if !shouldAlertAfterDampening(d, streak+1) {
		t.Fatal("streak 3 >= dampening 3, should alert")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/ -run TestDampeningStreak`
Expected: FAIL — `shouldAlertAfterDampening` 未定义

- [ ] **Step 3: alerts.go 加 Dampening + shouldAlertAfterDampening**

```go
func shouldAlertAfterDampening(rule AlertRule, streak int) bool {
	if rule.Dampening <= 1 {
		return true
	}
	return streak >= rule.Dampening
}
```

`AlertRule` 加 `Dampening int \`json:"dampening,omitempty"\``。

- [ ] **Step 4: evaluateAlerts 加 streak**

用 `ruleStreak map[ruleID]int`：breach 时 streak++，未 breach 时 reset=0；`shouldAlertAfterDampening(rule, streak)` 为 false 时跳过事件创建。

- [ ] **Step 5: store/mysql 加 dampening 列**

AlertRule DAO 加 Dampening；mysql.go alert_rules 加 `dampening INT DEFAULT 0,` + 兼容 ALTER。

- [ ] **Step 6: 测试 + 提交**

Run: `go build ./... && go test ./internal/api/ ./internal/store/`
Expected: PASS

```bash
git add -A
git commit -m "feat(alerts): 规则 dampening 连续确认（连续 N 次 breach 才告警）"
```

---

## Task 4: log 规则类型

**Files:**
- Modify: `ai-apm-query-go/internal/api/alerts.go`（evaluateRule 加 log 类型：CH 日志查询）
- Test: `ai-apm-query-go/internal/api/alerts_test.go`

**Interfaces:**
- Consumes: `evaluateRule`（现有）、`h.queryClickHouse`（现有）
- Produces: `Type=="log"` 时 metric 为 `log_error_rate`（错误日志占比）或 `log_keyword`（关键词命中数）

- [ ] **Step 1: 写失败测试**

```go
func TestLogTypeQuery(t *testing.T) {
	// log_error_rate：错误日志占全部日志比例
	q := logMetricQuery("svc", "log_error_rate", "error")
	if !strings.Contains(q, "log_records") {
		t.Fatal("log query should target log_records")
	}
	if !strings.Contains(q, "severity") {
		t.Fatal("log_error_rate should filter by severity")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/ -run TestLogTypeQuery`
Expected: FAIL — `logMetricQuery` 未定义

- [ ] **Step 3: alerts.go 加 log 类型处理**

```go
// logMetricQuery 构造日志类规则的 CH 查询。
// log_error_rate：错误日志占比；log_keyword：关键词命中数。
func logMetricQuery(service, metric, keyword string) string {
	if metric == "log_error_rate" {
		return fmt.Sprintf(
			"SELECT countIf(severity IN ('ERROR','FATAL')) / count() * 100 FROM observability.log_records WHERE service_name='%s' AND timestamp >= now() - INTERVAL 5 MINUTE", service)
	}
	return fmt.Sprintf(
		"SELECT count() FROM observability.log_records WHERE service_name='%s' AND body LIKE '%%%s%%' AND timestamp >= now() - INTERVAL 5 MINUTE", service, keyword)
}
```

- [ ] **Step 4: evaluateRule 加 log 分流**

在 evaluateRule 的 K8s 分支前（L1052）加：

```go
if rule.Type == "log" {
	sql := logMetricQuery(rule.Service, rule.Metric, rule.Name)
	// 执行 CH 查询（复用现有 runClickhouseQuery/queryClickHouse 模式）
	return h.evalCHQuery(sql)
}
```

复用现有 CH 查询执行逻辑（evaluateRule 的 CH 分支 L1076-1128 已有查询模式）。

- [ ] **Step 5: 测试 + 提交**

Run: `go build ./... && go test ./internal/api/`
Expected: PASS

```bash
git add -A
git commit -m "feat(alerts): log 规则类型（log_error_rate/log_keyword，CH 日志查询）"
```

---

## Task 5: trace_latency / trace_error_rate 规则类型

**Files:**
- Modify: `ai-apm-query-go/internal/api/alerts.go`（evaluateRule 加 trace_latency/trace_error_rate 类型）
- Test: `ai-apm-query-go/internal/api/alerts_test.go`

**Interfaces:**
- Consumes: `evaluateRule`（现有）
- Produces: `Type=="trace_latency"` 查 CH trace 延迟；`Type=="trace_error_rate"` 查 CH trace 错误率

- [ ] **Step 1: 写失败测试**

```go
func TestTraceTypeQuery(t *testing.T) {
	// trace_latency：链路 P99 延迟
	q := traceMetricQuery("svc", "trace_latency")
	if !strings.Contains(q, "trace_spans") {
		t.Fatal("trace query should target trace_spans")
	}
	if !strings.Contains(q, "quantile") {
		t.Fatal("trace_latency should use quantile")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api/ -run TestTraceTypeQuery`
Expected: FAIL — `traceMetricQuery` 未定义

- [ ] **Step 3: alerts.go 加 trace 类型处理**

```go
// traceMetricQuery 构造链路类规则的 CH 查询。
// trace_latency：P99 延迟；trace_error_rate：错误率。
func traceMetricQuery(service, metric string) string {
	if metric == "trace_latency" {
		return fmt.Sprintf(
			"SELECT quantile(0.99)(duration_ns)/1000000 FROM observability.trace_spans WHERE service_name='%s' AND start_time >= now() - INTERVAL 5 MINUTE", service)
	}
	return fmt.Sprintf(
		"SELECT countIf(is_error=1) / count() * 100 FROM observability.trace_spans WHERE service_name='%s' AND start_time >= now() - INTERVAL 5 MINUTE", service)
}
```

- [ ] **Step 4: evaluateRule 加 trace 分流**

在 evaluateRule 加：

```go
if rule.Type == "trace_latency" || rule.Type == "trace_error_rate" {
	sql := traceMetricQuery(rule.Service, rule.Metric)
	return h.evalCHQuery(sql)
}
```

- [ ] **Step 5: 测试 + 提交**

Run: `go build ./... && go test ./internal/api/`
Expected: PASS

```bash
git add -A
git commit -m "feat(alerts): trace_latency/trace_error_rate 规则类型（CH trace 查询）"
```

---

## Task 6: 前端规则表单支持新字段 + 部署验证

**Files:**
- Modify: `observability-frontend/src/pages/Alerts/`（规则表单加 type/cooldown/dampening）
- Test: `tsc --noEmit`

**Interfaces:**
- Consumes: AlertRule 新字段（cooldown/dampening）
- Produces: 规则创建表单支持 log/trace 类型 + cooldown/dampening 输入

- [ ] **Step 1: 规则表单支持 type 选项**

规则创建/编辑表单的 type 下拉加 `log`/`trace_latency`/`trace_error_rate` 选项；加 cooldown（分钟）与 dampening（连续次数）输入。

- [ ] **Step 2: tsc + 提交**

Run: `cd observability-frontend && node_modules/.bin/tsc --noEmit`
Expected: exit 0

```bash
git add -A
git commit -m "feat(web): 规则表单支持 log/trace 类型 + cooldown/dampening"
```

- [ ] **Step 3: 部署 + 冒烟**

重建 query-api + frontend 镜像 + 部署；冒烟：创建 log 类型规则 + cooldown 规则验证生效。

---

## Self-Review

**1. Spec coverage:** 覆盖批 2 的 B1（dedupe/cooldown/dampening）与 B2（log/trace_latency/trace_error_rate）。
**2. Placeholder scan:** 无 TBD/TODO；`eventSignature`/`inCooldown`/`shouldAlertAfterDampening`/`logMetricQuery`/`traceMetricQuery` 定义完整。
**3. Type consistency:** `Cooldown`/`Dampening`/`Signature` 字段名跨 Task 一致。
**4. 合规:** 全部自研，不复制 ongrid 代码。
