package metrics

import (
	"fmt"
	"sync/atomic"
	"time"
)

// Metrics 记录 ingest 自身的运行时指标，暴露为 Prometheus 文本格式。
// 生产上可被 vmalert/prometheus 抓取用于采集健康度告警。
type Metrics struct {
	spansReceived  atomic.Int64 // 累计接收 span
	spansWritten   atomic.Int64 // 累计成功写入 ClickHouse 的 span
	spansFailed    atomic.Int64 // 累计写入失败（已进重试队列）
	logsReceived   atomic.Int64
	metricsWritten atomic.Int64
	edgesWritten   atomic.Int64
	reqTotal       atomic.Int64 // 接收请求总数
	reqRejected    atomic.Int64 // 因鉴权/限流拒绝的请求
	lastWriteOk    atomic.Int64 // 最近一次成功写入时间戳(秒)
	lastWriteFail  atomic.Int64
}

// New 创建 Metrics 实例。
func New() *Metrics {
	m := &Metrics{}
	m.lastWriteOk.Store(time.Now().Unix())
	return m
}

func (m *Metrics) IncSpansReceived()   { m.spansReceived.Add(1) }
func (m *Metrics) AddSpansReceived(n int64) { m.spansReceived.Add(n) }
func (m *Metrics) AddSpansWritten(n int64)  { m.spansWritten.Add(n); m.lastWriteOk.Store(time.Now().Unix()) }
func (m *Metrics) IncSpansFailed()          { m.spansFailed.Add(1); m.lastWriteFail.Store(time.Now().Unix()) }
func (m *Metrics) IncLogsReceived()         { m.logsReceived.Add(1) }
func (m *Metrics) AddMetricsWritten(n int64){ m.metricsWritten.Add(n) }
func (m *Metrics) AddEdgesWritten(n int64)  { m.edgesWritten.Add(n) }
func (m *Metrics) IncReqTotal()             { m.reqTotal.Add(1) }
func (m *Metrics) IncReqRejected()          { m.reqRejected.Add(1) }

// Snapshot 生成 Prometheus 文本输出。
func (m *Metrics) Snapshot() string {
	return fmt.Sprintf(`# HELP ai_ingest_spans_received_total Total spans received.
# TYPE ai_ingest_spans_received_total counter
ai_ingest_spans_received_total %d
# HELP ai_ingest_spans_written_total Total spans written to ClickHouse.
# TYPE ai_ingest_spans_written_total counter
ai_ingest_spans_written_total %d
# HELP ai_ingest_spans_failed_total Total spans failed to write (in retry queue).
# TYPE ai_ingest_spans_failed_total counter
ai_ingest_spans_failed_total %d
# HELP ai_ingest_logs_received_total Total logs received.
# TYPE ai_ingest_logs_received_total counter
ai_ingest_logs_received_total %d
# HELP ai_ingest_metrics_written_total Total service metrics written.
# TYPE ai_ingest_metrics_written_total counter
ai_ingest_metrics_written_total %d
# HELP ai_ingest_edges_written_total Total topology edges written.
# TYPE ai_ingest_edges_written_total counter
ai_ingest_edges_written_total %d
# HELP ai_ingest_requests_total Total HTTP requests.
# TYPE ai_ingest_requests_total counter
ai_ingest_requests_total %d
# HELP ai_ingest_requests_rejected_total Total requests rejected (auth/ratelimit).
# TYPE ai_ingest_requests_rejected_total counter
ai_ingest_requests_rejected_total %d
# HELP ai_ingest_last_write_ok_time Last successful ClickHouse write time.
# TYPE ai_ingest_last_write_ok_time gauge
ai_ingest_last_write_ok_time %d
# HELP ai_ingest_last_write_fail_time Last failed ClickHouse write time.
# TYPE ai_ingest_last_write_fail_time gauge
ai_ingest_last_write_fail_time %d
`,
		m.spansReceived.Load(), m.spansWritten.Load(), m.spansFailed.Load(),
		m.logsReceived.Load(), m.metricsWritten.Load(), m.edgesWritten.Load(),
		m.reqTotal.Load(), m.reqRejected.Load(),
		m.lastWriteOk.Load(), m.lastWriteFail.Load())
}
