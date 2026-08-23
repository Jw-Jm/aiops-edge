package pipeline

import (
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-ingest-go/internal/model"
)

// TestSetREDSink_FlushCallsSink 验证注册 redSink 后，flushMetrics 会把聚合的
// RED 服务指标推给 sink（P6.5 new 链双写 VictoriaMetrics 的接入点）。
func TestSetREDSink_FlushCallsSink(t *testing.T) {
	p := &Pipeline{
		metricsAgg: make(map[metricsKey]*metricsValue),
		edgesAgg:   make(map[edgeKey]*edgeValue),
	}
	p.SetClusterID("cluster-a")

	var got []*model.ServiceMetric
	p.SetREDSink(func(m *model.ServiceMetric) {
		got = append(got, m)
	})

	ts := time.Date(2026, 8, 20, 12, 0, 5, 0, time.UTC)
	bucket := ts.Truncate(time.Minute).Format("2006-01-02T15:04")
	mk := metricsKey{tenantID: "tenant-a", serviceName: "checkout", callerService: "api", timeBucket: bucket}
	p.metricsAgg[mk] = &metricsValue{callCount: 10, errorCount: 2, durationSumNs: 5000000000, durationCount: 10}

	p.flushMetrics()

	if len(got) != 1 {
		t.Fatalf("expected 1 ServiceMetric, got %d", len(got))
	}
	m := got[0]
	if m.TenantID != "tenant-a" || m.ServiceName != "checkout" || m.CallerService != "api" {
		t.Fatalf("unexpected metric identity: %+v", m)
	}
	if m.ClusterID != "cluster-a" {
		t.Fatalf("clusterID = %q, want cluster-a", m.ClusterID)
	}
	if m.CallCount != 10 || m.ErrorCount != 2 || m.DurationSumNs != 5000000000 {
		t.Fatalf("unexpected RED values: %+v", m)
	}
	if !m.TimeBucket.Equal(ts.Truncate(time.Minute)) {
		t.Fatalf("timeBucket = %v, want %v", m.TimeBucket, ts.Truncate(time.Minute))
	}
}

// TestSetREDSink_NilSink_NoPanic 验证未注册 sink 时 flushMetrics 行为不变（向后兼容）。
func TestSetREDSink_NilSink_NoPanic(t *testing.T) {
	p := &Pipeline{
		metricsAgg: make(map[metricsKey]*metricsValue),
		edgesAgg:   make(map[edgeKey]*edgeValue),
	}
	ts := time.Date(2026, 8, 20, 12, 0, 5, 0, time.UTC)
	bucket := ts.Truncate(time.Minute).Format("2006-01-02T15:04")
	p.metricsAgg[metricsKey{tenantID: "t", serviceName: "svc", timeBucket: bucket}] = &metricsValue{callCount: 1}
	p.flushMetrics() // 不应 panic
}

// TestDeepFlowRED_IndependentOfLegacySpanWriter 验证 B1 正交化：
// spanWriter 为 nil（LEGACY_WRITER_ENABLED=false，legacy CH 停用）时，
// syncTraces 仍遍历 l7 rows、计算 isErr/durNs 并累加 VM RED 指标，
// 仅跳过 legacy ClickHouse span 写入。修复前 syncTraces 首行
// `if s.spanWriter == nil { return nil }` 会整体短路，RED 永不累加。
func TestDeepFlowRED_IndependentOfLegacySpanWriter(t *testing.T) {
	t.Setenv("INGEST_WAL_DIR", t.TempDir())
	rows := "" +
		`{"start_time":"2026-08-15 10:00:00.000000","response_duration":1000,"src":"query-api","src_ns":"observability","dst":"ingest","dst_ns":"observability","request_resource":"/api/orders","response_code":200},` +
		`{"start_time":"2026-08-15 10:00:01.000000","response_duration":2000,"src":"deepflow-server","src_ns":"deepflow","dst":"deepflow-mysql","dst_ns":"deepflow","request_resource":"/query","response_code":503}`
	host, port := mockDFCH(t, rows)

	red := &fakeREDMetric{}
	// spanWriter/logWriter/edgeWriter 全 nil：模拟 LEGACY_WRITER_ENABLED=false
	s := NewDeepFlowSyncer(host, port, "cluster-x", nil, nil, nil, red)
	s.sampleRate = 1.0 // 关闭抽样，确保 rows 全部遍历
	windowStart := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)
	if err := s.syncTraces(windowStart); err != nil {
		t.Fatalf("syncTraces with nil spanWriter: %v", err)
	}
	if len(red.calls) == 0 {
		t.Fatal("expected RED accumulation when spanWriter is nil, got 0 calls")
	}
	want := []string{"cluster-x|ingest|ok", "cluster-x|deepflow-mysql|err"}
	for _, w := range want {
		found := false
		for _, c := range red.calls {
			if c == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing RED call %q, got %v", w, red.calls)
		}
	}
}

// TestLegacyDisabled_DeepFlowREDStillAccumulates 回归：LEGACY_WRITER_ENABLED=false
// （edgeWriter/spanWriter/logWriter 全 nil）时完整 Sync() 仍累加 VM RED 且不 panic。
func TestLegacyDisabled_DeepFlowREDStillAccumulates(t *testing.T) {
	t.Setenv("INGEST_WAL_DIR", t.TempDir())
	// 同一行同时满足 application_map（time/src/src_ns/dst/dst_ns/calls/errs）
	// 与 l7_flow_log（start_time/response_duration/request_resource/response_code）两路查询。
	rows := `{"time":"2026-08-15 10:00:00","src":"svc-a","src_ns":"obs","dst":"svc-b","dst_ns":"obs","calls":5,"errs":1,"start_time":"2026-08-15 10:00:00.000000","response_duration":1000,"request_resource":"/api/orders","response_code":200}`
	host, port := mockDFCH(t, rows)

	red := &fakeREDMetric{}
	s := NewDeepFlowSyncer(host, port, "cluster-x", nil, nil, nil, red)
	s.sampleRate = 1.0
	if err := s.Sync(); err != nil {
		t.Fatalf("Sync (legacy disabled, all CH writers nil): %v", err)
	}
	if len(red.calls) == 0 {
		t.Fatal("expected RED accumulation in Sync() when legacy writers are nil")
	}
	if red.calls[0] != "cluster-x|svc-b|ok" {
		t.Errorf("RED call = %q, want cluster-x|svc-b|ok", red.calls[0])
	}
}
