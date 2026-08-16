package pipeline

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-ingest-go/internal/model"
)

// TestWatermarkPersistLoadRoundTrip 验证水位持久化往返：persist 后可被新构造的
// syncer（模拟重启）恢复为同一时间戳。
func TestWatermarkPersistLoadRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("INGEST_WAL_DIR", tmp)

	want := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	s1 := NewDeepFlowSyncer("127.0.0.1", 8123, "cluster-x", nil, nil, nil, nil)
	if s1.watermarkPath == "" {
		t.Fatalf("watermarkPath empty, want persistence under %s", tmp)
	}
	s1.persistLastSync(want)

	s2 := NewDeepFlowSyncer("127.0.0.1", 8123, "cluster-x", nil, nil, nil, nil)
	if !s2.lastSyncTime.Equal(want) {
		t.Fatalf("restored lastSyncTime = %v, want %v", s2.lastSyncTime, want)
	}
}

// TestLoadLastSyncBadFileKeepsZero 坏文件（非法时间戳）应保持零值，等同首次启动。
func TestLoadLastSyncBadFileKeepsZero(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("INGEST_WAL_DIR", tmp)
	if err := os.WriteFile(filepath.Join(tmp, watermarkFileName), []byte("not-a-timestamp"), 0o644); err != nil {
		t.Fatalf("write bad watermark: %v", err)
	}
	s := NewDeepFlowSyncer("127.0.0.1", 8123, "cluster-x", nil, nil, nil, nil)
	if !s.lastSyncTime.IsZero() {
		t.Fatalf("bad watermark should keep zero lastSyncTime, got %v", s.lastSyncTime)
	}
}

// TestLoadLastSyncMissingFileKeepsZero 首次启动（无水位文件）行为与现状一致：零值。
func TestLoadLastSyncMissingFileKeepsZero(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("INGEST_WAL_DIR", tmp)
	s := NewDeepFlowSyncer("127.0.0.1", 8123, "cluster-x", nil, nil, nil, nil)
	if !s.lastSyncTime.IsZero() {
		t.Fatalf("missing watermark should keep zero lastSyncTime, got %v", s.lastSyncTime)
	}
}

func TestParseSyncInterval_Default(t *testing.T) {
	if got := parseSyncInterval(""); got != 60*time.Second {
		t.Fatalf("default interval = %v, want 60s", got)
	}
}

func TestParseSyncInterval_ValidSeconds(t *testing.T) {
	if got := parseSyncInterval("10"); got != 10*time.Second {
		t.Fatalf("got %v, want 10s", got)
	}
}

func TestParseSyncInterval_ValidDuration(t *testing.T) {
	if got := parseSyncInterval("15s"); got != 15*time.Second {
		t.Fatalf("got %v, want 15s", got)
	}
}

func TestParseSyncInterval_ClampedOutOfRange(t *testing.T) {
	if got := parseSyncInterval("2h"); got != 60*time.Second { // >3600s 回退默认
		t.Fatalf("too-large interval = %v, want default", got)
	}
	if got := parseSyncInterval("1s"); got != 60*time.Second { // <5s 回退默认
		t.Fatalf("too-small interval = %v, want default", got)
	}
}

func TestParseSyncInterval_AtMaxAllowed(t *testing.T) {
	if got := parseSyncInterval("1h"); got != 3600*time.Second { // 恰好等于上限，应允许
		t.Fatalf("at-max interval = %v, want 3600s", got)
	}
}

func TestParseSyncInterval_InvalidString(t *testing.T) {
	if got := parseSyncInterval("abc"); got != 60*time.Second {
		t.Fatalf("invalid interval = %v, want default", got)
	}
}

func TestClampStartTime_NoClockSkew(t *testing.T) {
	now := time.Now().UTC()
	last := now.Add(-2 * time.Minute)
	if got := clampStartTime(last, now); got.Unix() != last.Unix() {
		t.Fatalf("clamp changed valid last time: got %v, want %v", got, last)
	}
}

func TestClampStartTime_Future(t *testing.T) {
	now := time.Now().UTC()
	if got := clampStartTime(now.Add(time.Hour), now); got.After(now) {
		t.Fatalf("clamp did not pull future time back: %v", got)
	}
}

func TestClampStartTime_TooOld(t *testing.T) {
	now := time.Now().UTC()
	if got := clampStartTime(now.Add(-2*time.Hour), now); got.Before(now.Add(-15 * time.Minute)) {
		t.Fatalf("clamp allowed too-old start: %v", got)
	}
}

// fakeREDMetric 记录 AddServiceREDForCluster 的调用，用于验证 DeepFlowSyncer 累加 RED。
type fakeREDMetric struct {
	calls []string // "cluster|service|isError"
}

func (f *fakeREDMetric) AddServiceREDForCluster(cluster, service string, isError bool, durationNs uint64) {
	flag := "ok"
	if isError {
		flag = "err"
	}
	f.calls = append(f.calls, cluster+"|"+service+"|"+flag)
}

type fakeSpanWriter struct{}

func (f *fakeSpanWriter) Add(*model.Span) {}

// TestNewDeepFlowSyncer_BindsREDMetric 验证 cluster/redMetric 正确绑定。
func TestNewDeepFlowSyncer_BindsREDMetric(t *testing.T) {
	red := &fakeREDMetric{}
	s := NewDeepFlowSyncer("10.0.0.1", 8123, "cluster-x", nil, &fakeSpanWriter{}, nil, red)
	if s.cluster != "cluster-x" {
		t.Fatalf("cluster = %q, want cluster-x", s.cluster)
	}
	if s.redMetric == nil {
		t.Fatalf("redMetric not bound")
	}
	s.redMetric.AddServiceREDForCluster(s.cluster, "payments", true, 1000)
	if len(red.calls) != 1 || red.calls[0] != "cluster-x|payments|err" {
		t.Fatalf("redMetric not wired to fake: %v", red.calls)
	}
}

// TestREDAccumulation 用注入 rows 驱动 RED 累加核心逻辑（错误判定 + cluster 传递）。
func TestREDAccumulation(t *testing.T) {
	red := &fakeREDMetric{}
	s := &DeepFlowSyncer{redMetric: red, cluster: "cluster-x"}
	s.accumulateREDFromRows([]map[string]interface{}{
		{"dst": "svc-b", "response_duration": uint64(1200), "response_code": uint64(200)},
		{"dst": "svc-b", "response_duration": uint64(800), "response_code": uint64(503)},
		{"dst": "svc-c", "response_duration": uint64(500), "response_code": uint64(404)},
	})
	if len(red.calls) != 3 {
		t.Fatalf("expected 3 RED calls, got %v", red.calls)
	}
	if red.calls[0] != "cluster-x|svc-b|ok" {
		t.Fatalf("call0 = %q, want cluster-x|svc-b|ok", red.calls[0])
	}
	if red.calls[1] != "cluster-x|svc-b|err" {
		t.Fatalf("call1 = %q, want cluster-x|svc-b|err (5xx)", red.calls[1])
	}
	// 4xx 不算错误
	if red.calls[2] != "cluster-x|svc-c|ok" {
		t.Fatalf("call2 = %q, want cluster-x|svc-c|ok (4xx not error)", red.calls[2])
	}
}

// accumulateREDFromRows 封装 syncTraces 中的 RED 累加逻辑，便于单测（与 syncTraces 共用同一判定）。
func (s *DeepFlowSyncer) accumulateREDFromRows(rows []map[string]interface{}) {
	for _, r := range rows {
		dst, _ := r["dst"].(string)
		if dst == "" {
			continue
		}
		durUs := toUint(r["response_duration"])
		durNs := durUs * 1000
		code := int64(toUint(r["response_code"]))
		isErr := uint8(0)
		if (code >= 500 && code <= 599) || (code == 0 && durNs >= 1000000000) {
			isErr = 1
		}
		if s.redMetric != nil {
			s.redMetric.AddServiceREDForCluster(s.cluster, dst, isErr == 1, durNs)
		}
	}
}

// capturingSpanWriter 记录写入的 span，用于验证 ns 映射。
type capturingSpanWriter struct {
	spans []*model.Span
}

func (w *capturingSpanWriter) Add(s *model.Span) {
	w.spans = append(w.spans, s)
}

// mockDFCH 构造 mock DeepFlow ClickHouse：解析 query 参数返回固定 JSON 行。
// 返回 syncer 所需的 host/port（用于 NewDeepFlowSyncer）。
func mockDFCH(t *testing.T, rowsJSON string) (host string, port int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"meta":[],"data":[` + rowsJSON + `]}`))
	}))
	t.Cleanup(srv.Close)
	h, pStr := splitHostPortMock(srv.URL)
	p, _ := strconv.Atoi(pStr)
	return h, p
}

// splitHostPortMock 从 test server URL 拆 host/port（避免依赖 url.Parse 细节）。
func splitHostPortMock(u string) (string, string) {
	s := strings.TrimPrefix(u, "http://")
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// TestSyncTracesWritesK8sNamespace 验证 deepflow 同步写 span 时 k8s_namespace 有值：
// 修复前 src_ns/dst_ns 未提取，所有 deepflow 同步服务的 k8s_namespace 为空串。
func TestSyncTracesWritesK8sNamespace(t *testing.T) {
	rows := "" +
		`{"start_time":"2026-08-15 10:00:00.000000","response_duration":1000,"src":"query-api","src_ns":"observability","dst":"ingest","dst_ns":"observability","request_resource":"/api/orders","response_code":200},` +
		`{"start_time":"2026-08-15 10:00:01.000000","response_duration":2000,"src":"deepflow-server","src_ns":"deepflow","dst":"deepflow-mysql","dst_ns":"deepflow","request_resource":"/query","response_code":503}`
	host, port := mockDFCH(t, rows)

	sw := &capturingSpanWriter{}
	s := NewDeepFlowSyncer(host, port, "cluster-x", nil, sw, nil, nil)
	s.sampleRate = 1.0 // 关闭抽样，确保全部写入
	windowStart := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)
	if err := s.syncTraces(windowStart); err != nil {
		t.Fatalf("syncTraces: %v", err)
	}
	if len(sw.spans) != 4 {
		t.Fatalf("expected 4 spans (2 flows × client+server), got %d", len(sw.spans))
	}
	// 服务名 → ns 校验
	nsByService := map[string]string{}
	for _, sp := range sw.spans {
		nsByService[sp.ServiceName] = sp.K8sNamespace
	}
	want := map[string]string{
		"query-api":       "observability",
		"ingest":          "observability",
		"deepflow-server": "deepflow",
		"deepflow-mysql":  "deepflow",
	}
	for svc, ns := range want {
		if got := nsByService[svc]; got != ns {
			t.Errorf("service %s k8s_namespace=%q, want %q", svc, got, ns)
		}
	}
}

// TestSyncTracesK8sNamespaceEmptyFallback 验证 deepflow 无 ns 数据时 k8s_namespace 为空串（不 panic）。
func TestSyncTracesK8sNamespaceEmptyFallback(t *testing.T) {
	rows := `{"start_time":"2026-08-15 10:00:00.000000","response_duration":1000,"src":"legacy","src_ns":"","dst":"old-svc","dst_ns":"","request_resource":"/api","response_code":200}`
	host, port := mockDFCH(t, rows)

	sw := &capturingSpanWriter{}
	s := NewDeepFlowSyncer(host, port, "cluster-x", nil, sw, nil, nil)
	s.sampleRate = 1.0
	windowStart := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)
	if err := s.syncTraces(windowStart); err != nil {
		t.Fatalf("syncTraces: %v", err)
	}
	for _, sp := range sw.spans {
		if sp.K8sNamespace != "" {
			t.Errorf("span %s/%s k8s_namespace=%q, want empty when deepflow has no ns", sp.ServiceName, sp.SpanKind, sp.K8sNamespace)
		}
	}
}
