package pipeline

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/observability-platform/ai-apm-ingest-go/internal/model"
)

const slowThresholdNs = 500_000_000 // 500ms

// metricsKey is the composite key for RED metrics aggregation
type metricsKey struct {
	tenantID      string
	serviceName   string
	callerService string
	timeBucket    string // time truncated to minute as string
}

// metricsValue holds aggregated RED metrics values
type metricsValue struct {
	callCount     uint64
	errorCount    uint64
	durationSumNs uint64
	durationCount uint64
}

// edgeKey is the composite key for topology edge aggregation
type edgeKey struct {
	tenantID      string
	sourceService string
	targetService string
	timeBucket    string
}

// edgeValue holds aggregated topology edge values
type edgeValue struct {
	callCount     uint64
	errorCount    uint64
	durationSumNs uint64
	durationCount uint64
}

// Pipeline processes OTLP JSON and writes to sinks
type Pipeline struct {
	// writer/metricsWriter 为可选 span/edge sink（V9.3 Phase 14 依赖倒置后为中立接口）。
	// 传 nil（无 sink）合法，使用方必须按 nil 安全跳过。
	writer        SpanSink
	metricsWriter EdgeSink
	clusterID     string // 本 ingest 实例所属集群（多集群纳管打标；空默认 default）
	mu            sync.Mutex
	metricsAgg    map[metricsKey]*metricsValue
	edgesAgg      map[edgeKey]*edgeValue
	stopCh        chan struct{}
	doneCh        chan struct{}
	// onServiceMetric 可选回调：每累加一次服务调用时通知外部（用于喂 Prometheus 服务 RED）。
	onServiceMetric func(service string, isError bool, durationNs uint64)
	// onServiceMetricWithCluster is the canonical callback for multi-cluster
	// deployments. It preserves the immutable cluster identity assigned to the
	// ingest instance instead of falling back to a shared "default" label.
	onServiceMetricWithCluster func(cluster, service string, isError bool, durationNs uint64)
	// redSink 可选回调：flush 时把聚合的 RED 服务指标推给外部（P6.5 用于双写 VictoriaMetrics）。
	// 为空时跳过，不改变既有 Prometheus 行为。
	redSink func(m *model.ServiceMetric)
}

// SetOnServiceMetric 注册服务 RED 回调（可选，用于暴露服务指标到 /metrics）。
func (p *Pipeline) SetOnServiceMetric(fn func(service string, isError bool, durationNs uint64)) {
	p.onServiceMetric = fn
}

// SetOnServiceMetricWithCluster registers the production RED callback. The
// cluster argument is taken from Pipeline.SetClusterID and is never supplied
// by the caller payload.
func (p *Pipeline) SetOnServiceMetricWithCluster(fn func(cluster, service string, isError bool, durationNs uint64)) {
	p.onServiceMetricWithCluster = fn
}

// SetREDSink 注册 RED 服务指标聚合的写回调（可选；P6.5 new 链双写 VictoriaMetrics）。
// 回调在 flush 时按 (tenant, service, caller, minute) 聚合后调用一次。
func (p *Pipeline) SetREDSink(fn func(m *model.ServiceMetric)) {
	p.redSink = fn
}

// New creates a new Pipeline with the given optional span/edge sinks (may be nil).
func New(w SpanSink, mw EdgeSink) *Pipeline {
	p := &Pipeline{
		writer:        w,
		metricsWriter: mw,
		metricsAgg:    make(map[metricsKey]*metricsValue),
		edgesAgg:      make(map[edgeKey]*edgeValue),
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	go p.flushLoop()
	return p
}

// SetClusterID 设置本 ingest 实例所属集群 ID（多集群纳管打标）。
// Phase 5：不再把空 id 兜底为 default；空即保持为空，由调用方（cmd/ingest/main.go）
// 在启动时校验 CLUSTER_ID 为 canonical UUID 并 fail-closed。
func (p *Pipeline) SetClusterID(id string) {
	p.clusterID = id
}

// ClusterID returns the canonical cluster identity assigned to this ingest
// instance. It is used by protocol adapters that convert external telemetry
// into the shared internal model.
func (p *Pipeline) ClusterID() string {
	return p.clusterID
}

// flushLoop periodically flushes aggregated metrics and edges to the MetricsWriter
func (p *Pipeline) flushLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			p.flushMetrics()
			close(p.doneCh)
			return
		case <-ticker.C:
			p.flushMetrics()
		}
	}
}

// flushMetrics drains the in-memory aggregation maps and sends to MetricsWriter
func (p *Pipeline) flushMetrics() {
	p.mu.Lock()
	edges := p.edgesAgg
	metrics := p.metricsAgg
	p.metricsAgg = make(map[metricsKey]*metricsValue)
	p.edgesAgg = make(map[edgeKey]*edgeValue)
	p.mu.Unlock()

	// RED 服务指标聚合 → 外部 sink（P6.5 new 链双写 VictoriaMetrics；可选，不影响既有路径）。
	if p.redSink != nil {
		for k, v := range metrics {
			tb, _ := time.Parse("2006-01-02T15:04", k.timeBucket)
			p.redSink(&model.ServiceMetric{
				TenantID:      k.tenantID,
				ClusterID:     p.clusterID,
				ServiceName:   k.serviceName,
				CallerService: k.callerService,
				TimeBucket:    tb,
				CallCount:     v.callCount,
				ErrorCount:    v.errorCount,
				DurationSumNs: v.durationSumNs,
				DurationCount: v.durationCount,
				Date:          tb.Format("2006-01-02"),
			})
		}
	}

	for k, v := range edges {
		tb, _ := time.Parse("2006-01-02T15:04", k.timeBucket)
		date := tb.Format("2006-01-02")
		avgNs := uint64(0)
		if v.durationCount > 0 {
			avgNs = v.durationSumNs / v.durationCount
		}
		// B1：LEGACY_WRITER_ENABLED=false 时 metricsWriter 为 nil，跳过 CH 边写入
		// （new 链 RED 指标仍经 redSink 写入 VictoriaMetrics）。
		if p.metricsWriter != nil {
			p.metricsWriter.AddEdge(&model.TopologyEdge{
				TenantID:      k.tenantID,
				ClusterID:     p.clusterID,
				SourceService: k.sourceService,
				TargetService: k.targetService,
				TimeBucket:    tb,
				CallCount:     v.callCount,
				ErrorCount:    v.errorCount,
				AvgDurationNs: avgNs,
				Date:          date,
			})
		}
	}
}

// Close stops the flush loop and performs a final flush
func (p *Pipeline) Close() {
	close(p.stopCh)
	<-p.doneCh
}

// spanInfo holds parsed span data for metrics extraction
type spanInfo struct {
	traceID      string
	spanID       string
	parentSpanID string
	serviceName  string
	startTime    time.Time
	durationNs   uint64
	isError      uint8
}

// traceContext holds per-request trace data needed for parent-child resolution
type traceContext struct {
	spanIDToService map[string]string // spanID -> serviceName
	spanIDToCaller  map[string]string // spanID -> callerService (from parent)
	spanInfos       []*spanInfo
}

// ProcessOTLPTraces converts OTLP JSON request to internal spans, extracts metrics, and writes to ClickHouse
func (p *Pipeline) ProcessOTLPTraces(tenantID string, body []byte) (int, error) {
	var req model.OTLPTraceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return 0, fmt.Errorf("json unmarshal: %w", err)
	}

	spans := make([]*model.Span, 0)
	for _, rs := range req.ResourceSpans {
		resourceAttrs := extractAttributes(rs.Resource.Attributes)
		serviceName := resourceAttrs["service.name"]
		if serviceName == "" {
			serviceName = "unknown"
		}

		for _, ss := range rs.ScopeSpans {
			for _, s := range ss.Spans {
				span := p.convertSpan(tenantID, &s, serviceName, resourceAttrs)
				spans = append(spans, span)
			}
		}
	}

	return p.ProcessSpans(tenantID, spans)
}

// ProcessSpans persists already-converted spans and extracts the same RED and
// topology signals used by the JSON OTLP path. A DurableSpanSink is preferred
// when configured so protocol adapters can return a retryable error instead of
// acknowledging data that failed to reach the platform Trace SoT.
func (p *Pipeline) ProcessSpans(tenantID string, spans []*model.Span) (int, error) {
	if strings.TrimSpace(tenantID) == "" {
		return 0, fmt.Errorf("tenant id is required")
	}

	traceCtx := &traceContext{
		spanIDToService: make(map[string]string),
		spanIDToCaller:  make(map[string]string),
	}
	for _, span := range spans {
		if span == nil {
			return 0, fmt.Errorf("nil span")
		}
		if span.TenantID == "" {
			span.TenantID = tenantID
		}
		if span.TenantID != tenantID {
			return 0, fmt.Errorf("span tenant %q does not match request tenant %q", span.TenantID, tenantID)
		}
		if span.ClusterID == "" {
			span.ClusterID = p.clusterID
		}
		if span.ClusterID != p.clusterID {
			return 0, fmt.Errorf("span cluster %q does not match ingest cluster %q", span.ClusterID, p.clusterID)
		}
		traceCtx.spanInfos = append(traceCtx.spanInfos, &spanInfo{
			traceID:      span.TraceID,
			spanID:       span.SpanID,
			parentSpanID: span.ParentSpanID,
			serviceName:  span.ServiceName,
			startTime:    span.StartTime,
			durationNs:   span.DurationNs,
			isError:      span.IsError,
		})
		traceCtx.spanIDToService[span.SpanID] = span.ServiceName
	}

	if p.writer != nil && len(spans) > 0 {
		if durable, ok := p.writer.(DurableSpanSink); ok {
			if err := durable.AddBatch(spans); err != nil {
				return 0, fmt.Errorf("span sink batch: %w", err)
			}
		} else {
			for _, span := range spans {
				p.writer.Add(span)
			}
		}
	}

	p.extractMetrics(tenantID, traceCtx)
	log.Printf("Pipeline: processed %d spans for tenant %s", len(spans), tenantID)
	return len(spans), nil
}

// extractMetrics extracts RED metrics and topology edges from collected span infos
func (p *Pipeline) extractMetrics(tenantID string, ctx *traceContext) {
	// Build parent-to-child caller mapping
	// For each span, if it has a parentSpanID that exists in this trace, the parent's service is the caller
	for _, info := range ctx.spanInfos {
		if info.parentSpanID != "" {
			if callerService, ok := ctx.spanIDToService[info.parentSpanID]; ok {
				ctx.spanIDToCaller[info.spanID] = callerService
			}
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, info := range ctx.spanInfos {
		timeBucket := info.startTime.Truncate(time.Minute).Format("2006-01-02T15:04")

		// RED metrics: group by (tenant_id, service_name, caller_service, time_bucket_minute)
		callerService := ctx.spanIDToCaller[info.spanID]
		mk := metricsKey{
			tenantID:      tenantID,
			serviceName:   info.serviceName,
			callerService: callerService,
			timeBucket:    timeBucket,
		}
		mv, ok := p.metricsAgg[mk]
		if !ok {
			mv = &metricsValue{}
			p.metricsAgg[mk] = mv
		}
		mv.callCount++
		if info.isError == 1 {
			mv.errorCount++
		}
		mv.durationSumNs += info.durationNs
		mv.durationCount++

		// 服务 RED 注入：按 service 累计（喂 Prometheus /metrics 服务指标）
		if p.onServiceMetricWithCluster != nil {
			p.onServiceMetricWithCluster(p.clusterID, info.serviceName, info.isError == 1, info.durationNs)
		} else if p.onServiceMetric != nil {
			p.onServiceMetric(info.serviceName, info.isError == 1, info.durationNs)
		}

		// Topology edges: parent-child relationship
		if info.parentSpanID != "" {
			if sourceService, ok := ctx.spanIDToService[info.parentSpanID]; ok {
				ek := edgeKey{
					tenantID:      tenantID,
					sourceService: sourceService,
					targetService: info.serviceName,
					timeBucket:    timeBucket,
				}
				ev, ok := p.edgesAgg[ek]
				if !ok {
					ev = &edgeValue{}
					p.edgesAgg[ek] = ev
				}
				ev.callCount++
				if info.isError == 1 {
					ev.errorCount++
				}
				ev.durationSumNs += info.durationNs
				ev.durationCount++
			}
		}
	}
}

func (p *Pipeline) convertSpan(tenantID string, raw *struct {
	TraceID       string `json:"traceId"`
	SpanID        string `json:"spanId"`
	ParentSpanID  string `json:"parentSpanId"`
	Name          string `json:"name"`
	Kind          int    `json:"kind"`
	StartTimeUnix string `json:"startTimeUnixNano"`
	EndTimeUnix   string `json:"endTimeUnixNano"`
	Status        struct {
		Code int `json:"code"`
	} `json:"status"`
	Attributes []struct {
		Key   string      `json:"key"`
		Value interface{} `json:"value"`
	} `json:"attributes"`
}, serviceName string, resourceAttrs map[string]string) *model.Span {

	startNs, _ := strconv.ParseInt(raw.StartTimeUnix, 10, 64)
	endNs, _ := strconv.ParseInt(raw.EndTimeUnix, 10, 64)
	durationNs := uint64(0)
	if endNs > startNs {
		durationNs = uint64(endNs - startNs)
	}

	startTime := time.Unix(0, startNs).UTC()

	spanAttrs := extractAttributes(raw.Attributes)
	// merge: resource attrs first, span attrs override
	merged := make(map[string]string, len(resourceAttrs)+len(spanAttrs))
	for k, v := range resourceAttrs {
		merged[k] = v
	}
	for k, v := range spanAttrs {
		merged[k] = v
	}

	span := &model.Span{
		TenantID:      tenantID,
		ClusterID:     p.clusterID,
		TraceID:       raw.TraceID,
		SpanID:        raw.SpanID,
		ParentSpanID:  raw.ParentSpanID,
		ServiceName:   serviceName,
		OperationName: raw.Name,
		SpanKind:      model.KindMap[raw.Kind],
		StatusCode:    uint8(raw.Status.Code),
		StartTime:     startTime,
		DurationNs:    durationNs,
		Attributes:    merged,

		HTTPMethod:        merged["http.method"],
		HTTPURL:           merged["http.url"],
		DBSystem:          merged["db.system"],
		DBStatement:       merged["db.statement"],
		RPCSystem:         merged["rpc.system"],
		ServiceInstanceID: merged["service.instance.id"],
		K8sNamespace:      merged["k8s.namespace.name"],
		K8sPodName:        merged["k8s.pod.name"],

		IsSlow:  0,
		IsError: 0,
	}

	if raw.Status.Code == 2 { // STATUS_CODE_ERROR
		span.IsError = 1
	}
	if durationNs > slowThresholdNs {
		span.IsSlow = 1
	}

	if httpCode := merged["http.status_code"]; httpCode != "" {
		if code, err := strconv.Atoi(httpCode); err == nil {
			span.HTTPStatusCode = uint16(code)
			if code >= 400 {
				span.IsError = 1
			}
		}
	}

	return span
}

// extractAttributes converts OTel attribute list to flat map
func extractAttributes(attrs interface{}) map[string]string {
	result := make(map[string]string)

	switch a := attrs.(type) {
	case []struct {
		Key   string      `json:"key"`
		Value interface{} `json:"value"`
	}:
		for _, attr := range a {
			v := attr.Value
			switch val := v.(type) {
			case string:
				result[attr.Key] = val
			case float64:
				// 用 strconv.FormatFloat('g', -1) 保留完整精度，避免 %f 丢失小数位
				result[attr.Key] = strconv.FormatFloat(val, 'g', -1, 64)
			case bool:
				result[attr.Key] = fmt.Sprintf("%t", val)
			case map[string]interface{}:
				if sv, ok := val["stringValue"]; ok {
					result[attr.Key] = fmt.Sprintf("%v", sv)
				} else if iv, ok := val["intValue"]; ok {
					result[attr.Key] = fmt.Sprintf("%v", iv)
				}
			default:
				if v != nil {
					result[attr.Key] = fmt.Sprintf("%v", v)
				}
			}
		}
	}

	return result
}

// truncateAttr truncates long db.statement values to prevent storage issues
func truncateAttr(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

// sanitizeSQL escapes special characters for ClickHouse TabSeparated format
func sanitizeSQL(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}
