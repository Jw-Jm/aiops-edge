# Phase 5 Writer Implementation + Atomic-Cutover Readiness Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 重构 `ai-apm-ingest-go` 与 `ai-event-collector` 为 tenant/cluster/resource 三字段强制携带的 writer，补齐 event-collector WAL/checkpoint，并实现 VM/VLogs writer adapter（`PRODUCTION_ACTIVE=false`）——为 Phase 6 原子 cutover 做好 ready，**但不切走任何生产路径**。

**Architecture:** 严格遵循 R2 §71/§72 原子 cutover 规则——Phase 5 只建 writer 新链路 + cutover readiness，不 production 切换。ClickHouse `log_records` 保持写入（P4.5 已标 LEGACY），其停写跟随 Phase 6 原子窗口。复用 Phase 4 已冻结的 `internal/telemetrylabels` scope-label 校验。

**Tech Stack:** Go 1.23（ingest）、Go 1.21（event-collector）、ClickHouse HTTP TabSeparated、VictoriaMetrics remote-write 风格、VictoriaLogs /insert JSON lines、标准库。

## Global Constraints

- V9.2 §71：所有新写入必须有 `tenant_id`/`cluster_id`/`resource_id`，缺失→`partial`/`missing_fields`，禁止猜测、禁止 `default` 兜底。
- V9.2 §71：保留 WAL / append / ack / replay / compaction / bounded retry / health / graceful shutdown。
- V9.2 §72：Phase 5 与 Phase 6 生产 cutover 必须同一原子窗口；Phase 5 不得单独切走生产 writer。
- V9.2 §5：所有 API 时间 UTC / RFC3339；数据库业务时间 TIMESTAMP(6)；禁止依赖本地时区做运行逻辑判断。
- Phase 4 label contracts：`tenant_id`/`cluster_id` 必须 canonical UUID，拒绝空 / `default` / slug / 数值。
- `telemetrylabels.ValidateScopeLabels(labels, scope)` 是唯一校验入口。
- **禁止 git add / commit / push。**
- 禁止打印任何 Secret / token / kubeconfig / 证书私钥 / API key（§90）。

---

## 任务分解（文件结构）

Phase 5 分成两个服务，共 6 个任务。每个任务独立可测。

```
ai-event-collector/                       ← Go 1.21 单包 main
  config.go        (改) 去 default 默认值 + 三字段校验入口
  scope.go         (新) canonicalUUID 校验 + EventScope + Validate  —— 复用 telemetrylabels 语义（单包无跨包依赖）
  wal.go           (新) 精简 WAL（append/ack/replay/compact）       —— 仿 ingest wal.go 但保持最小
  clickhouse.go    (改) EventWriter 接入 WAL + 三字段 fail-closed + QueryLatestTS 加 tenant
  main.go          (改) 启动时校验 tenant/cluster canonical UUID，非法则 fail-closed 退出
  *_test.go        (新) 单测

ai-apm-ingest-go/internal/
  telemetrylabels/labels.go     (不改) 已冻结
  telemetry/                   (新) TelemetryWriter 抽象 + VM/VLogs adapter（不 production active）
    writer.go        (新) TelemetryWriter 接口 + WriteResult + 通用 bounded retry
    vmetrics.go      (新) VictoriaMetricsWriter（/api/v1/import 或 /insert）
    vlogs.go         (新) VictoriaLogsWriter（/insert/jsonline）
    *_test.go        (新) 单测 + label contract 校验测试
  clickhouse/writer.go        (改) 去 serialize 兜底 default → fail-closed
  clickhouse/log_writer.go    (改) 同上（log_records 保持写入至 Phase 6）
  clickhouse/metrics_writer.go(改) 同上
  pipeline/ingest.go          (改) SetClusterID 去 default 兜底
  cmd/ingest/main.go          (改) X-Tenant-ID/CLUSTER_ID 去 default 兜底 + 缺失 fail-closed
```

---

## 任务 1：ai-event-collector 三字段强制 + fail-closed

**Files:**
- Create: `ai-event-collector/scope.go`
- Create: `ai-event-collector/scope_test.go`
- Modify: `ai-event-collector/config.go:40-60`（loadConfig 默认值）
- Modify: `ai-event-collector/clickhouse.go`（EventWriter 构造 + serialize 三字段）
- Modify: `ai-event-collector/main.go`（启动校验）

**Interfaces:**
- Consumes: `Config{TenantID, ClusterID}`（已存在）
- Produces: `func validateCanonicalUUID(s string) bool`；`type EventScope struct { TenantID, ClusterID string }`；`func (s EventScope) Validate() error`

- [ ] **Step 1: 写失败测试** `scope_test.go`

```go
package main

import "testing"

func TestEventScopeValidate(t *testing.T) {
	cases := []struct {
		name      string
		tenant    string
		cluster   string
		wantValid bool
	}{
		{"valid canonical uuid", "3f3c3b3a-0000-4000-8000-000000000001", "3f3c3b3a-0000-4000-8000-000000000002", true},
		{"empty tenant", "", "3f3c3b3a-0000-4000-8000-000000000002", false},
		{"default tenant", "default", "3f3c3b3a-0000-4000-8000-000000000002", false},
		{"slug cluster", "3f3c3b3a-0000-4000-8000-000000000001", "orbstack", false},
		{"numeric cluster", "3f3c3b3a-0000-4000-8000-000000000001", "1", false},
		{"empty cluster", "3f3c3b3a-0000-4000-8000-000000000001", "", false},
	}
	for _, c := range cases {
		sc := EventScope{TenantID: c.tenant, ClusterID: c.cluster}
		if err := sc.Validate(); (err == nil) != c.wantValid {
			t.Errorf("%s: Validate() err=%v, wantValid=%v", c.name, err, c.wantValid)
		}
	}
}

func TestValidateCanonicalUUID(t *testing.T) {
	if !validateCanonicalUUID("3f3c3b3a-0000-4000-8000-000000000001") {
		t.Error("expected valid UUID accepted")
	}
	if validateCanonicalUUID("default") || validateCanonicalUUID("orbstack") || validateCanonicalUUID("1") || validateCanonicalUUID("") {
		t.Error("expected invalid refs rejected")
	}
}
```

- [ ] **Step 2: 运行测试确认失败** — `cd ai-event-collector && go test -run TestEventScopeValidate -v` — Expected: FAIL（`EventScope`/`validateCanonicalUUID` undefined）

- [ ] **Step 3: 实现最小代码** `scope.go`

```go
package main

import (
	"errors"
	"regexp"
)

// canonicalUUID 对齐 Phase 3/Phase 4 冻结语义（telemetrylabels 同 pattern）。
// 拒绝空 / default / slug / 数值。
var canonicalUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

var (
	ErrInvalidTenantID  = errors.New("tenant_id must be canonical uuid")
	ErrInvalidClusterID = errors.New("cluster_id must be canonical uuid")
)

// EventScope 是事件写入的 tenant/cluster 身份。两者都必须是 canonical UUID。
type EventScope struct {
	TenantID  string
	ClusterID string
}

func validateCanonicalUUID(s string) bool { return canonicalUUID.MatchString(s) }

func (s EventScope) Validate() error {
	if !validateCanonicalUUID(s.TenantID) {
		return ErrInvalidTenantID
	}
	if !validateCanonicalUUID(s.ClusterID) {
		return ErrInvalidClusterID
	}
	return nil
}
```

- [ ] **Step 4: 运行测试确认通过** — `go test -run TestEventScopeValidate -v` — Expected: PASS

- [ ] **Step 5: 更新 `config.go`** 去除 `default` 默认值（保持字段存在但无默认，缺省即空 → 后续 fail-closed）

```go
func loadConfig() *Config {
	return &Config{
		TenantID:          os.Getenv("TENANT_ID"),   // 不再默认 "default"
		ClusterID:         os.Getenv("CLUSTER_ID"),  // 不再默认 "default"
		CHHost:            getenv("CLICKHOUSE_HOST", "clickhouse.observability.svc.cluster.local"),
		// ... 其余不变
	}
}
```

- [ ] **Step 6: 更新 `main.go`** 启动时 fail-closed 校验

```go
scope := EventScope{TenantID: cfg.TenantID, ClusterID: cfg.ClusterID}
if err := scope.Validate(); err != nil {
	log.Fatalf("invalid event scope (TENANT_ID/CLUSTER_ID must be canonical UUID): %v", err)
}
```

- [ ] **Step 7: 更新 `clickhouse.go` serializeEvents** 移除隐式 default，改为显式三字段（字段本身已在 EventWriter 持有）

在 `serializeEvents` 中保持 `escapeTSV(w.tenantID)`/`escapeTSV(w.clusterID)` 原样即可（不再有 default 兜底来源，因为 Config 已无默认值）。若构造时 scope 非法则 NewEventWriter 之前已 fail-closed。无需改 serialize 逻辑本身；改注释说明三字段由构造时强校验。

- [ ] **Step 8: 全量测试** — `go build ./... && go vet ./... && go test ./...` — Expected: PASS

- [ ] **Step 9: 记录**（Phase 5 输出格式，不 commit）

---

## 任务 2：ai-event-collector WAL 持久化

**Files:**
- Create: `ai-event-collector/wal.go`
- Create: `ai-event-collector/wal_test.go`
- Modify: `ai-event-collector/clickhouse.go`（EventWriter 集成 WAL，替代纯内存 retry）
- Modify: `ai-event-collector/config.go`（新增 `WALDir`）

**Interfaces:**
- Consumes: `walDir string`
- Produces: `func NewWAL(dir, file string) (*WAL, error)`；`func (w *WAL) Append(kind string, v []byte) (uint64, error)`；`func (w *WAL) Ack(seq uint64)`；`func (w *WAL) ReadAll() ([]walEntry, error)`；`func (w *WAL) Compact()`；`func (w *WAL) Close()`

- [ ] **Step 1: 写失败测试** `wal_test.go`（验证 append→ack→replay 语义）

```go
package main

import "testing"

func TestWALAppendAckReplay(t *testing.T) {
	dir := t.TempDir()
	w1, err := NewWAL(dir, "events.log")
	if err != nil { t.Fatal(err) }
	s1, _ := w1.Append("event", []byte("row-1"))
	s2, _ := w1.Append("event", []byte("row-2"))
	w1.Ack(s1)
	w1.Close() // 模拟崩溃/重启，水位已持久化

	w2, err := NewWAL(dir, "events.log")
	if err != nil { t.Fatal(err) }
	defer w2.Close()
	entries, err := w2.ReadAll()
	if err != nil { t.Fatal(err) }
	if len(entries) != 1 || entries[0].Seq != s2 {
		t.Fatalf("expected only unacked seq=%d on replay, got %v", s2, entries)
	}
}

func TestWALCompactKeepsUnacked(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWAL(dir, "events.log")
	if err != nil { t.Fatal(err) }
	s1, _ := w.Append("event", []byte("row-1"))
	s2, _ := w.Append("event", []byte("row-2"))
	w.Ack(s1)
	w.Compact()
	entries, err := w.ReadAll()
	if err != nil { t.Fatal(err) }
	if len(entries) != 1 || entries[0].Seq != s2 {
		t.Fatalf("expected only unacked seq=%d after compact, got %v", s2, entries)
	}
	w.Close()
}
```

- [ ] **Step 2: 运行测试确认失败** — `go test -run TestWAL -v` — Expected: FAIL（`NewWAL` undefined）

- [ ] **Step 3: 实现最小 WAL** `wal.go`

```go
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// walEntry 是 WAL 最小单元。value 为序列化批次。
type walEntry struct {
	Seq   uint64 `json:"seq"`
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// WAL 提供崩溃安全持久化：写入 CH 前先落盘，重启后从磁盘恢复未确认批次。
// 仿 ai-apm-ingest-go/internal/clickhouse/wal.go，保持最小实现。
type WAL struct {
	mu               sync.Mutex
	path             string
	ackPath          string
	file             *os.File
	writer           *bufio.Writer
	seq              uint64
	acked            map[uint64]struct{}
	consecutiveAckSeq uint64
}

type ackState struct {
	Consecutive uint64   `json:"consecutive"`
	Acked       []uint64 `json:"acked"`
}

func NewWAL(dir, file string) (*WAL, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil { return nil, err }
	path := filepath.Join(dir, file)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil { return nil, err }
	w := &WAL{path: path, ackPath: path + ".ack", file: f, writer: bufio.NewWriterSize(f, 64*1024), acked: make(map[uint64]struct{})}
	w.recover()
	return w, nil
}

func (w *WAL) recover() {
	w.mu.Lock(); defer w.mu.Unlock()
	if st, acked, err := w.readAck(); err == nil {
		w.consecutiveAckSeq = st
		for _, s := range acked { if s > st { w.acked[s] = struct{}{} } }
	}
	if last, err := scanLastSeq(w.path); err == nil && last > w.seq { w.seq = last }
	if w.seq < w.consecutiveAckSeq { w.seq = w.consecutiveAckSeq }
}

func (w *WAL) Append(kind string, v []byte) (uint64, error) {
	w.mu.Lock(); defer w.mu.Unlock()
	w.seq++
	e := walEntry{Seq: w.seq, Kind: kind, Value: base64.StdEncoding.EncodeToString(v)}
	b, err := json.Marshal(e)
	if err != nil { return 0, err }
	if _, err := w.writer.Write(append(b, '\n')); err != nil { return 0, err }
	if err := w.writer.Flush(); err != nil { return 0, err }
	return w.seq, nil
}

func (w *WAL) Ack(seq uint64) {
	w.mu.Lock(); defer w.mu.Unlock()
	if seq > w.consecutiveAckSeq { w.acked[seq] = struct{}{} }
	for {
		next := w.consecutiveAckSeq + 1
		if _, ok := w.acked[next]; !ok { break }
		delete(w.acked, next)
		w.consecutiveAckSeq = next
	}
	w.persistAck()
}

func (w *WAL) ReadAll() ([]walEntry, error) {
	w.mu.Lock(); defer w.mu.Unlock()
	return w.readRemaining(w.consecutiveAckSeq)
}

func (w *WAL) Compact() {
	w.mu.Lock(); defer w.mu.Unlock()
	if w.seq <= w.consecutiveAckSeq {
		_ = w.file.Truncate(0); _, _ = w.file.Seek(0, 0); _ = w.file.Sync(); return
	}
	remaining, err := w.readRemaining(w.consecutiveAckSeq)
	if err != nil { return }
	tmp := w.path + ".tmp"
	tf, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil { return }
	bw := bufio.NewWriterSize(tf, 64*1024)
	for _, e := range remaining {
		if _, acked := w.acked[e.Seq]; acked { continue }
		b, _ := json.Marshal(e)
		_, _ = bw.Write(append(b, '\n'))
	}
	_ = bw.Flush(); _ = tf.Sync(); _ = tf.Close()
	_ = os.Rename(tmp, w.path)
	nf, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil { return }
	w.file = nf; w.writer = bufio.NewWriterSize(nf, 64*1024)
	w.acked = make(map[uint64]struct{})
	w.persistAck()
}

func (w *WAL) Close() { w.mu.Lock(); defer w.mu.Unlock(); _ = w.writer.Flush(); _ = w.file.Sync(); _ = w.file.Close() }

func (w *WAL) persistAck() {
	st := ackState{Consecutive: w.consecutiveAckSeq}
	for s := range w.acked { st.Acked = append(st.Acked, s) }
	sort.Slice(st.Acked, func(i, j int) bool { return st.Acked[i] < st.Acked[j] })
	b, _ := json.Marshal(st)
	_ = os.WriteFile(w.ackPath+".tmp", b, 0o644)
	_ = os.Rename(w.ackPath+".tmp", w.ackPath)
}

func (w *WAL) readAck() (uint64, []uint64, error) {
	b, err := os.ReadFile(w.ackPath)
	if err != nil { return 0, nil, err }
	if n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64); err == nil { return n, nil, nil }
	var st ackState
	if err := json.Unmarshal(b, &st); err != nil { return 0, nil, err }
	return st.Consecutive, st.Acked, nil
}

func (w *WAL) readRemaining(after uint64) ([]walEntry, error) {
	f, err := os.Open(w.path)
	if err != nil { return nil, err }
	defer f.Close()
	sc := bufio.NewScanner(f)
	var out []walEntry
	for sc.Scan() {
		var e walEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil { continue }
		if _, acked := w.acked[e.Seq]; acked { continue }
		if e.Seq > after { out = append(out, e) }
	}
	return out, sc.Err()
}

func scanLastSeq(path string) (uint64, error) {
	f, err := os.Open(path)
	if err != nil { return 0, err }
	defer f.Close()
	sc := bufio.NewScanner(f)
	var last uint64
	for sc.Scan() {
		var e walEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil { continue }
		if e.Seq > last { last = e.Seq }
	}
	return last, sc.Err()
}
```

- [ ] **Step 4: 运行测试确认通过** — `go test -run TestWAL -v` — Expected: PASS

- [ ] **Step 5: `config.go` 新增 `WALDir`** — `WALDir: os.Getenv("WAL_DIR")`（默认空→内存重试，向后兼容）

- [ ] **Step 6: 集成 WAL 到 `EventWriter`**（`clickhouse.go`）：
  - `EventWriter` 增加 `wal *WAL`
  - `NewEventWriter` 里 `if cfg.WALDir != "" { w.wal, _ = NewWAL(cfg.WALDir, "events-wal.log"); 启动恢复 ReadAll → enqueueRetry }`
  - `flush` 中 `insertBatch` 失败时：有 WAL → `Append` 后入 retry；无 WAL → 入内存 retry（现状）
  - retry 成功时 `Ack(seq)`；丢弃最旧批次时 `Ack(seq)`
  - `Close` 前 `wal.Close()`

- [ ] **Step 7: 全量测试** — `go build ./... && go vet ./... && go test ./...` — Expected: PASS

- [ ] **Step 8: 记录**

---

## 任务 3：ai-event-collector checkpoint key=tenant+cluster+source

**Files:**
- Modify: `ai-event-collector/clickhouse.go`（`QueryLatestTS`）
- Modify: `ai-event-collector/k8s_events.go`（checkpoint 调用带 tenant）

**Interfaces:**
- Produces: `func (w *EventWriter) QueryLatestTS(source string) (time.Time, error)`（改签名或加 tenant 过滤）

- [ ] **Step 1: 写失败测试**（验证 query SQL 含 tenant_id 过滤）— 由于 QueryLatestTS 打真实 CH，用可注入 query 构建器

改为抽取 `func latestTSQuery(source, tenantID, clusterID string) string` 纯函数，测试其 SQL 字符串：

```go
func TestLatestTSQueryIncludesTenant(t *testing.T) {
	q := latestTSQuery("k8s", "3f3c3b3a-0000-4000-8000-000000000001", "3f3c3b3a-0000-4000-8000-000000000002")
	if !strings.Contains(q, "tenant_id = '3f3c3b3a-0000-4000-8000-000000000001'") {
		t.Errorf("query must filter tenant_id, got: %s", q)
	}
	if !strings.Contains(q, "cluster_id = '3f3c3b3a-0000-4000-8000-000000000002'") {
		t.Errorf("query must filter cluster_id, got: %s", q)
	}
	if !strings.Contains(q, "source = 'k8s'") {
		t.Errorf("query must filter source, got: %s", q)
	}
}
```

- [ ] **Step 2: 运行测试确认失败** — `go test -run TestLatestTSQuery -v` — Expected: FAIL（`latestTSQuery` undefined）

- [ ] **Step 3: 实现** — `QueryLatestTS` 内部改用 `latestTSQuery(source, w.tenantID, w.clusterID)`

```go
func latestTSQuery(source, tenantID, clusterID string) string {
	return fmt.Sprintf("SELECT max(ts) FROM observability.k8s_events WHERE source = '%s' AND tenant_id = '%s' AND cluster_id = '%s'", source, tenantID, clusterID)
}
```

- [ ] **Step 4: 运行测试确认通过** — `go test -run TestLatestTSQuery -v` — Expected: PASS

- [ ] **Step 5: `k8s_events.go` checkpoint 不改变（QueryLatestTS("k8s") 已带 tenant via writer）** — 确认 `EventWriter.tenantID` 在调用时已通过 scope.Validate

- [ ] **Step 6: 全量测试** — Expected: PASS

- [ ] **Step 7: 记录**

---

## 任务 4：ai-apm-ingest-go 消除 default 兜底

**Files:**
- Modify: `cmd/ingest/main.go:36-41,119-123,144-148`
- Modify: `internal/clickhouse/writer.go:180-183`
- Modify: `internal/clickhouse/log_writer.go:280-283`
- Modify: `internal/clickhouse/metrics_writer.go:147-150`
- Modify: `internal/pipeline/ingest.go:83-89`
- Modify: `internal/clickhouse/writer_test.go`（更新 default 断言 → fail-closed 断言）

**Interfaces:**
- Consumes: `CLUSTER_ID` env（canonical UUID）
- Produces: fail-closed 行为：tenant/cluster 缺失或非 canonical → 拒绝写入/请求

- [ ] **Step 1: 更新失败测试** `writer_test.go` — 把 `cluster_id 应为 default` 断言改为"缺失 cluster_id 时 serialize 不兜底、输出空"（fail-closed 语义由上层负责，serialize 层不再注入 default）

```go
// 修正：Phase 5 不再把空 cluster_id 兜底为 default。
if cols[1] != "" {
	t.Fatalf("cluster_id should be empty when unset (no default fallback), got %q", cols[1])
}
```

- [ ] **Step 2: 运行测试确认失败** — `cd ai-apm-ingest-go && go test ./internal/clickhouse/ -run TestSerializeSpansEscapesSpecialChars -v` — Expected: FAIL（当前输出 `default`）

- [ ] **Step 3: 修改 `writer.go` serializeSpans** 移除 `clusterID == "" → "default"` 兜底

```go
func (w *Writer) serializeSpans(spans []*model.Span) []byte {
	for _, s := range spans {
		fmt.Fprintf(&buf, "%s\t%s\t...", escapeTSV(s.TenantID), escapeTSV(s.ClusterID), ...)
	}
}
```

- [ ] **Step 4: 运行测试确认通过** — Expected: PASS

- [ ] **Step 5: 同理修改 `log_writer.go`、`metrics_writer.go`** 移除 default 兜底，更新对应测试

- [ ] **Step 6: 修改 `pipeline/ingest.go` SetClusterID** 移除 `""→"default"`

- [ ] **Step 7: 修改 `cmd/ingest/main.go`**：
  - `clusterID` 不再默认 `"default"`，空则记录为缺失（依赖三字段校验）
  - `/v1/traces`、`/v1/logs` 的 `X-Tenant-ID` 空时不再 `→"default"`，改为返回 400 fail-closed（或标记 missing_fields）

- [ ] **Step 8: 全量测试** — `go build ./... && go vet ./... && go test ./...` — Expected: PASS

- [ ] **Step 9: 记录**

---

## 任务 5：VictoriaMetrics writer adapter（PRODUCTION_ACTIVE=false）

**Files:**
- Create: `internal/telemetry/writer.go`
- Create: `internal/telemetry/vmetrics.go`
- Create: `internal/telemetry/vmetrics_test.go`

**Interfaces:**
- Consumes: `telemetrylabels.ValidateScopeLabels`
- Produces: `type WriteResult struct { Status string; ErrorCode string; Retryable bool }`；`type MetricsWriter interface { Write(labels map[string]string, value float64, ts time.Time) WriteResult }`；`func NewVictoriaMetricsWriter(endpoint string) *VictoriaMetricsWriter`

- [ ] **Step 1: 写失败测试** `vmetrics_test.go` — label 校验（非法 cluster 拒绝）+ 序列化

```go
func TestVMRejectsInvalidClusterID(t *testing.T) {
	w := NewVictoriaMetricsWriter("http://vm:8428")
	labels := map[string]string{
		"tenant_id": "3f3c3b3a-0000-4000-8000-000000000001",
		"cluster_id": "orbstack", // 非法
		"__name__": "http_requests_total",
	}
	res := w.Write(labels, 1, time.Now())
	if res.ErrorCode != "INVALID_SCOPE" {
		t.Fatalf("expected INVALID_SCOPE, got %q", res.ErrorCode)
	}
}
```

- [ ] **Step 2: 运行测试确认失败** — `go test ./internal/telemetry/ -run TestVMRejectsInvalidClusterID -v` — Expected: FAIL（package 不存在）

- [ ] **Step 3: 实现** `writer.go` + `vmetrics.go`（复用 telemetrylabels.ValidateScopeLabels，Write 先校验后构造 remote-write 行；不实际发送到生产，仅结构化）

- [ ] **Step 4: 运行测试确认通过** — Expected: PASS

- [ ] **Step 5: 记录**

---

## 任务 6：VictoriaLogs writer adapter（PRODUCTION_ACTIVE=false）

**Files:**
- Create: `internal/telemetry/vlogs.go`
- Create: `internal/telemetry/vlogs_test.go`

**Interfaces:**
- Consumes: `telemetrylabels.ValidateScopeLabels`
- Produces: `func NewVictoriaLogsWriter(endpoint string) *VictoriaLogsWriter`；`func (w *VictoriaLogsWriter) WriteLog(labels map[string]string, body string, ts time.Time) WriteResult`

- [ ] **Step 1: 写失败测试** — label 校验 + JSON line 序列化（streamFields 契约：tenant_id + cluster_id 必须）

- [ ] **Step 2: 运行测试确认失败**

- [ ] **Step 3: 实现** `vlogs.go`

- [ ] **Step 4: 运行测试确认通过**

- [ ] **Step 5: 记录**

---

## 任务 7：Gate 5 验证与 cutover readiness 报告

**Files:**
- Create: `docs/AIOPS_PHASE5_GATE.md`

- [ ] **Step 1: 全量测试** — `cd ai-event-collector && go build ./... && go vet ./... && go test ./...`；`cd ai-apm-ingest-go && go build ./... && go vet ./... && go test ./...`

- [ ] **Step 2: WAL replay / storage recovery 验证** — event-collector `TestWALAppendAckReplay`、ingest `TestWALRestartRestoresAckWatermark` 等已覆盖；确认全绿

- [ ] **Step 3: telemetry label contract PASS** — `telemetrylabels` 已有 3 单测；新增 VM/VLogs adapter 测试覆盖

- [ ] **Step 4: 输出 Phase 5 Gate 报告** `docs/AIOPS_PHASE5_GATE.md`，格式按 R2 §92（PHASE/STATUS/IMPLEMENTED/GATE_RESULTS/NEXT_PHASE）

**Gate 5 判定标准（R2 §71）：**
- new writer tests PASS ✅
- WAL replay PASS ✅
- storage outage recovery PASS ✅
- event dedup/backlog observable ✅
- telemetry labels contract PASS ✅
- VM/VLogs writer adapter PASS ✅
- ClickHouse legacy log writer removal ready（`log_records` 仍写入，Phase 6 原子窗口停写）✅
- **writer ready for atomic cutover**（未切换，Phase 6 与 reader 同窗口）✅

## 记录的输出格式（每个任务后）

```text
PHASE: 5
STATUS: PASS
NEXT_PHASE: 6 (NOT_STARTED — atomic cutover window)
GIT_ACTION: NONE
```
