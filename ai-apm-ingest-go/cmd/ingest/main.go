package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"

	"github.com/observability-platform/ai-apm-ingest-go/internal/metrics"
	"github.com/observability-platform/ai-apm-ingest-go/internal/model"
	"github.com/observability-platform/ai-apm-ingest-go/internal/otlpgrpc"
	"github.com/observability-platform/ai-apm-ingest-go/internal/pipeline"
	"github.com/observability-platform/ai-apm-ingest-go/internal/telemetry"
	"github.com/observability-platform/ai-apm-ingest-go/internal/tracesink"
)

var canonicalUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var eventIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func main() {
	port := flag.Int("port", 8080, "HTTP server port")
	flag.Parse()

	// WAL 持久化目录（生产挂载 PVC 保证数据不丢）。生产环境没有 WAL
	// 时拒绝启动，避免把 200/202 当成不可恢复的内存接受。
	walDir := os.Getenv("INGEST_WAL_DIR")
	production := strings.EqualFold(strings.TrimSpace(os.Getenv("AIOPS_ENV")), "production")
	if production && strings.TrimSpace(walDir) == "" {
		log.Fatalf("INGEST_WAL_DIR is required in production: durable event acceptance cannot use memory fallback")
	}
	// 多集群纳管：本 ingest 实例所属集群 ID。
	// Phase 5 Gate（R2 §71）：cluster_id 是本实例的静态身份，不允许缺失后写空——那既违反
	// "禁止猜"，也写不出 partial/missing_fields（ClickHouse 列固定无法表达该语义）。因此
	// 缺失即 fail-closed 拒绝启动（reject 路径，符合"该路径不允许 partial 则直接 reject"）。
	clusterID := os.Getenv("CLUSTER_ID")
	if !canonicalUUID.MatchString(strings.TrimSpace(clusterID)) {
		log.Fatalf("CLUSTER_ID must be a canonical UUID: ingest instance requires an immutable cluster identity")
	}

	// V9.3 Phase 14（P14.5）：legacy ClickHouse writer 已物理删除。写路径唯一为 new 后端
	// （VictoriaMetrics / VictoriaLogs，由 TELEMETRY_WRITER_MODE 控制）。
	// 生产化中间件：鉴权 + 限流 + metrics
	met := metrics.New()

	// V9.2 P6.5：new 后端（VictoriaMetrics / VictoriaLogs）生产接线。
	// 由 TELEMETRY_WRITER_MODE 控制（disabled/"legacy"=停用；new=生产写入）。
	// 非法 mode 或 ModeNew 缺 backend URL 时 fail-closed 拒绝启动。
	telRT, err := telemetry.NewRuntimeFromEnv()
	if err != nil {
		log.Fatalf("telemetry wiring: %v", err)
	}
	if telRT.Enabled() {
		log.Printf("telemetry new backend ACTIVE (TELEMETRY_WRITER_MODE=new): VM=%v VLogs=%v", telRT.VM != nil, telRT.VLogs != nil)
	} else {
		log.Printf("telemetry new backend disabled (TELEMETRY_WRITER_MODE=%q)", os.Getenv("TELEMETRY_WRITER_MODE"))
	}

	// fail-closed：new 后端未启用时没有任何写路径，拒绝启动（不许"无数据 sink 静默运行"）。
	// 不再接受 LEGACY_WRITER_ENABLED 等旧开关（已删除），避免未来误恢复双写。
	if !telRT.Enabled() {
		log.Fatalf("no write path active: telemetry new backend disabled; refusing to start with no data sink")
	}

	clickHouseHTTPURL := strings.TrimRight(strings.TrimSpace(os.Getenv("CLICKHOUSE_HTTP_URL")), "/")
	eventsWAL, err := newEventWAL(walDir)
	if err != nil {
		log.Fatalf("event acceptance WAL init failed: %v", err)
	}
	eventReplayCtx, cancelEventReplay := context.WithCancel(context.Background())
	if eventsWAL != nil {
		met.SetEventsWALPending(int64(eventsWAL.PendingCount()))
		eventsWAL.StartReplay(eventReplayCtx, func(ctx context.Context, body []byte) error {
			if clickHouseHTTPURL == "" {
				met.IncEventsBackendFailed()
				return fmt.Errorf("event sink unavailable")
			}
			if err := insertEventBatch(ctx, clickHouseHTTPURL, os.Getenv("CLICKHOUSE_USER"), os.Getenv("CLICKHOUSE_PASSWORD"), body); err != nil {
				met.IncEventsBackendFailed()
				return err
			}
			met.SetEventsWALPending(int64(eventsWAL.PendingCount()))
			return nil
		}, time.Second)
		defer func() {
			cancelEventReplay()
			_ = eventsWAL.Close()
		}()
	} else {
		defer cancelEventReplay()
	}
	// C-01（0004_runtime_convergence / 报告 §16）：固定 ClickHouse trace_spans 为平台
	// Trace Persistent SoT。构造 ClickHouseSpanSink（CLICKHOUSE_HTTP_URL 配置，HTTP 接口，
	// 零新增依赖）。若配置了 CH 但写入失败 → readiness fail-closed（不允许"接收成功但静默丢 Span"）。
	var spanSink pipeline.SpanSink
	var topologySink *tracesink.ClickHouseTopologyEdgeSink
	if clickHouseHTTPURL != "" {
		// C-01（报告 §16 / 27.18）：trace_spans 为平台 Trace SoT；使用带鉴权的 sink（生产 CH 需要）。
		chs := tracesink.NewClickHouseSpanSinkAuth(clickHouseHTTPURL, os.Getenv("CLICKHOUSE_USER"), os.Getenv("CLICKHOUSE_PASSWORD"), 10*time.Second)
		if err := chs.Probe(); err != nil {
			log.Fatalf("ClickHouseSpanSink probe failed: %v", err)
		}
		spanSink = chs
		log.Printf("ClickHouseSpanSink enabled (trace SoT): %s", clickHouseHTTPURL)
		topologySink = tracesink.NewClickHouseTopologyEdgeSinkAuth(clickHouseHTTPURL, os.Getenv("CLICKHOUSE_USER"), os.Getenv("CLICKHOUSE_PASSWORD"), 10*time.Second)
		if err := topologySink.Probe(); err != nil {
			log.Fatalf("ClickHouseTopologyEdgeSink probe failed: %v", err)
		}
		log.Printf("ClickHouseTopologyEdgeSink enabled (service dependency projection)")
	} else {
		// 27.18：candidate/production profile 不允许 SpanSink=nil（fail-closed 拒绝启动）。
		// 默认本地（TRACE_SOT_MODE=off）仅 WARN；设 TRACE_SOT_MODE=required 时 SpanSink=nil → 拒绝启动。
		if os.Getenv("TRACE_SOT_MODE") == "required" {
			log.Fatalf("TRACE_SOT_MODE=required but CLICKHOUSE_HTTP_URL not set: trace_spans SoT sink must be configured; refusing to start with nil span sink")
		}
		log.Printf("WARN: CLICKHOUSE_HTTP_URL not set; trace_spans SoT sink is nil (production/candidate must configure or set TRACE_SOT_MODE=required)")
	}

	// C-01：Pipeline 以平台 ClickHouseSpanSink 作为 span sink（Trace Persistent SoT）。
	// DeepFlow 只通过官方 OTLP/gRPC exporter 输入，不读取或修改 DeepFlow 自有 ClickHouse。
	pl := pipeline.New(spanSink, topologySink)
	pl.SetClusterID(clusterID)                                    // 多集群纳管：数据打 cluster_id 标
	pl.SetOnServiceMetricWithCluster(met.AddServiceREDForCluster) // 服务 RED 指标保留不可变 cluster 维度
	pl.SetEdgeSinkResultObserver(func(accepted int, failed bool) {
		if failed {
			met.AddMetricsDropped(1)
			return
		}
		met.AddEdgesWritten(int64(accepted))
	})
	// P6.5 new 链双写：聚合的 RED 服务指标在 flush 时写 VictoriaMetrics（ModeNew 真实发送）。
	// 失败仅记日志（可观测），不回退 ClickHouse，也不伪装成功。
	if telRT.Enabled() {
		pl.SetREDSink(func(m *model.ServiceMetric) {
			if res := telRT.WriteServiceRED(m); res.Status != "ok" {
				log.Printf("VM RED write failed (tenant=%s service=%s): code=%s msg=%s", m.TenantID, m.ServiceName, res.ErrorCode, res.Message)
			}
		})
	}
	defer pl.Close()

	apiKey := os.Getenv("INGEST_API_KEY") // 为空则不启用鉴权（便于本地调试），生产必须配置
	if production && strings.TrimSpace(apiKey) == "" {
		log.Fatalf("INGEST_API_KEY is required in production: refusing unauthenticated event ingestion")
	}
	rl := newRateLimiter(parseEnvInt("INGEST_RPS")) // 每接收端点 QPS 上限；<=0 表示不限
	maxBody := int64(10 << 20)                      // 默认 10MB body 上限
	eventWALMaxBytes := parseEnvInt64Default("INGEST_WAL_MAX_BYTES", 1<<30)

	mux := http.NewServeMux()
	// Event collector is an adapter only. It POSTs the event envelope to this
	// endpoint; ingest is the sole component allowed to write k8s_events.
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		tenantID := strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
		clusterID := strings.TrimSpace(r.Header.Get("X-Cluster-ID"))
		if !canonicalUUID.MatchString(tenantID) || !canonicalUUID.MatchString(clusterID) {
			http.Error(w, "tenant and cluster must be canonical UUIDs", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			http.Error(w, "body too large or read error", http.StatusRequestEntityTooLarge)
			return
		}
		if err := validateEventBatch(body, tenantID, clusterID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if clickHouseHTTPURL == "" {
			http.Error(w, "event sink unavailable", http.StatusServiceUnavailable)
			return
		}
		if eventsWAL != nil {
			rec, err := eventsWAL.AppendBounded(tenantID, clusterID, body, eventWALMaxBytes)
			if err != nil {
				if errors.Is(err, errEventWALFull) {
					w.Header().Set("Retry-After", "5")
					http.Error(w, "event acceptance WAL is full", http.StatusTooManyRequests)
					return
				}
				http.Error(w, "durable acceptance unavailable", http.StatusServiceUnavailable)
				return
			}
			met.AddEventsAccepted(int64(countEventRows(body)))
			met.SetEventsWALPending(int64(eventsWAL.PendingCount()))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = fmt.Fprintf(w, `{"accepted":%d,"receipt_id":%q}`, countEventRows(body), rec.ReceiptID)
			return
		}
		if err := insertEventBatch(r.Context(), clickHouseHTTPURL, os.Getenv("CLICKHOUSE_USER"), os.Getenv("CLICKHOUSE_PASSWORD"), body); err != nil {
			http.Error(w, "event sink unavailable", http.StatusServiceUnavailable)
			return
		}
		met.AddEventsAccepted(int64(countEventRows(body)))
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"accepted":%d}`, countEventRows(body))
	})
	mux.HandleFunc("/v1/events/checkpoint", func(w http.ResponseWriter, r *http.Request) {
		tenantID := strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
		clusterID := strings.TrimSpace(r.Header.Get("X-Cluster-ID"))
		source := strings.TrimSpace(r.URL.Query().Get("source"))
		if !canonicalUUID.MatchString(tenantID) || !canonicalUUID.MatchString(clusterID) || source == "" || clickHouseHTTPURL == "" {
			http.Error(w, "invalid checkpoint scope", http.StatusBadRequest)
			return
		}
		q := "SELECT max(ts) FROM observability.k8s_events WHERE source = " + quoteCHString(source) +
			" AND tenant_id = " + quoteCHString(tenantID) + " AND cluster_id = " + quoteCHString(clusterID)
		value, err := queryClickHouse(r.Context(), clickHouseHTTPURL, os.Getenv("CLICKHOUSE_USER"), os.Getenv("CLICKHOUSE_PASSWORD"), q)
		if err != nil {
			http.Error(w, "checkpoint unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.TrimSpace(value)))
	})
	mux.HandleFunc("/v1/traces", func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.Header.Get("X-Tenant-ID")
		// Phase 5：X-Tenant-ID 缺失不再静默兜底 "default"，改为 fail-closed 拒绝，
		// 避免把缺 tenant 身份的 trace 写入共享存储（多租户隔离）。
		if tenantID == "" {
			http.Error(w, "missing X-Tenant-ID header", http.StatusBadRequest)
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			http.Error(w, "body too large or read error", http.StatusRequestEntityTooLarge)
			return
		}
		defer r.Body.Close()

		count, err := pl.ProcessOTLPTraces(tenantID, body)
		if err != nil {
			log.Printf("ProcessOTLPTraces error: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		met.AddSpansReceived(int64(count))

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"spans":%d}`, count)
	})

	writeLog := func(tenantID, clusterID, service, level, body string, ts time.Time) telemetry.WriteResult {
		return telRT.WriteLog(tenantID, clusterID, service, level, body, ts)
	}
	mux.Handle("/v1/logs", newOTLPLogsHandler(clusterID, maxBody, writeLog))

	var grpcServer *grpc.Server
	var grpcListener net.Listener
	if parseEnvBoolDefault("OTLP_GRPC_ENABLED", true) {
		otlpTenantID := strings.TrimSpace(os.Getenv("DEEPFLOW_TENANT_ID"))
		if otlpTenantID == "" {
			log.Fatalf("DEEPFLOW_TENANT_ID not set: OTLP/gRPC receiver requires a tenant identity")
		}
		grpcServer = grpc.NewServer()
		receiver := otlpgrpc.NewReceiver(pl, otlpTenantID).SetMetrics(met)
		coltrace.RegisterTraceServiceServer(grpcServer, receiver)
		grpcPort := parseEnvInt("OTLP_GRPC_PORT")
		if grpcPort <= 0 || grpcPort > 65535 {
			grpcPort = 4317
		}
		grpcAddr := fmt.Sprintf(":%d", grpcPort)
		grpcListener, err = net.Listen("tcp", grpcAddr)
		if err != nil {
			log.Fatalf("OTLP/gRPC listen %s: %v", grpcAddr, err)
		}
		go func() {
			log.Printf("OTLP/gRPC TraceService listening on %s", grpcAddr)
			if err := grpcServer.Serve(grpcListener); err != nil {
				log.Printf("OTLP/gRPC server stopped: %v", err)
			}
		}()
	}

	// /health 健康端点。V9.3 Phase 14：legacy ClickHouse 探测已删除；
	// ingest 自身写路径健康由 VM/VLogs 后端侧探针负责，本端点报告进程存活 + new 后端启用。
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// C-01：Trace Persistent SoT sink 配置了但不可用 → readiness fail-closed
		//（不允许"接收成功但静默丢 Span"）。
		if eventsWAL != nil && clickHouseHTTPURL == "" {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "unhealthy", "reason": "event_sink_not_configured"})
			return
		}
		if chs, ok := spanSink.(*tracesink.ClickHouseSpanSink); ok && !chs.Healthy() {
			detail := "trace_sot_sink_not_ready"
			if last := chs.LastError(); last != nil {
				detail = last.Error()
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status": "unhealthy", "reason": "trace_sot_sink_unavailable", "detail": detail,
			})
			return
		}
		if topologySink != nil && !topologySink.Healthy() {
			detail := "topology_projection_sink_not_ready"
			if last := topologySink.LastError(); last != nil {
				detail = last.Error()
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status": "unhealthy", "reason": "topology_projection_sink_unavailable", "detail": detail,
			})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
	})

	// Prometheus 指标端点（生产供 vmalert 抓取告警）
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(met.Snapshot()))
	})

	// 鉴权 + 限流中间件（对数据接收端点生效，health/metrics 开放）
	secured := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		met.IncReqTotal()
		if apiKey != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Api-Key")), []byte(apiKey)) != 1 {
			met.IncReqRejected()
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if rl.limit > 0 && !rl.allow() {
			met.IncReqRejected()
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		mux.ServeHTTP(w, r)
	})

	// 将 /metrics 与 /health 从鉴权中豁免：在 secured 前先判断
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" || r.URL.Path == "/health" {
			mux.ServeHTTP(w, r)
			return
		}
		secured.ServeHTTP(w, r)
	})

	addr := fmt.Sprintf(":%d", *port)
	server := &http.Server{Addr: addr, Handler: requireMTLS(final)}
	if err := configureMTLSServer(server); err != nil {
		log.Fatalf("mTLS configuration: %v", err)
	}

	go func() {
		log.Printf("Ingest Pipeline listening on %s (apiKey=%v, rps=%d, walDir=%q)", addr, apiKey != "", rl.limit, walDir)
		if err := listenHTTP(server); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("Shutting down...")
	// H6：Shutdown 优雅排空在途请求（10s 上限），再冲刷各 writer 缓冲/WAL
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}
	if grpcServer != nil {
		grpcServer.GracefulStop()
		if grpcListener != nil {
			_ = grpcListener.Close()
		}
	}
}

type logWriteFunc func(tenantID, clusterID, service, level, body string, ts time.Time) telemetry.WriteResult

// newOTLPLogsHandler acknowledges a log batch only after every record has been
// accepted by the configured durable sink. Retryable sink failures use 503 so
// OTLP clients retain and retry the batch instead of deleting it after a false
// 200 response.
func newOTLPLogsHandler(clusterID string, maxBody int64, writeLog logWriteFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if writeLog == nil {
			http.Error(w, "log sink unavailable", http.StatusServiceUnavailable)
			return
		}
		tenantID := r.Header.Get("X-Tenant-ID")
		if tenantID == "" {
			http.Error(w, "missing X-Tenant-ID header", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			http.Error(w, "body too large or read error", http.StatusRequestEntityTooLarge)
			return
		}
		defer r.Body.Close()

		var req model.OTLPLogRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "json unmarshal: "+err.Error(), http.StatusBadRequest)
			return
		}

		count := 0
		for _, rl := range req.ResourceLogs {
			resourceAttrs := extractAttributes(rl.Resource.Attributes)
			serviceName := resourceAttrs["service.name"]
			if serviceName == "" {
				serviceName = "unknown"
			}
			for _, sl := range rl.ScopeLogs {
				for _, lr := range sl.LogRecords {
					ts := parseNanoTimestamp(lr.TimeUnixNano)
					logAttrs := extractAttributes(lr.Attributes)
					merged := make(map[string]string, len(resourceAttrs)+len(logAttrs))
					for k, v := range resourceAttrs {
						merged[k] = v
					}
					for k, v := range logAttrs {
						merged[k] = v
					}
					record := &model.LogRecord{
						TenantID: tenantID, ClusterID: clusterID, Timestamp: ts,
						ServiceName: serviceName, Severity: lr.SeverityText,
						Body: sanitizeLogBody(lr.Body.StringValue), Attributes: merged,
						TraceID: lr.TraceID, SpanID: lr.SpanID,
						TimeBucket: ts.Truncate(time.Minute).Format("2006-01-02 15:04:05"),
						Date:       ts.Format("2006-01-02"),
					}
					res := writeLog(record.TenantID, record.ClusterID, record.ServiceName, record.Severity, record.Body, record.Timestamp)
					if res.Status != "ok" {
						log.Printf("VLogs write failed (tenant=%s service=%s): code=%s msg=%s", record.TenantID, record.ServiceName, res.ErrorCode, res.Message)
						status := http.StatusInternalServerError
						if res.Retryable {
							status = http.StatusServiceUnavailable
						}
						http.Error(w, "log sink write failed: "+res.Message, status)
						return
					}
					count++
				}
			}
		}

		log.Printf("Pipeline: processed %d log records for tenant %s", count, tenantID)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"logs":%d}`, count)
	})
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

// parseNanoTimestamp parses a nanosecond timestamp string to time.Time
func parseNanoTimestamp(s string) time.Time {
	if s == "" {
		return time.Now().UTC()
	}
	ns, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Now().UTC()
	}
	return time.Unix(0, ns).UTC()
}

// sanitizeLogBody escapes special characters for ClickHouse TabSeparated format
func sanitizeLogBody(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

func validateEventBatch(body []byte, tenantID, clusterID string) error {
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && strings.TrimSpace(lines[0]) == "") {
		return fmt.Errorf("event batch is empty")
	}
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 15 {
			return fmt.Errorf("event row must contain 15 tab-separated fields")
		}
		if fields[0] != tenantID || fields[1] != clusterID {
			return fmt.Errorf("event row scope does not match authenticated headers")
		}
		ts, err := parseEventTimestamp(fields[2])
		if err != nil {
			return fmt.Errorf("event row timestamp is invalid")
		}
		bucket, err := time.ParseInLocation("2006-01-02 15:04:05", fields[13], time.UTC)
		if err != nil || !bucket.Equal(ts.UTC().Truncate(time.Minute)) {
			return fmt.Errorf("event row time_bucket is invalid")
		}
		if !eventIDPattern.MatchString(fields[14]) {
			return fmt.Errorf("event row event_id must be a 64-character SHA-256 hex digest")
		}
	}
	return nil
}

// parseEventTimestamp accepts exactly the UTC, timezone-free representation
// emitted by the event collector for ClickHouse DateTime64(9).  Ingest must
// reject malformed rows before they enter the durable acceptance WAL: a bad
// first row would otherwise block FIFO replay and cause every later event to
// remain pending indefinitely.
func parseEventTimestamp(value string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("timestamp must be UTC ClickHouse format")
}

func countEventRows(body []byte) int {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "\n"))
}

func insertEventBatch(ctx context.Context, baseURL, user, password string, body []byte) error {
	target := strings.TrimRight(baseURL, "/") + "/?query=" + url.QueryEscape("INSERT INTO observability.k8s_events FORMAT TabSeparated") + "&wait_end_of_query=1&send_progress_in_http_headers=0"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain")
	if user != "" {
		req.SetBasicAuth(user, password)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("clickhouse insert status %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	return nil
}

func queryClickHouse(ctx context.Context, baseURL, user, password, query string) (string, error) {
	target := strings.TrimRight(baseURL, "/") + "/?query=" + url.QueryEscape(query) + "&wait_end_of_query=1&send_progress_in_http_headers=0"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	if user != "" {
		req.SetBasicAuth(user, password)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("clickhouse query status %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	return string(payload), nil
}

func quoteCHString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `'`, `\'`)
	return "'" + value + "'"
}

// parseEnvInt 解析环境变量为整数，非法或空返回 0。
func parseEnvInt(key string) int {
	v := os.Getenv(key)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}

func parseEnvInt64Default(key string, defaultValue int64) int64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultValue
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return defaultValue
	}
	return n
}

func parseEnvBoolDefault(key string, defaultValue bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if v == "" {
		return defaultValue
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return defaultValue
	}
}

// rateLimiter 是基于固定窗口的简单令牌限流器（生产可替换为更精确的滑动窗口）。
type rateLimiter struct {
	limit   int
	mu      sync.Mutex
	window  time.Time
	windowN int
}

// newRateLimiter 从环境变量字符串创建限流器。limit<=0 表示不限流。
func newRateLimiter(rps int) *rateLimiter {
	return &rateLimiter{limit: rps, window: time.Now()}
}

// allow 判断当前是否允许放行一个请求（固定 1 秒窗口内的 RPS）。
func (r *rateLimiter) allow() bool {
	if r.limit <= 0 {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if now.Sub(r.window) >= time.Second {
		r.window = now
		r.windowN = 0
	}
	if r.windowN >= r.limit {
		return false
	}
	r.windowN++
	return true
}
