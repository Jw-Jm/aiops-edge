package api

import (
	"fmt"
	"os"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/query"
)

// ─────────────────────────────────────────────────────────────────────────────
// P6.3.3 / P6.4.0 QUERY_READER_MODE wiring。
//
// QUERY_READER_MODE 控制 reader 路由：
//   - legacy → 读 ClickHouse（transition path，未切生产）
//   - new   → Raw Metrics 走 VictoriaMetrics、Raw Logs 走 VictoriaLogs（SoT）
//   - missing → default=legacy，仅 transition period 允许
//   - 显式提供但非法 → configuration error → startup FAIL
//
// 关键安全边界（P6.4.0）：非法显式值绝不能静默回 legacy。否则误写 QUERY_READER_MODE=NEW
// （大小写/拼写错误）会静默走旧 reader，造成"new writer ACTIVE / old reader ACTIVE"的
// 原子切换危险状态。因此显式非法值必须拒绝启动。
//
// 该开关为 TRANSITION_ONLY，REMOVE_BEFORE_GATE6：Gate 6 后不能留下永久 legacy/fallback 开关。
// ─────────────────────────────────────────────────────────────────────────────

// readerModeFromEnv 解析 QUERY_READER_MODE；missing→legacy（transition），显式非法→error（startup FAIL）。
func readerModeFromEnv() (query.ReaderMode, error) {
	raw := os.Getenv("QUERY_READER_MODE")
	if raw == "" {
		return query.ModeLegacy, nil // default = legacy，仅 transition period 允许
	}
	mode, err := query.ParseReaderMode(raw)
	if err != nil {
		return query.ModeLegacy, fmt.Errorf("QUERY_READER_MODE=%q invalid (must be legacy or new): %w", raw, err)
	}
	return mode, nil
}

// newVMRReaderFromEnv 构造 VictoriaMetrics reader（new mode Raw Metrics SoT）。
func newVMRReaderFromEnv() *query.VictoriaMetricsReader {
	url := os.Getenv("VICTORIA_METRICS_URL")
	if url == "" {
		url = "http://victoria-metrics.observability.svc.cluster.local:8428"
	}
	return query.NewVictoriaMetricsReader(url, nil)
}

// normalizeVLogsEndpoint 规范化 VictoriaLogs endpoint 为 reader 所需 base URL。
// VICTORIA_LOGS_URL 会被 data_sync.go StartLogShipper 复用作写入 URL（带 /insert/jsonline），
// 而 VLogsReader.Search 会追加 /select/logsql/query；若不去掉后缀将得到错误查询路径
// "/insert/jsonline/select/logsql/query"（400）。因此 reader 必须用 base URL。
func normalizeVLogsEndpoint(raw string) string {
	const suffix = "/insert/jsonline"
	for strings.HasSuffix(raw, suffix) || strings.HasSuffix(raw, suffix+"/") {
		raw = strings.TrimSuffix(strings.TrimSuffix(raw, "/"), suffix)
	}
	return raw
}

// newVLogsReaderFromEnv 构造 VictoriaLogs reader（new mode Raw Logs SoT）。
func newVLogsReaderFromEnv() *query.VLogsReader {
	url := os.Getenv("VICTORIA_LOGS_URL")
	if url == "" {
		url = "http://victoria-logs.observability.svc.cluster.local:9428"
	}
	return query.NewVLogsReader(normalizeVLogsEndpoint(url), nil)
}
