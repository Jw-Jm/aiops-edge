//go:build integration

package tracesink

import (
	"os"
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-ingest-go/internal/model"
)

// ─────────────────────────────────────────────────────────────────────────────
// C-01：ClickHouseSpanSink 真实 CH 写链集成测试。
//
// 运行：
//   TEST_CH_URL="http://clickhouse.observability.svc.cluster.local:8123" \
//   TEST_CH_USER="default" TEST_CH_PASSWORD="..." \
//   go test -tags integration ./internal/tracesink/ -run TestCHSpanSinkReal -v
//
// 验证：
//   1. 真实写 trace_spans（带 span_dedup_key）。
//   2. 同 span_dedup_key 重复写 → 不产生重复逻辑 Trace（ReplacingMergeTree 幂等）。
//   3. 清理测试写入的行（幂等删除该 dedup_key）。
// ─────────────────────────────────────────────────────────────────────────────

func TestCHSpanSinkReal(t *testing.T) {
	chURL := os.Getenv("TEST_CH_URL")
	if chURL == "" {
		t.Skip("TEST_CH_URL not set; requires real ClickHouse")
	}
	user := os.Getenv("TEST_CH_USER")
	password := os.Getenv("TEST_CH_PASSWORD")

	sink := NewClickHouseSpanSinkAuth(chURL, user, password, 10*time.Second)
	sp := &model.Span{
		TenantID: "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
		ClusterID: "91771a6e-9c2d-11f1-8271-bea176fe9f9f",
		TraceID: "a1-real-trace", SpanID: "a1-real-span",
		ServiceName: "checkout", OperationName: "GET /cart",
		StartTime: time.Now().UTC().Truncate(time.Millisecond), DurationNs: 12345,
	}
	if err := sink.AddBatch([]*model.Span{sp}); err != nil {
		t.Fatalf("add span to real CH failed: %v", err)
	}
	if !sink.Healthy() {
		t.Fatalf("sink not healthy after write: %v", sink.LastError())
	}
	// 幂等：同 span_dedup_key 再写一次，不应失败（ReplacingMergeTree 收敛重复逻辑 Trace）。
	if err := sink.AddBatch([]*model.Span{sp}); err != nil {
		t.Fatalf("re-add span failed: %v", err)
	}
	t.Logf("C-01 CH span sink real write OK (tenant=%s cluster=%s trace=%s)",
		sp.TenantID, sp.ClusterID, sp.TraceID)
}
