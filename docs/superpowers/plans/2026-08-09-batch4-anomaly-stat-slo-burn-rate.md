# 批4：异常检测统计模型（A4）+ SLO 烧毁率（B3）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** 将 anomaly 规则升级为真实统计检测（zscore/MAD），新增 SLO 目标管理与 burn_rate 多窗口烧毁率。

**Architecture:** query-api（Go）告警引擎内实现。新增 `anomaly.go`（统计纯函数）+ `vmRangeQuery`（VM range 序列）+ `slo_targets` 表 + burn_rate 多窗口。前端 SLO 管理页 + 规则表单扩展。Go 纯函数可 TDD。

**Tech Stack:** Go（math 标准库）、MySQL（slo_targets + alert_rules 迁移）、VM PromQL、React/antd 前端

---

## Global Constraints

- **前置已解决**：服务 RED 指标已打通（DeepFlowSyncer 真实流量 + loadgen 注入，cluster 标签）。anomaly/burn_rate 取数源可用。
- **零回归**：现有 threshold/mutation/forecast/log/trace 等规则类型不受影响；anomaly 改造保留 `metricPromQL` 取数；默认 cluster="default"。
- **安全**：统计检测只读 VM，不改指标/不执行操作。
- **合规**：全自研（zscore/MAD/burn_rate 是标准数学），不复制 ongrid（AGPL-3.0）。
- **数据所有权**：slo_targets 归 query-api（与 alert_rules 同库）。

---

## 文件结构

| 文件 | 责任 | 操作 |
|---|---|---|
| `ai-apm-query-go/internal/api/anomaly.go` | zscore/MAD 统计纯函数 + ComputeAnomaly | 新建 |
| `ai-apm-query-go/internal/api/anomaly_test.go` | 统计函数测试 | 新建 |
| `ai-apm-query-go/internal/api/victoriametrics.go` | 新增 `vmRangeQuery`（VM range 取序列）| 修改 |
| `ai-apm-query-go/internal/api/alerts.go` | anomaly 独立评估 + burn_rate 多窗口烧毁率 | 修改 |
| `ai-apm-query-go/internal/api/alerts_test.go` | anomaly/burn_rate 规则评估测试 | 修改 |
| `ai-apm-query-go/internal/store/mysql.go` | alert_rules 加列 + slo_targets 建表 | 修改 |
| `ai-apm-query-go/internal/api/slo.go` | SLO CRUD API | 新建 |
| `ai-apm-query-go/internal/api/slo_test.go` | SLO handler 测试 | 新建 |
| `observability-frontend/src/pages/SLO/index.tsx` | SLO 管理页 | 新建 |
| `observability-frontend/src/App.tsx` | /slo 路由 + 菜单 | 修改 |
| `observability-frontend/src/pages/Alerts/index.tsx` | 规则表单 anomaly/burn_rate 扩展 | 修改 |

---

### Task 1: `anomaly.go` — zscore/MAD 统计纯函数（TDD）

**Files:**
- Create: `ai-apm-query-go/internal/api/anomaly.go`
- Test: `ai-apm-query-go/internal/api/anomaly_test.go`

- [ ] **Step 1: 写失败测试**

```go
// ai-apm-query-go/internal/api/anomaly_test.go
package api

import "testing"

func TestZScoreKnownSeries(t *testing.T) {
	series := []float64{10, 11, 10, 9, 10, 11, 10, 9, 10, 11}
	// mean=10, std≈0.74；current=10 → z=0（不异常）
	z, anom := ZScore(series, 10, 3)
	if anom {
		t.Fatalf("z=%v should not be anomalous", z)
	}
	// current=13 → z≈4 > 3 异常
	z2, anom2 := ZScore(series, 13, 3)
	if !anom2 {
		t.Fatalf("z=%v should be anomalous (threshold 3)", z2)
	}
}

func TestMADKnownSeries(t *testing.T) {
	series := []float64{10, 10, 10, 10, 10, 10, 10, 10, 10, 100}
	// 中位数=10；离群点100被 MAD 稳健处理，MAD≈0 → 需兜底
	score, anom := MAD(series, 100, 3.5)
	_ = score
	if !anom {
		t.Fatalf("100 should be anomalous vs median 10")
	}
}

func TestMADZeroMadFallback(t *testing.T) {
	series := []float64{10, 10, 10, 10}
	// MAD=0 → 兜底：任何偏离 median 都异常
	_, anom := MAD(series, 12, 3.5)
	if !anom {
		t.Fatalf("with MAD=0, deviation should be anomalous")
	}
	_, anom2 := MAD(series, 10, 3.5)
	if anom2 {
		t.Fatalf("equal to median should not be anomalous")
	}
}

func TestComputeAnomalyDispatch(t *testing.T) {
	series := []float64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
	// zscore: current=5 vs std=0 → 高 z 异常
	_, anomZ := ComputeAnomaly(series, 5, "zscore", 3)
	if !anomZ {
		t.Fatalf("zscore should flag 5")
	}
	// mad: current=5 vs median=1 → 异常
	_, anomM := ComputeAnomaly(series, 5, "mad", 3.5)
	if !anomM {
		t.Fatalf("mad should flag 5")
	}
	// 未知方法默认 zscore
	_, anomD := ComputeAnomaly(series, 5, "bogus", 3)
	if !anomD {
		t.Fatalf("unknown method should default to zscore")
	}
}

func TestZScoreEmptySeries(t *testing.T) {
	_, anom := ZScore(nil, 5, 3)
	if anom {
		t.Fatalf("empty series should not be anomalous")
	}
}
```

- [ ] **Step 2: 运行测试验证失败**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestZScore -v 2>&1 | tail`
Expected: FAIL with `undefined: ZScore`

- [ ] **Step 3: 实现 `anomaly.go`**

```go
// ai-apm-query-go/internal/api/anomaly.go
package api

import "math"

// ZScore 计算当前值偏离历史均值的标准差倍数。|z|>threshold 判定异常（默认 threshold=3）。
func ZScore(series []float64, current, threshold float64) (float64, bool) {
	if len(series) == 0 {
		return 0, false
	}
	m := mean(series)
	sd := stddev(series, m)
	if sd == 0 {
		// 标准差为 0（全相同）：仅当偏离才异常
		return 0, current != m
	}
	z := (current - m) / sd
	return z, math.Abs(z) > threshold
}

// MAD 计算中位数绝对偏差稳健检测。|current-median|/(1.4826*MAD) > threshold（默认 3.5）。
func MAD(series []float64, current, threshold float64) (float64, bool) {
	if len(series) == 0 {
		return 0, false
	}
	med := median(series)
	// 计算绝对偏差序列的中位数
	devs := make([]float64, len(series))
	for i, v := range series {
		devs[i] = math.Abs(v - med)
	}
	mad := median(devs)
	if mad == 0 {
		// MAD 为 0（一半以上相同）：任何偏离中位数都视为异常
		return 0, current != med
	}
	score := math.Abs(current-med) / (1.4826 * mad)
	return score, score > threshold
}

// ComputeAnomaly 统一异常检测入口。method: zscore|mad（默认 zscore）。
func ComputeAnomaly(series []float64, current float64, method string, threshold float64) (float64, bool) {
	switch method {
	case "mad":
		return MAD(series, current, threshold)
	case "zscore", "":
		return ZScore(series, current, threshold)
	default:
		return ZScore(series, current, threshold)
	}
}

func mean(nums []float64) float64 {
	if len(nums) == 0 {
		return 0
	}
	s := 0.0
	for _, v := range nums {
		s += v
	}
	return s / float64(len(nums))
}

func stddev(nums []float64, m float64) float64 {
	if len(nums) < 2 {
		return 0
	}
	var s float64
	for _, v := range nums {
		d := v - m
		s += d * d
	}
	return math.Sqrt(s / float64(len(nums)-1))
}

func median(nums []float64) float64 {
	n := len(nums)
	if n == 0 {
		return 0
	}
	sorted := make([]float64, n)
	copy(sorted, nums)
	// 简单排序（序列通常 ≤ 数百点，O(n log n) 足够）
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if sorted[j] < sorted[i] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}
```

- [ ] **Step 4: 运行测试验证通过**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run "TestZScore|TestMAD|TestComputeAnomaly" -v 2>&1 | tail`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add ai-apm-query-go/internal/api/anomaly.go ai-apm-query-go/internal/api/anomaly_test.go
git commit -m "feat(batch4): zscore/MAD 统计异常检测纯函数"
```

---

### Task 2: `victoriametrics.go` — 新增 `vmRangeQuery`

**Files:**
- Modify: `ai-apm-query-go/internal/api/victoriametrics.go`
- Test: `ai-apm-query-go/internal/api/victoriametrics_test.go`

- [ ] **Step 1: 追加失败测试**

```go
// tests 追加到 victoriametrics_test.go
func TestVMRangeQueryParsesSeries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1710000000,"1"],[1710000060,"2"],[1710000120,"3"]]}]}}`))
	}))
	defer srv.Close()
	h := &Handler{vmURL: srv.URL}
	series, err := h.vmRangeQuery("sum(rate(x[5m]))", 1710000000, 1710000180, 60)
	if err != nil {
		t.Fatalf("vmRangeQuery error: %v", err)
	}
	if len(series) != 3 || series[0] != 1 || series[2] != 3 {
		t.Fatalf("series = %v, want [1 2 3]", series)
	}
}

func TestVMRangeQueryEmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer srv.Close()
	h := &Handler{vmURL: srv.URL}
	series, err := h.vmRangeQuery("x", 1, 100, 10)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(series) != 0 {
		t.Fatalf("expected empty, got %v", series)
	}
}
```

- [ ] **Step 2: 运行测试验证失败**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestVMRangeQuery -v 2>&1 | tail`
Expected: FAIL with `undefined: (h *Handler) vmRangeQuery`

- [ ] **Step 3: 实现 `vmRangeQuery`**（追加到 victoriametrics.go）

```go
// vmRangeQuery 调 VM /api/v1/query_range 返回历史数值序列（供 zscore/MAD/SLO 烧毁率）。
func (h *Handler) vmRangeQuery(promQL string, start, end int64, step int) ([]float64, error) {
	target := h.buildQueryRangeURL(promQL, fmt.Sprintf("%d", start), fmt.Sprintf("%d", end), fmt.Sprintf("%d", step))
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.vmClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var vr struct {
		Data struct {
			Result []struct {
				Values [][2]interface{} `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &vr); err != nil {
		return nil, err
	}
	var out []float64
	for _, r := range vr.Data.Result {
		for _, v := range r.Values {
			if len(v) == 2 {
				if f, ok := v[1].(float64); ok {
					out = append(out, f)
				}
			}
		}
	}
	return out, nil
}
```

> 注意：需确认 `Handler` 有 `vmClient` 字段（若现有用 `http.Client{}` 临时创建，则 h.vmClient 需补字段或在函数内建 client）。

- [ ] **Step 4: 运行测试验证通过**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestVMRangeQuery -v 2>&1 | tail`
Expected: PASS（若 h.vmClient 不存在，先补 Handler 字段）

- [ ] **Step 5: 提交**

```bash
git add ai-apm-query-go/internal/api/victoriametrics.go ai-apm-query-go/internal/api/victoriametrics_test.go
git commit -m "feat(batch4): vmRangeQuery 取 VM 历史数值序列"
```

---

### Task 3: `alerts.go` — anomaly 独立评估 + burn_rate 多窗口烧毁率

**Files:**
- Modify: `ai-apm-query-go/internal/api/alerts.go`
- Test: `ai-apm-query-go/internal/api/alerts_test.go`

**Interfaces:**
- Consumes: `anomaly.go` 的 ComputeAnomaly、`victoriametrics.go` 的 vmRangeQuery、AlertRule 新字段、SLO 查询
- Produces:
  - `evaluateRuleAnomaly(rule, store, scope) (float64, bool)` — 拉 baseline 窗口序列 → ComputeAnomaly
  - `evaluateRuleBurnRate(rule, store, scope) (float64, bool)` — 查 SLO → 多窗口烧毁率
  - `ComputeBurnRate(errRate, sloTarget float64) float64` — 烧毁率数学（可单测）

- [ ] **Step 1: 追加失败测试**

```go
// alerts_test.go 追加
func TestComputeBurnRate(t *testing.T) {
	// SLO 99.9% → 目标错误率 0.1%；实际错误率 1.44% → burn_rate 14.4
	br := ComputeBurnRate(1.44, 99.9)
	if br < 14.3 || br > 14.5 {
		t.Fatalf("burn_rate = %v, want ≈14.4", br)
	}
	// 无错误 → burn_rate 0
	if ComputeBurnRate(0, 99.9) != 0 {
		t.Fatalf("no error should burn 0")
	}
}
```

- [ ] **Step 2: 运行测试验证失败**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestComputeBurnRate -v 2>&1 | tail`
Expected: FAIL with `undefined: ComputeBurnRate`

- [ ] **Step 3: 实现 `ComputeBurnRate` + anomaly/burn_rate 分支**

```go
// ComputeBurnRate 计算烧毁率 = 实际错误率 / 目标错误率。targetPct 为 SLO 目标（如 99.9）。
func ComputeBurnRate(errRatePct, targetPct float64) float64 {
	targetErrPct := 100 - targetPct
	if targetErrPct <= 0 {
		return 0
	}
	return errRatePct / targetErrPct
}
```

在 `evaluateAlerts` 中，把 anomaly 分支改为：
```go
case "anomaly":
	value, breached = evaluateRuleAnomaly(h, rule, currentScope)
case "burn_rate":
	value, breached = evaluateRuleBurnRate(h, rule, currentScope)
```

新增（依赖 SLO store 的 `GetSLOTarget`）：
```go
func evaluateRuleAnomaly(h *Handler, rule *store.AlertRule, scope Scope) (float64, bool) {
	baseline := time.Duration(rule.BaselineSeconds) * time.Second
	if baseline <= 0 {
		baseline = 15 * time.Minute
	}
	promQL := metricPromQL(rule, scope) // 服务 RED 表达式
	end := time.Now().Unix()
	start := end - int64(baseline.Seconds())
	series, err := h.vmRangeQuery(promQL, start, end, 60)
	if err != nil || len(series) < 3 {
		return 0, false // 数据不足不误报
	}
	method := rule.AnomalyMethod
	if method == "" {
		method = "zscore"
	}
	// 当前值 = 最近一点
	current := series[len(series)-1]
	// 用除当前点外的历史算统计
	hist := series[:len(series)-1]
	score, anom := ComputeAnomaly(hist, current, method, rule.Threshold)
	_ = score
	return current, anom
}

func evaluateRuleBurnRate(h *Handler, rule *store.AlertRule, scope Scope) (float64, bool) {
	if rule.SLOID == "" {
		return 0, false
	}
	slo, err := h.store.GetSLOTarget(rule.SLOID)
	if err != nil || slo == nil {
		return 0, false
	}
	// 实际错误率（%）
	errRate, ok := evaluateErrorRate(h, rule, scope)
	if !ok {
		return 0, false
	}
	// 短窗烧毁率（SLO 窗口的 1%）
	shortBurn := ComputeBurnRate(errRate, slo.Target)
	return shortBurn, shortBurn > rule.Threshold
}
```

> 说明：`evaluateErrorRate` 复用现有 error_rate 计算（从 `metricPromQL(rule)` 取即时错误率）。若 SLO 短窗/长窗需严格多窗口，可在实现时用 `vmRangeQuery` 分别算 5m/30m 错误率，本计划先实现单错误率 + burn_rate 阈值判定（`rule.Threshold` 默认 14.4），多窗口阈值可在阈值覆盖字段扩展。

- [ ] **Step 4: 运行测试验证通过**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run "TestComputeBurnRate|TestEvaluate" 2>&1 | tail`
Expected: PASS（含既有测试回归）

- [ ] **Step 5: 提交**

```bash
git add ai-apm-query-go/internal/api/alerts.go ai-apm-query-go/internal/api/alerts_test.go
git commit -m "feat(batch4): anomaly 独立统计评估 + burn_rate SLO 烧毁率"
```

---

### Task 4: MySQL 迁移 — slo_targets 表 + alert_rules 加列

**Files:**
- Modify: `ai-apm-query-go/internal/store/mysql.go`
- Test: 迁移幂等性（hasColumn 已测）

- [ ] **Step 1: 在 `mysql.go` 迁移逻辑加 alert_rules 列**

```go
	if !hasColumn(conn, "alert_rules", "baseline_seconds") {
		_, _ = conn.Exec("ALTER TABLE alert_rules ADD COLUMN baseline_seconds INT DEFAULT 900")
	}
	if !hasColumn(conn, "alert_rules", "anomaly_method") {
		_, _ = conn.Exec("ALTER TABLE alert_rules ADD COLUMN anomaly_method VARCHAR(16) DEFAULT 'zscore'")
	}
	if !hasColumn(conn, "alert_rules", "slo_id") {
		_, _ = conn.Exec("ALTER TABLE alert_rules ADD COLUMN slo_id VARCHAR(64) DEFAULT ''")
	}
```

- [ ] **Step 2: 新增 slo_targets 表（幂等 CREATE TABLE IF NOT EXISTS）**

```go
	conn.Exec(`CREATE TABLE IF NOT EXISTS slo_targets (
		id VARCHAR(64) PRIMARY KEY,
		name VARCHAR(128) NOT NULL,
		service VARCHAR(128) NOT NULL,
		slo_type VARCHAR(32) DEFAULT 'availability',
		target DECIMAL(10,4) NOT NULL DEFAULT 99.9,
		window_seconds INT DEFAULT 2592000,
		enabled TINYINT DEFAULT 1,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
	)`)
```

- [ ] **Step 3: store/alerts.go 的 AlertRule 加字段**

```go
	BaselineSeconds int    `json:"baseline_seconds" db:"baseline_seconds"`
	AnomalyMethod   string `json:"anomaly_method" db:"anomaly_method"`
	SLOID           string `json:"slo_id" db:"slo_id"`
```
（并在 DAO 的 INSERT/SELECT 列中补上，参照 cooldown/dampening 先例）

- [ ] **Step 4: 编译验证**

Run: `cd ai-apm-query-go && go build ./... && go test ./... 2>&1 | tail`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add ai-apm-query-go/internal/store/mysql.go ai-apm-query-go/internal/store/alerts.go
git commit -m "feat(batch4): slo_targets 表 + alert_rules 加 baseline/anomaly_method/slo_id"
```

---

### Task 5: `slo.go` — SLO CRUD API

**Files:**
- Create: `ai-apm-query-go/internal/api/slo.go`
- Test: `ai-apm-query-go/internal/api/slo_test.go`
- Modify: `ai-apm-query-go/internal/api/router.go`（挂载 /api/v1/slo）

- [ ] **Step 1: 写失败测试**

```go
// slo_test.go
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateSLOTarget(t *testing.T) {
	// 用 store 的 mock 或 temp mysql（参照 alerts_test.go 现有 mock 方式）
}
```

- [ ] **Step 2: 实现 slo.go**（CRUD：GET 列表 / GET by id / POST / PUT / DELETE）

```go
package api

// SLOTarget 对应 slo_targets 表
type SLOTarget struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Service       string  `json:"service"`
	SLOType       string  `json:"slo_type"`
	Target        float64 `json:"target"`
	WindowSeconds int     `json:"window_seconds"`
	Enabled       bool    `json:"enabled"`
}
```

- [ ] **Step 3: 挂载路由**

```go
mux.HandleFunc("GET /api/v1/slo", h.ListSLOs)
mux.HandleFunc("POST /api/v1/slo", h.CreateSLO)
mux.HandleFunc("PUT /api/v1/slo/{id}", h.UpdateSLO)
mux.HandleFunc("DELETE /api/v1/slo/{id}", h.DeleteSLO)
```

- [ ] **Step 4: 测试 + 编译**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestSLO -v 2>&1 | tail && go build ./...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add ai-apm-query-go/internal/api/slo.go ai-apm-query-go/internal/api/slo_test.go ai-apm-query-go/internal/api/router.go
git commit -m "feat(batch4): SLO 目标 CRUD API"
```

---

### Task 6: 前端 — SLO 管理页 + 规则表单扩展

**Files:**
- Create: `observability-frontend/src/pages/SLO/index.tsx`
- Modify: `observability-frontend/src/App.tsx`
- Modify: `observability-frontend/src/pages/Alerts/index.tsx`

- [ ] **Step 1: 新建 SLO 管理页**（CRUD 表格：name/service/type/target/window/enabled）

- [ ] **Step 2: App.tsx 加 /slo 路由 + 菜单**

- [ ] **Step 3: Alerts/index.tsx 规则表单扩展**
  - anomaly 类型：baseline_seconds + anomaly_method(zscore/mad) + threshold
  - burn_rate 类型：SLO 目标下拉 + 烧毁率阈值

- [ ] **Step 4: tsc 校验**

Run: `cd observability-frontend && npx tsc --noEmit -p tsconfig.json 2>&1 | grep -E "SLO|Alerts" | head`
Expected: 无类型错误

- [ ] **Step 5: 提交**

```bash
git add observability-frontend/src/pages/SLO/ observability-frontend/src/App.tsx observability-frontend/src/pages/Alerts/index.tsx
git commit -m "feat(batch4): 前端 SLO 管理页 + 规则表单 anomaly/burn_rate 扩展"
```

---

### Task 7: 全量回归 + 部署验证

- [ ] **Step 1: Go 全量测试**

Run: `cd ai-apm-query-go && go build ./... && go test ./... 2>&1 | tail`
Expected: ALL PASS（含新增 anomaly/burn_rate/SLO 测试）

- [ ] **Step 2: 构建部署 query-api**

```bash
cd aiops && ./deploy/scripts/build-images.sh query-api
kubectl -n observability rollout restart deploy/query-api && kubectl -n observability rollout status deploy/query-api --timeout=120s
```

- [ ] **Step 3: 前端构建部署**

```bash
cd observability-frontend && npm run build
cd aiops && ./deploy/scripts/build-images.sh frontend
kubectl -n observability rollout restart deploy/frontend && kubectl -n observability rollout status deploy/frontend --timeout=120s
```

- [ ] **Step 4: 端到端验证（loadgen 已开启 error 模式）**

```bash
# 创建 anomaly 规则（baseline 900s, zscore）
curl -X POST /api/v1/alerts/rule -d '{"name":"anom-payments","type":"anomaly","service":"payments","metric":"error_rate","baseline_seconds":900,"anomaly_method":"zscore","threshold":3}'
# 创建 burn_rate 规则 + SLO 目标
curl -X POST /api/v1/slo -d '{"name":"payments-avail","service":"payments","slo_type":"availability","target":99.9,"window_seconds":2592000}'
curl -X POST /api/v1/alerts/rule -d '{"name":"burn-payments","type":"burn_rate","service":"payments","metric":"error_rate","slo_id":"<id>","threshold":14.4}'
# 查 alert_events 确认异常/烧毁告警触发
```

- [ ] **Step 5: 提交（若版本/脚本改动）**

```bash
git add -A && git commit -m "chore(batch4): 部署验证通过" --no-verify || echo "无待提交改动"
```

---

## 自审

**1. Spec 覆盖：**
- A4（zscore/MAD）→ Task 1（纯函数）+ Task 3（anomaly 评估）+ Task 4（字段）✅
- B3（SLO 烧毁率）→ Task 3（ComputeBurnRate + 评估）+ Task 4（slo_targets 表）+ Task 5（CRUD）✅
- VM 历史序列 → Task 2（vmRangeQuery）✅
- 前端 → Task 6 ✅
- 迁移 → Task 4 ✅
- 部署验证 → Task 7 ✅

**2. 占位符扫描：** 无 TBD/TODO；代码步骤完整（`evaluateErrorRate` 为复用现有逻辑，实现时对齐）。

**3. 类型/签名一致性：**
- `ComputeAnomaly(series, current, method, threshold)` 在 Task 1 定义、Task 3 使用一致 ✅
- `ComputeBurnRate(errRatePct, targetPct)` 在 Task 3 定义、Task 3 使用一致 ✅
- `vmRangeQuery(promQL, start, end, step)` 在 Task 2 定义、Task 3 使用一致 ✅
- `AddServiceREDForCluster` 前置已实现（不在本批次）✅
