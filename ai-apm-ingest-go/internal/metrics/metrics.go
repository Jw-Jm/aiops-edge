package metrics

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// maxServiceREDEntries 服务 RED 计数器条数上限，防止服务基数极端增长导致内存无界。
const maxServiceREDEntries = 100000

// Metrics 记录 ingest 自身的运行时指标，暴露为 Prometheus 文本格式。
// 生产上可被 vmalert/prometheus 抓取用于采集健康度告警。
type Metrics struct {
	spansReceived    atomic.Int64 // 累计接收 span
	spansWritten     atomic.Int64 // 累计成功写入 ClickHouse 的 span
	spansFailed      atomic.Int64 // 累计写入失败（已进重试队列）
	spansDropped     atomic.Int64 // 累计因背压丢弃的 span（缓冲满，H3）
	otlpGRPCReceived atomic.Int64
	otlpGRPCAccepted atomic.Int64
	otlpGRPCRejected atomic.Int64
	otlpGRPCFailed   atomic.Int64
	logsReceived     atomic.Int64
	logsDropped      atomic.Int64 // 累计因背压丢弃的日志（缓冲满，H3）
	metricsWritten   atomic.Int64
	metricsDropped   atomic.Int64 // 累计因背压丢弃的指标/拓扑边批次（缓冲满，H3）
	edgesWritten     atomic.Int64
	reqTotal         atomic.Int64 // 接收请求总数
	reqRejected      atomic.Int64 // 因鉴权/限流拒绝的请求
	lastWriteOk      atomic.Int64 // 最近一次成功写入时间戳(秒)
	lastWriteFail    atomic.Int64

	// 服务 RED 标签化计数器（service → 累计值），供 vmagent 抓取进 VictoriaMetrics。
	redMu      sync.Mutex
	serviceRED map[string]*serviceREDEntry
}

// serviceREDEntry 单个 (cluster, service) 的累计 RED 值。
type serviceREDEntry struct {
	cluster string // 所属 k8s 环境/集群，多环境接入时区分
	reqs    uint64
	errs    uint64
	durSum  float64 // 秒
	durCnt  uint64
}

// New 创建 Metrics 实例。
func New() *Metrics {
	m := &Metrics{}
	m.lastWriteOk.Store(time.Now().Unix())
	m.serviceRED = make(map[string]*serviceREDEntry)
	return m
}

// AddServiceRED 累加一个服务的请求/错误/耗时（默认 cluster="default"）。
// durationNs 为纳秒，内部转为秒。兼容 OTLP 单环境路径。
func (m *Metrics) AddServiceRED(service string, isError bool, durationNs uint64) {
	m.AddServiceREDForCluster("default", service, isError, durationNs)
}

// AddServiceREDForCluster 按 (cluster, service) 累加服务 RED，多 k8s 环境接入时用 cluster 区分。
func (m *Metrics) AddServiceREDForCluster(cluster, service string, isError bool, durationNs uint64) {
	if cluster == "" {
		cluster = "default"
	}
	if service == "" {
		service = "unknown"
	}
	m.redMu.Lock()
	defer m.redMu.Unlock()
	key := cluster + "\x00" + service
	e, ok := m.serviceRED[key]
	if !ok {
		// 容量保护：超过 maxServiceREDEntries 时重建 map（丢弃全部已聚合值，作为
		// Prometheus counter 会短暂归零但持续运行后恢复，避免极端服务基数导致 OOM）
		if maxServiceREDEntries > 0 && len(m.serviceRED) >= maxServiceREDEntries {
			m.serviceRED = make(map[string]*serviceREDEntry)
		}
		e = &serviceREDEntry{cluster: cluster}
		m.serviceRED[key] = e
	}
	e.reqs++
	if isError {
		e.errs++
	}
	e.durSum += float64(durationNs) / 1e9
	e.durCnt++
}

// ResetServiceRED 清空服务 RED 计数器（周期性清零，配合 rate 使用）。
func (m *Metrics) ResetServiceRED() {
	m.redMu.Lock()
	defer m.redMu.Unlock()
	m.serviceRED = make(map[string]*serviceREDEntry)
}

func (m *Metrics) IncSpansReceived()        { m.spansReceived.Add(1) }
func (m *Metrics) AddSpansReceived(n int64) { m.spansReceived.Add(n) }
func (m *Metrics) AddSpansWritten(n int64) {
	m.spansWritten.Add(n)
	m.lastWriteOk.Store(time.Now().Unix())
}
func (m *Metrics) IncSpansFailed()             { m.spansFailed.Add(1); m.lastWriteFail.Store(time.Now().Unix()) }
func (m *Metrics) AddSpansDropped(n int64)     { m.spansDropped.Add(n) }
func (m *Metrics) IncOTLPGRPCReceived()        { m.otlpGRPCReceived.Add(1) }
func (m *Metrics) AddOTLPGRPCAccepted(n int64) { m.otlpGRPCAccepted.Add(n) }
func (m *Metrics) IncOTLPGRPCRejected()        { m.otlpGRPCRejected.Add(1) }
func (m *Metrics) IncOTLPGRPCFailed()          { m.otlpGRPCFailed.Add(1) }
func (m *Metrics) IncLogsReceived()            { m.logsReceived.Add(1) }
func (m *Metrics) AddLogsDropped(n int64)      { m.logsDropped.Add(n) }
func (m *Metrics) AddMetricsWritten(n int64)   { m.metricsWritten.Add(n) }
func (m *Metrics) AddMetricsDropped(n int64)   { m.metricsDropped.Add(n) }
func (m *Metrics) AddEdgesWritten(n int64)     { m.edgesWritten.Add(n) }
func (m *Metrics) IncReqTotal()                { m.reqTotal.Add(1) }
func (m *Metrics) IncReqRejected()             { m.reqRejected.Add(1) }

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
# HELP ai_ingest_spans_dropped_total Total spans dropped due to backpressure (buffer full).
# TYPE ai_ingest_spans_dropped_total counter
ai_ingest_spans_dropped_total %d
# HELP ai_ingest_otlp_grpc_batches_received_total OTLP gRPC trace batches received.
# TYPE ai_ingest_otlp_grpc_batches_received_total counter
ai_ingest_otlp_grpc_batches_received_total %d
# HELP ai_ingest_otlp_grpc_spans_accepted_total OTLP gRPC spans durably accepted.
# TYPE ai_ingest_otlp_grpc_spans_accepted_total counter
ai_ingest_otlp_grpc_spans_accepted_total %d
# HELP ai_ingest_otlp_grpc_batches_rejected_total OTLP gRPC batches rejected by validation or tenant auth.
# TYPE ai_ingest_otlp_grpc_batches_rejected_total counter
ai_ingest_otlp_grpc_batches_rejected_total %d
# HELP ai_ingest_otlp_grpc_batches_failed_total OTLP gRPC batches rejected because the platform sink failed.
# TYPE ai_ingest_otlp_grpc_batches_failed_total counter
ai_ingest_otlp_grpc_batches_failed_total %d
# HELP ai_ingest_logs_received_total Total logs received.
# TYPE ai_ingest_logs_received_total counter
ai_ingest_logs_received_total %d
# HELP ai_ingest_logs_dropped_total Total logs dropped due to backpressure (buffer full).
# TYPE ai_ingest_logs_dropped_total counter
ai_ingest_logs_dropped_total %d
# HELP ai_ingest_metrics_written_total Total service metrics written.
# TYPE ai_ingest_metrics_written_total counter
ai_ingest_metrics_written_total %d
# HELP ai_ingest_metrics_dropped_total Total metrics/topology batches dropped due to backpressure (buffer full).
# TYPE ai_ingest_metrics_dropped_total counter
ai_ingest_metrics_dropped_total %d
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
		m.spansReceived.Load(), m.spansWritten.Load(), m.spansFailed.Load(), m.spansDropped.Load(),
		m.otlpGRPCReceived.Load(), m.otlpGRPCAccepted.Load(), m.otlpGRPCRejected.Load(), m.otlpGRPCFailed.Load(),
		m.logsReceived.Load(), m.logsDropped.Load(),
		m.metricsWritten.Load(), m.metricsDropped.Load(), m.edgesWritten.Load(),
		m.reqTotal.Load(), m.reqRejected.Load(),
		m.lastWriteOk.Load(), m.lastWriteFail.Load()) + m.serviceREDSnapshot()
}

// serviceREDSnapshot 生成服务 RED 指标（Prometheus 文本格式）。
func (m *Metrics) serviceREDSnapshot() string {
	m.redMu.Lock()
	defer m.redMu.Unlock()
	var b strings.Builder
	b.WriteString("# HELP service_requests_total Total requests per service.\n")
	b.WriteString("# TYPE service_requests_total counter\n")
	b.WriteString("# HELP service_errors_total Total errors per service.\n")
	b.WriteString("# TYPE service_errors_total counter\n")
	b.WriteString("# HELP service_request_duration_seconds_sum Sum of request duration in seconds per service.\n")
	b.WriteString("# TYPE service_request_duration_seconds_sum counter\n")
	b.WriteString("# HELP service_request_duration_seconds_count Count of requests per service.\n")
	b.WriteString("# TYPE service_request_duration_seconds_count counter\n")
	for key, e := range m.serviceRED {
		// key = cluster+"\x00"+service
		parts := strings.Split(key, "\x00")
		svc := "unknown"
		if len(parts) == 2 {
			svc = parts[1]
		}
		b.WriteString(fmt.Sprintf("service_requests_total{cluster=%q, service=%q} %d\n", e.cluster, svc, e.reqs))
		b.WriteString(fmt.Sprintf("service_errors_total{cluster=%q, service=%q} %d\n", e.cluster, svc, e.errs))
		b.WriteString(fmt.Sprintf("service_request_duration_seconds_sum{cluster=%q, service=%q} %.9f\n", e.cluster, svc, e.durSum))
		b.WriteString(fmt.Sprintf("service_request_duration_seconds_count{cluster=%q, service=%q} %d\n", e.cluster, svc, e.durCnt))
	}
	return b.String()
}
