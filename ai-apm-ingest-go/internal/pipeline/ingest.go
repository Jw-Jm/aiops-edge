package pipeline

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/observability-platform/ai-apm-ingest-go/internal/clickhouse"
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

// Pipeline processes OTLP JSON and writes to ClickHouse
type Pipeline struct {
	writer        *clickhouse.Writer
	metricsWriter *clickhouse.MetricsWriter
	mu            sync.Mutex
	metricsAgg    map[metricsKey]*metricsValue
	edgesAgg      map[edgeKey]*edgeValue
	stopCh        chan struct{}
	doneCh        chan struct{}
	// onServiceMetric 可选回调：每累加一次服务调用时通知外部（用于喂 Prometheus 服务 RED）。
	onServiceMetric func(service string, isError bool, durationNs uint64)
}

// SetOnServiceMetric 注册服务 RED 回调（可选，用于暴露服务指标到 /metrics）。
func (p *Pipeline) SetOnServiceMetric(fn func(service string, isError bool, durationNs uint64)) {
	p.onServiceMetric = fn
}

// New creates a new Pipeline with the given span writer and metrics writer
func New(w *clickhouse.Writer, mw *clickhouse.MetricsWriter) *Pipeline {
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
	p.metricsAgg = make(map[metricsKey]*metricsValue)
	p.edgesAgg = make(map[edgeKey]*edgeValue)
	p.mu.Unlock()

	for k, v := range edges {
		tb, _ := time.Parse("2006-01-02T15:04", k.timeBucket)
		date := tb.Format("2006-01-02")
		avgNs := uint64(0)
		if v.durationCount > 0 {
			avgNs = v.durationSumNs / v.durationCount
		}
		p.metricsWriter.AddEdge(&model.TopologyEdge{
			TenantID:      k.tenantID,
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

	// First pass: collect all span infos for this request (needed for parent lookup)
	// We need trace-level context: spanID -> serviceName mapping
	traceCtx := &traceContext{
		spanIDToService: make(map[string]string),
		spanIDToCaller:  make(map[string]string),
	}

	count := 0
	for _, rs := range req.ResourceSpans {
		resourceAttrs := extractAttributes(rs.Resource.Attributes)
		serviceName := resourceAttrs["service.name"]
		if serviceName == "" {
			serviceName = "unknown"
		}

		for _, ss := range rs.ScopeSpans {
			for _, s := range ss.Spans {
				span := p.convertSpan(tenantID, &s, serviceName, resourceAttrs)
				p.writer.Add(span)
				count++

				// Track span for metrics
				info := &spanInfo{
					traceID:      span.TraceID,
					spanID:       span.SpanID,
					parentSpanID: span.ParentSpanID,
					serviceName:  span.ServiceName,
					startTime:    span.StartTime,
					durationNs:   span.DurationNs,
					isError:      span.IsError,
				}
				traceCtx.spanInfos = append(traceCtx.spanInfos, info)
				traceCtx.spanIDToService[span.SpanID] = span.ServiceName
			}
		}
	}

	// Second pass: extract RED metrics and topology edges
	p.extractMetrics(tenantID, traceCtx)

	log.Printf("Pipeline: processed %d spans for tenant %s", count, tenantID)
	return count, nil
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
		if p.onServiceMetric != nil {
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

		HTTPMethod:     merged["http.method"],
		HTTPURL:        merged["http.url"],
		DBSystem:       merged["db.system"],
		DBStatement:    merged["db.statement"],
		RPCSystem:      merged["rpc.system"],
		ServiceInstanceID: merged["service.instance.id"],
		K8sNamespace:   merged["k8s.namespace.name"],
		K8sPodName:     merged["k8s.pod.name"],

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
