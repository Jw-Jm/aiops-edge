package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/observability-platform/ai-apm-ingest-go/internal/metrics"
	"github.com/observability-platform/ai-apm-ingest-go/internal/model"
	"github.com/observability-platform/ai-apm-ingest-go/internal/pipeline"
	"github.com/observability-platform/ai-apm-ingest-go/internal/telemetry"
)

func main() {
	port := flag.Int("port", 8080, "HTTP server port")
	flag.Parse()

	// WAL 持久化目录（生产挂载 PVC 保证数据不丢；空则退化为内存重试）
	walDir := os.Getenv("INGEST_WAL_DIR")
	// 多集群纳管：本 ingest 实例所属集群 ID。
	// Phase 5 Gate（R2 §71）：cluster_id 是本实例的静态身份，不允许缺失后写空——那既违反
	// "禁止猜"，也写不出 partial/missing_fields（ClickHouse 列固定无法表达该语义）。因此
	// 缺失即 fail-closed 拒绝启动（reject 路径，符合"该路径不允许 partial 则直接 reject"）。
	// 现有部署 clusterId="default"（非空）不受影响；真正缺失时才拒绝。
	clusterID := os.Getenv("CLUSTER_ID")
	if clusterID == "" {
		log.Fatalf("CLUSTER_ID not set: ingest instance requires a cluster identity to tag writes; " +
			"set CLUSTER_ID (deployment clusterId). Refusing to start with empty cluster_id.")
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

	// DeepFlow 同步器：把 deepflow-clickhouse 的应用层调用写入 observability 拓扑/trace/日志，
	// 并累加为 VM 服务 RED 指标。
	// 多 k8s 环境支持：DEEPFLOW_CH_ENDPOINTS="name@host:port,name2@host2:port2"（name=cluster 名，RED 指标按 cluster 区分）
	// 兼容旧配置：仅 DEEPFLOW_CH_HOST/DEEPFLOW_CH_PORT 时按单环境 cluster="default" 接入。
	startDeepFlowSyncers := func() int {
		n := 0
		// V9.3 Phase 14：legacy ClickHouse sink 已删除。DeepFlowSyncer 只依赖中立 sink 接口，
		// 此处显式传 nil（无 CH span/edge/log 落盘），核心链路 DeepFlow→VM RED 指标不受影响。
		var (
			dfEdge pipeline.EdgeSink
			dfSpan pipeline.SpanSink
			dfLog  pipeline.LogSink
		)
		if eps := os.Getenv("DEEPFLOW_CH_ENDPOINTS"); eps != "" {
			for _, ep := range strings.Split(eps, ",") {
				ep = strings.TrimSpace(ep)
				if ep == "" {
					continue
				}
				cluster, hostPort := ep, ep
				if at := strings.LastIndex(ep, "@"); at > 0 {
					cluster, hostPort = ep[:at], ep[at+1:]
				}
				host, portStr, ok := strings.Cut(hostPort, ":")
				if !ok {
					portStr = "8123"
				}
				port, _ := strconv.Atoi(portStr)
				if port == 0 {
					port = 8123
				}
				syncer := pipeline.NewDeepFlowSyncer(host, port, cluster, dfEdge, dfSpan, dfLog, met)
				syncer.Start()
				n++
				log.Printf("DeepFlowSyncer enabled (cluster=%s deepflow-ch=%s:%d)", cluster, host, port)
			}
		} else if dfHost := os.Getenv("DEEPFLOW_CH_HOST"); dfHost != "" {
			dfPort := parseEnvInt("DEEPFLOW_CH_PORT")
			if dfPort == 0 {
				dfPort = 8123
			}
			syncer := pipeline.NewDeepFlowSyncer(dfHost, dfPort, "default", dfEdge, dfSpan, dfLog, met)
			syncer.Start()
			n++
			log.Printf("DeepFlowSyncer enabled (cluster=default deepflow-ch=%s:%d)", dfHost, dfPort)
		}
		if n == 0 {
			log.Printf("DeepFlowSyncer disabled (DEEPFLOW_CH_HOST / DEEPFLOW_CH_ENDPOINTS not set)")
		}
		return n
	}
	startDeepFlowSyncers()

	// V9.3 Phase 14：无 legacy CH sink，Pipeline 以 nil span/edge sink 构造
	// （RED 聚合 + new 链 redSink 仍工作）。
	pl := pipeline.New(nil, nil)
	pl.SetClusterID(clusterID)               // 多集群纳管：数据打 cluster_id 标
	pl.SetOnServiceMetric(met.AddServiceRED) // 服务 RED 指标暴露到 /metrics
	// P6.5 new 链双写：聚合的 RED 服务指标在 flush 时写 VictoriaMetrics（ModeNew 真实发送）。
	// 失败仅记日志（可观测），不回退 ClickHouse，也不伪装成功。
	if telRT.Enabled() {
		pl.SetREDSink(func(m *model.ServiceMetric) {
			ts := m.TimeBucket
			if res := telRT.WriteRED(m.TenantID, m.ClusterID, m.ServiceName, float64(m.CallCount), ts); res.Status != "ok" {
				log.Printf("VM RED write failed (tenant=%s service=%s): code=%s msg=%s", m.TenantID, m.ServiceName, res.ErrorCode, res.Message)
			}
		})
	}
	defer pl.Close()

	// DeepFlow data receiver
	dfReceiver := pipeline.NewDeepFlowReceiver(pl)

	apiKey := os.Getenv("INGEST_API_KEY")           // 为空则不启用鉴权（便于本地调试），生产必须配置
	rl := newRateLimiter(parseEnvInt("INGEST_RPS")) // 每接收端点 QPS 上限；<=0 表示不限
	maxBody := int64(10 << 20)                      // 默认 10MB body 上限

	mux := http.NewServeMux()
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

	mux.HandleFunc("/v1/logs", func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.Header.Get("X-Tenant-ID")
		// Phase 5：X-Tenant-ID 缺失不再静默兜底 "default"，改为 fail-closed 拒绝。
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
					// merge resource attrs
					merged := make(map[string]string, len(resourceAttrs)+len(logAttrs))
					for k, v := range resourceAttrs {
						merged[k] = v
					}
					for k, v := range logAttrs {
						merged[k] = v
					}

					record := &model.LogRecord{
						TenantID:    tenantID,
						ClusterID:   clusterID,
						Timestamp:   ts,
						ServiceName: serviceName,
						Severity:    lr.SeverityText,
						Body:        sanitizeLogBody(lr.Body.StringValue),
						Attributes:  merged,
						TraceID:     lr.TraceID,
						SpanID:      lr.SpanID,
						TimeBucket:  ts.Truncate(time.Minute).Format("2006-01-02 15:04:05"),
						Date:        ts.Format("2006-01-02"),
					}
					// V9.3 Phase 14：legacy CH logWriter 已删除，日志只走 new 链 VictoriaLogs
					// （ModeNew 真实发送）。失败仅记日志（可观测），不伪装成功。
					if telRT.Enabled() {
						if res := telRT.WriteLog(record.TenantID, record.ClusterID, record.ServiceName, record.Severity, record.Body, record.Timestamp); res.Status != "ok" {
							log.Printf("VLogs write failed (tenant=%s service=%s): code=%s msg=%s", record.TenantID, record.ServiceName, res.ErrorCode, res.Message)
						}
					}
					count++
				}
			}
		}

		log.Printf("Pipeline: processed %d log records for tenant %s", count, tenantID)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"logs":%d}`, count)
	})

	// DeepFlow native protocol endpoint
	mux.HandleFunc("/v1/deepflow", dfReceiver.ServeHTTP)

	// /health 健康端点。V9.3 Phase 14：legacy ClickHouse 探测已删除；
	// ingest 自身写路径健康由 VM/VLogs 后端侧探针负责，本端点报告进程存活 + new 后端启用。
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
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
		if apiKey != "" && r.Header.Get("X-Api-Key") != apiKey {
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
	server := &http.Server{Addr: addr, Handler: final}

	go func() {
		log.Printf("Ingest Pipeline listening on %s (apiKey=%v, rps=%d, walDir=%q)", addr, apiKey != "", rl.limit, walDir)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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
