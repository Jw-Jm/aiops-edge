//go:build integration

package tracesink

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-ingest-go/internal/model"
)

// ─────────────────────────────────────────────────────────────────────────────
// C-01 + P1：ClickHouseSpanSink 真实 CH 写链集成测试。
//
// 运行：
//   TEST_CH_URL="http://clickhouse.observability.svc.cluster.local:8123" \
//   TEST_CH_USER="default" TEST_CH_PASSWORD="..." \
//   go test -tags integration ./internal/tracesink/ -run TestCHSpanSink -v
//
// 验证（报告 §16 / P1 "Trace duplicate/failure/tenant E2E"）：
//   1. 真实写 trace_spans（带 span_dedup_key）。
//   2. 去重：同 span_dedup_key 重复写 → 不产生重复逻辑 Trace（ReplacingMergeTree 幂等收敛）。
//   3. 故障恢复：sink 连错地址 → LastError 记录 + Healthy=false；恢复正确地址 → Healthy。
//   4. 租户隔离：不同 tenant 相同 trace_id/span_id → span_dedup_key 不同，互不覆盖。
//   5. 清理测试写入的行（幂等删除该 dedup_key）。
// ─────────────────────────────────────────────────────────────────────────────

func chEnv(t *testing.T) (string, string, string) {
	t.Helper()
	chURL := os.Getenv("TEST_CH_URL")
	if chURL == "" {
		t.Skip("TEST_CH_URL not set; requires real ClickHouse")
	}
	return chURL, os.Getenv("TEST_CH_USER"), os.Getenv("TEST_CH_PASSWORD")
}

func chQuery(t *testing.T, chURL, user, password, query string) string {
	t.Helper()
	return chHTTP(t, chURL, user, password, query, http.MethodGet)
}

func chMutate(t *testing.T, chURL, user, password, query string) string {
	t.Helper()
	return chHTTP(t, chURL, user, password, query, http.MethodPost)
}

func chHTTP(t *testing.T, chURL, user, password, query, method string) string {
	t.Helper()
	// 修改类查询（ALTER DELETE）必须用 POST（GET 在 CH 是 readonly）。
	req, err := http.NewRequestWithContext(context.Background(), method,
		chURL+"/?query="+urlQueryEncode(query), nil)
	if err != nil {
		t.Fatalf("query req: %v", err)
	}
	if user != "" {
		req.SetBasicAuth(user, password)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ch query: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		t.Fatalf("ch query status %d: %s", resp.StatusCode, string(body))
	}
	return strings.TrimSpace(string(body))
}

func TestCHSpanSinkReal(t *testing.T) {
	chURL, user, password := chEnv(t)
	sink := NewClickHouseSpanSinkAuth(chURL, user, password, 10*time.Second)
	sp := &model.Span{
		TenantID: "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
		ClusterID: "91771a6e-9c2d-11f1-8271-bea176fe9f9f",
		TraceID: "a1-real-trace", SpanID: "a1-real-span",
		ServiceName: "checkout", OperationName: "GET /cart",
		StartTime: time.Now().UTC().Truncate(time.Millisecond), DurationNs: 12345,
	}
	dedup := spanDedupKey(sp)
	// 清理可能残留的旧测试行（幂等）。
	chMutate(t, chURL, user, password, fmt.Sprintf(
		`ALTER TABLE observability.trace_spans DELETE WHERE span_dedup_key='%s'`, dedup))
	defer chMutate(t, chURL, user, password, fmt.Sprintf(
		`ALTER TABLE observability.trace_spans DELETE WHERE span_dedup_key='%s'`, dedup))

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
	// 去重验证：写后查询该 dedup_key 只有 1 行（FINAL 语义去重）。
	cnt := chQuery(t, chURL, user, password, fmt.Sprintf(
		`SELECT count() FROM (SELECT * FROM observability.trace_spans FINAL WHERE span_dedup_key='%s')`, dedup))
	if cnt != "1" {
		t.Fatalf("dedup: expected 1 logical row after duplicate write, got %q", cnt)
	}
	t.Logf("C-01/P1 dedup OK (tenant=%s trace=%s rows=%s)", sp.TenantID, sp.TraceID, cnt)
}

// TestCHSpanSinkReal_FailureRecovery 验证 sink 故障时 fail-closed + 恢复后重新健康。
func TestCHSpanSinkReal_FailureRecovery(t *testing.T) {
	chURL, user, password := chEnv(t)
	// 故意用错误地址 → 连接失败。
	badSink := NewClickHouseSpanSinkAuth("http://127.0.0.1:1", user, password, 2*time.Second)
	sp := &model.Span{
		TenantID: "t-fail", ClusterID: "c-fail", TraceID: "tr-fail",
		SpanID: "sp-fail", ServiceName: "svc", OperationName: "op",
		StartTime: time.Now().UTC(), DurationNs: 1,
	}
	if err := badSink.AddBatch([]*model.Span{sp}); err == nil {
		t.Fatalf("expected failure on bad CH URL")
	}
	if badSink.Healthy() {
		t.Fatalf("sink must be unhealthy after failed write")
	}
	if badSink.LastError() == nil {
		t.Fatalf("LastError must be recorded after failure")
	}
	// 恢复正确地址 → 重新健康。
	goodSink := NewClickHouseSpanSinkAuth(chURL, user, password, 10*time.Second)
	_ = chMutate(t, chURL, user, password, `ALTER TABLE observability.trace_spans DELETE WHERE tenant_id='t-fail'`)
	defer chMutate(t, chURL, user, password, `ALTER TABLE observability.trace_spans DELETE WHERE tenant_id='t-fail'`)
	if err := goodSink.AddBatch([]*model.Span{sp}); err != nil {
		t.Fatalf("recover write failed: %v", err)
	}
	if !goodSink.Healthy() {
		t.Fatalf("sink must recover healthy after successful write")
	}
	t.Logf("C-01/P1 failure-recovery OK")
}

// TestCHSpanSinkReal_TenantIsolation 验证不同 tenant 相同 trace_id/span_id 各自独立。
func TestCHSpanSinkReal_TenantIsolation(t *testing.T) {
	chURL, user, password := chEnv(t)
	sink := NewClickHouseSpanSinkAuth(chURL, user, password, 10*time.Second)
	// 同一 trace_id/span_id/start，仅 tenant 不同 → span_dedup_key 必须不同。
	sp1 := &model.Span{TenantID: "ten-a", ClusterID: "c1", TraceID: "same-trace", SpanID: "same-span",
		ServiceName: "svc", OperationName: "op", StartTime: time.Unix(1700000000, 0).UTC(), DurationNs: 1}
	sp2 := &model.Span{TenantID: "ten-b", ClusterID: "c1", TraceID: "same-trace", SpanID: "same-span",
		ServiceName: "svc", OperationName: "op", StartTime: time.Unix(1700000000, 0).UTC(), DurationNs: 1}
	d1, d2 := spanDedupKey(sp1), spanDedupKey(sp2)
	if d1 == d2 {
		t.Fatalf("tenant isolation: dedup keys must differ across tenants")
	}
	for _, d := range []string{d1, d2} {
		chMutate(t, chURL, user, password, fmt.Sprintf(
			`ALTER TABLE observability.trace_spans DELETE WHERE span_dedup_key='%s'`, d))
		defer chMutate(t, chURL, user, password, fmt.Sprintf(
			`ALTER TABLE observability.trace_spans DELETE WHERE span_dedup_key='%s'`, d))
	}
	if err := sink.AddBatch([]*model.Span{sp1, sp2}); err != nil {
		t.Fatalf("write both tenants failed: %v", err)
	}
	cnt1 := chQuery(t, chURL, user, password, fmt.Sprintf(
		`SELECT count() FROM (SELECT * FROM observability.trace_spans FINAL WHERE span_dedup_key='%s')`, d1))
	cnt2 := chQuery(t, chURL, user, password, fmt.Sprintf(
		`SELECT count() FROM (SELECT * FROM observability.trace_spans FINAL WHERE span_dedup_key='%s')`, d2))
	if cnt1 != "1" || cnt2 != "1" {
		t.Fatalf("tenant isolation: expected both tenants present (got %s,%s)", cnt1, cnt2)
	}
	t.Logf("C-01/P1 tenant isolation OK (dedup1=%s dedup2=%s)", d1[:8], d2[:8])
}
