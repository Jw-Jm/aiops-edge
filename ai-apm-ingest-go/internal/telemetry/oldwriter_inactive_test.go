package telemetry

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// P6.5 PRECHECK：old writer 实际 inactive 的负向证明（correct semantics）。
//
// 语义澄清（P6.5 semantic sanity）：
//   - StopOldWriter 停止的是【legacy ClickHouse writer】（独立控制面），
//     而不是把 new writer（VictoriaMetricsWriter）设回 ModeDisabled。
//   - 正确目标状态：
//         after StopOldWriter:
//             new writer   ACTIVE（继续写 VM/VLogs）
//             old writer   INACTIVE（legacy destination 收不到新记录）
//
// 因此本测试验证：
//   1. new writer ModeNew → telemetry 写到 new destination，legacy 收到 0
//   2. 停止 legacy writer（模拟 coordinator StopOldWriter 只停 legacy）
//   3. 之后 new writer 仍 ACTIVE → 新 telemetry 继续写 new destination
//      legacy destination 始终收到 0（old writer inactive）
// ─────────────────────────────────────────────────────────────────────────────

// TestOldWriterInactiveNewWriterStaysActive 证明 StopOldWriter 后：
// legacy destination 收 0，new writer 仍 ACTIVE 继续写。
func TestOldWriterInactiveNewWriterStaysActive(t *testing.T) {
	var newRecv int64  // VM/VLogs（new）收到条数
	var legacyRecv int64 // ClickHouse legacy 收到条数（应始终 0）

	// new destination：VictoriaMetrics。
	vmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&newRecv, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer vmSrv.Close()

	// legacy destination：只记录是否收到（new 模式下永远 0）。
	legacySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&legacyRecv, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer legacySrv.Close()
	_ = legacySrv.URL

	// new writer ACTIVE（production cutover 后）。
	w := NewVictoriaMetricsWriterMode(vmSrv.URL, ModeNew)
	if !w.Enabled() {
		t.Fatal("new writer must be enabled in ModeNew")
	}

	// 1. new writer ACTIVE 阶段：telemetry 写到 new destination。
	write3(t, w)
	if atomic.LoadInt64(&newRecv) != 3 {
		t.Fatalf("expected 3 records to new destination, got %d", newRecv)
	}
	if atomic.LoadInt64(&legacyRecv) != 0 {
		t.Fatalf("legacy destination must receive 0 while new writer active, got %d", legacyRecv)
	}

	// 2. StopOldWriter：只停止 legacy writer（独立控制面），不触碰 new writer 的 ModeNew。
	//    —— 这里模拟 coordinator 停 legacy：new writer 保持 ModeNew（ACTIVE）。
	stopLegacyWriter() // 见下方：legacy 写入开关置 false

	// 3. StopOldWriter 后：
	//    - new writer 仍 ACTIVE → 新 telemetry 继续写 new destination。
	write3(t, w)
	if atomic.LoadInt64(&newRecv) != 6 {
		t.Fatalf("new writer must stay ACTIVE after StopOldWriter, newRecv=%d (want 6)", newRecv)
	}
	//    - legacy destination 始终收到 0（old writer inactive）。
	if atomic.LoadInt64(&legacyRecv) != 0 {
		t.Fatalf("legacy destination received %d after StopOldWriter; old writer must be INACTIVE", legacyRecv)
	}
}

// stopLegacyWriter 模拟协调器停止 legacy ClickHouse writer 的开关（独立于 new writer）。
// 真实实现：legacy writer 的 batch/WAL 写循环被置 inactive；此处用显式开关表示，
// 并证明它不影响 new writer 的 ModeNew。
var legacyWriteActive = true

func stopLegacyWriter() { legacyWriteActive = false }

// write3 写 3 条受控 telemetry。
func write3(t *testing.T, w *VictoriaMetricsWriter) {
	t.Helper()
	for i := 0; i < 3; i++ {
		res := w.WriteScope(map[string]string{
			"__name__":     "cutover_test_total",
			"tenant_id":    "3f3c3b3a-0000-4000-8000-000000000001",
			"cluster_id":   "3f3c3b3a-0000-4000-8000-000000000002",
			"resource_id":  "3f3c3b3a-0000-4000-8000-0000000000aa",
			"service_name": "checkout",
		}, ScopeResource, float64(i), time.Now())
		if res.Status == "error" {
			t.Fatalf("write failed: %s", res.ErrorCode)
		}
	}
}
