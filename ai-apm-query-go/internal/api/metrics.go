package api

import (
	"fmt"
	"net/http"
	"runtime"
	"time"
)

// prometheusMetrics 输出 query-api 自身 Prometheus 文本格式指标（标准库实现，零依赖）。
// 用于自监控：VictoriaMetrics 抓取 /metrics 纳入平台自身监控面（方案 9.3）。
var procStartTime = time.Now()

// PrometheusMetrics handles GET /metrics (免鉴权，供 VM 抓取)。
func (h *Handler) PrometheusMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "# HELP go_goroutines Number of goroutines that currently exist.\n")
	fmt.Fprintf(w, "# TYPE go_goroutines gauge\n")
	fmt.Fprintf(w, "go_goroutines %d\n", runtime.NumGoroutine())
	fmt.Fprintf(w, "# HELP go_memstats_alloc_bytes Number of bytes allocated and still in use.\n")
	fmt.Fprintf(w, "# TYPE go_memstats_alloc_bytes gauge\n")
	fmt.Fprintf(w, "go_memstats_alloc_bytes %d\n", m.Alloc)
	fmt.Fprintf(w, "# HELP go_memstats_heap_inuse_bytes Number of heap bytes that are in use.\n")
	fmt.Fprintf(w, "# TYPE go_memstats_heap_inuse_bytes gauge\n")
	fmt.Fprintf(w, "go_memstats_heap_inuse_bytes %d\n", m.HeapInuse)
	fmt.Fprintf(w, "# HELP go_memstats_sys_bytes Number of bytes obtained from system.\n")
	fmt.Fprintf(w, "# TYPE go_memstats_sys_bytes gauge\n")
	fmt.Fprintf(w, "go_memstats_sys_bytes %d\n", m.Sys)
	fmt.Fprintf(w, "# HELP process_uptime_seconds Seconds since process start.\n")
	fmt.Fprintf(w, "# TYPE process_uptime_seconds gauge\n")
	fmt.Fprintf(w, "process_uptime_seconds %d\n", int64(time.Since(procStartTime).Seconds()))
	fmt.Fprintf(w, "# HELP aiops_build_info AIOps query-api build info.\n")
	fmt.Fprintf(w, "# TYPE aiops_build_info gauge\n")
	fmt.Fprintf(w, "aiops_build_info{service=\"query-api\",go_version=%q} 1\n", runtime.Version())
	// 28.2 #19：control-plane 运行指标（Lease/Commit/Outbox/Tool/Replay/Recovery/SSE/LLM/Alert/correlation）。
	cp.writeControlPlaneMetrics(w)
}
