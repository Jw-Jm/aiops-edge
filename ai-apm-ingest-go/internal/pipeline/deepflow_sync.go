package pipeline

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/observability-platform/ai-apm-ingest-go/internal/model"
)

const (
	defaultSyncInterval = 60 * time.Second
	minSyncInterval     = 5 * time.Second
	maxSyncInterval     = 3600 * time.Second
)

// parseSyncInterval 从 env DEEPFLOW_SYNC_INTERVAL 解析同步间隔；
// 支持纯数字（秒）或 Go duration（如 "30s"）；非法/越界回退默认 60s。
func parseSyncInterval(v string) time.Duration {
	if v == "" {
		return defaultSyncInterval
	}
	s := strings.TrimSpace(v)
	if _, err := strconv.Atoi(s); err == nil {
		s = s + "s"
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return defaultSyncInterval
	}
	if d < minSyncInterval || d > maxSyncInterval {
		return defaultSyncInterval
	}
	return d
}

// clampStartTime 处理时钟回拨/漂移，把增量起点限制在 [now-15m, now] 内。
func clampStartTime(last, now time.Time) time.Time {
	lo := now.Add(-15 * time.Minute)
	if last.Before(lo) {
		return lo
	}
	if last.After(now) {
		return now
	}
	return last
}

// DeepFlowSyncer 定期从 DeepFlow ClickHouse 拉取应用层调用数据，
// 解析服务名后写入 observability 的 service_topology（拓扑边）与 trace_spans（调用记录），
// 让拓扑页/服务列表/调用链页展示真实服务间调用。
type DeepFlowSyncer struct {
	dfEndpoint string // DeepFlow ClickHouse HTTP 地址，如 http://host:8123
	dfClient   *http.Client
	edgeWriter interface{ AddEdge(*model.TopologyEdge) }
	spanWriter interface{ Add(*model.Span) }
	logWriter  interface{ Add(*model.LogRecord) }
	tenantID   string
	interval   time.Duration
	// 增量拉取状态（线程安全）
	lastSyncMu   sync.Mutex
	lastSyncTime time.Time
}

// NewDeepFlowSyncer 创建 DeepFlow 同步器。edgeWriter 写拓扑边，spanWriter 写 span，logWriter 写日志。
func NewDeepFlowSyncer(dfCHHost string, dfCHPort int, edgeWriter interface{ AddEdge(*model.TopologyEdge) }, spanWriter interface{ Add(*model.Span) }, logWriter interface{ Add(*model.LogRecord) }) *DeepFlowSyncer {
	return &DeepFlowSyncer{
		dfEndpoint: fmt.Sprintf("http://%s:%d", dfCHHost, dfCHPort),
		dfClient:   &http.Client{Timeout: 30 * time.Second},
		edgeWriter: edgeWriter,
		spanWriter: spanWriter,
		logWriter:  logWriter,
		tenantID:   "default",
		interval:   parseSyncInterval(os.Getenv("DEEPFLOW_SYNC_INTERVAL")),
	}
}

// SetTenantID 设置写入的租户。
func (s *DeepFlowSyncer) SetTenantID(t string) *DeepFlowSyncer {
	if t != "" {
		s.tenantID = t
	}
	return s
}

// Start 启动定时同步循环。启动时先确保 DeepFlow 数据保留策略（TTL 30 天），再进入同步循环。
func (s *DeepFlowSyncer) Start() {
	go func() {
		log.Printf("DeepFlowSyncer: starting (interval=%s, endpoint=%s)", s.interval, s.dfEndpoint)
		// 启动时确保 DeepFlow 原始 flow_log 数据保留 30 天（代码层面兜底，
		// 防止 deepflow-server 或其它流程将 TTL 重置回默认 72h）。
		s.ensureRetention()
		for {
			if err := s.Sync(); err != nil {
				log.Printf("DeepFlowSyncer: sync error: %v", err)
			}
			time.Sleep(s.interval)
		}
	}()
}

// ensureRetention 在代码层面设置 DeepFlow flow_log 底层表的 TTL 保留 30 天。
// 通过 POST 执行（ClickHouse 修改类查询要求非 readonly 的 POST 方法）。
// 仅对 _local 底层表生效（Distributed 视图不支持 ALTER TTL）。
func (s *DeepFlowSyncer) ensureRetention() {
	const ttlDays = 30
	for _, tbl := range []string{"l7_flow_log_local", "l4_flow_log_local"} {
		sql := fmt.Sprintf("ALTER TABLE flow_log.%s MODIFY TTL time + toIntervalDay(%d)", tbl, ttlDays)
		if err := s.execDF(sql); err != nil {
			log.Printf("DeepFlowSyncer: 设置 %s TTL 保留 %d 天失败: %v", tbl, ttlDays, err)
			continue
		}
		log.Printf("DeepFlowSyncer: 已设置 %s 数据保留 %d 天 (TTL)", tbl, ttlDays)
	}
}

// execDF 通过 HTTP POST 执行 ClickHouse 修改类语句（ALTER/DELETE/OPTIMIZE 等），返回执行错误。
func (s *DeepFlowSyncer) execDF(sql string) error {
	resp, err := s.dfClient.Post(s.dfEndpoint+"/", "text/plain", strings.NewReader(sql))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("deepflow clickhouse error %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// Sync 增量拉取 application_map 聚合，写入 service_topology。
func (s *DeepFlowSyncer) Sync() error {
	// 增量窗口起点：默认最近 10 分钟（首次），否则自上次成功同步起（保护重叠 1 分钟，避免漏拉）
	now := time.Now().UTC()
	s.lastSyncMu.Lock()
	start := clampStartTime(s.lastSyncTime, now).Add(-1 * time.Minute)
	s.lastSyncMu.Unlock()
	// DeepFlow 的 time 为 Asia/Shanghai 时区，需按该时区格式化查询下界
	shanghai, errLoc := time.LoadLocation("Asia/Shanghai")
	if errLoc != nil {
		shanghai = time.FixedZone("CST", 8*3600)
	}
	startStr := start.In(shanghai).Format("2006-01-02 15:04:05")
	sql := "SELECT time, s0.name AS src, s1.name AS dst, sum(request) AS calls, " +
		"sum(client_error) + sum(server_error) + sum(timeout) AS errs " +
		"FROM flow_metrics.`application_map.1m` " +
		"LEFT JOIN flow_tag.pod_service_map s0 ON s0.id = auto_service_id_0 " +
		"LEFT JOIN flow_tag.pod_service_map s1 ON s1.id = auto_service_id_1 " +
		"WHERE s0.name IS NOT NULL AND s1.name IS NOT NULL AND time >= '" + startStr + "' " +
		"GROUP BY time, src, dst"

	rows, err := s.queryDF(sql)
	if err != nil {
		return fmt.Errorf("query deepflow: %w", err)
	}

	count := 0
	for _, r := range rows {
		src, _ := r["src"].(string)
		dst, _ := r["dst"].(string)
		if src == "" || dst == "" {
			continue
		}
		calls := toUint(r["calls"])
		errs := toUint(r["errs"])
		tbStr, _ := r["time"].(string)
		tb, perr := time.ParseInLocation("2006-01-02 15:04:05", tbStr, shanghai)
		if perr != nil {
			// 兼容带小数秒
			if t2, e2 := time.ParseInLocation("2006-01-02 15:04:05.000", tbStr, shanghai); e2 == nil {
				tb = t2
			} else {
				continue
			}
		}
		if calls == 0 {
			continue
		}
		// DeepFlow 返回的是 Asia/Shanghai 时区；写入时统一转成 UTC 存储（与 observability 一致）
		tbUTC := tb.In(time.UTC)
		s.edgeWriter.AddEdge(&model.TopologyEdge{
			TenantID:      s.tenantID,
			SourceService: src,
			TargetService: dst,
			TimeBucket:    tbUTC,
			CallCount:     calls,
			ErrorCount:    errs,
			AvgDurationNs: 0, // application_map 无时长字段，可后续扩展
			Date:          tbUTC.Format("2006-01-02"),
		})
		count++
	}
	log.Printf("DeepFlowSyncer: synced %d edges from %d rows (window since %s)", count, len(rows), startStr)

	// 同步调用记录（l7 flow → span），支撑 /services、/traces 与拓扑详情趋势
	if err := s.syncTraces(start); err != nil {
		// traces 同步失败时不可推进水位，否则该窗口的调用记录会永久丢失
		return fmt.Errorf("syncTraces failed, watermark not advanced: %w", err)
	}
	// edge 与 traces 均成功后推进增量水位（下次从本次窗口末尾继续）
	s.lastSyncMu.Lock()
	if now.After(s.lastSyncTime) {
		s.lastSyncTime = now
	}
	s.lastSyncMu.Unlock()
	return nil
}

// syncTraces 从 DeepFlow l7_flow_log 拉取最近的应用层调用，构造 span 写入 trace_spans。
// l7_flow_log 是每请求一条的真实 HTTP 调用记录（含源/目标服务、请求路径、响应码、时长）。
func (s *DeepFlowSyncer) syncTraces(windowStart time.Time) error {
	if s.spanWriter == nil {
		return nil
	}
	// 从增量起点拉取真实调用（控制写入量，取前 2000 条）
	shanghai, errLoc := time.LoadLocation("Asia/Shanghai")
	if errLoc != nil {
		shanghai = time.FixedZone("CST", 8*3600)
	}
	windowStartStr := windowStart.In(shanghai).Format("2006-01-02 15:04:05")
	sql := "SELECT start_time, response_duration, s0.name AS src, s1.name AS dst, " +
		"request_resource, response_code " +
		"FROM flow_log.`l7_flow_log` " +
		"LEFT JOIN flow_tag.pod_service_map s0 ON s0.id = auto_service_id_0 " +
		"LEFT JOIN flow_tag.pod_service_map s1 ON s1.id = auto_service_id_1 " +
		"WHERE s0.name IS NOT NULL AND s1.name IS NOT NULL " +
		"AND l7_protocol IN (20, 21, 22, 30) " + // HTTP/HTTP2/HTTPS/DNS 等应用协议
		"AND start_time >= '" + windowStartStr + "' " +
		"ORDER BY start_time DESC LIMIT 2000"

	rows, err := s.queryDF(sql)
	if err != nil {
		return fmt.Errorf("query l7 flow: %w", err)
	}

	count := 0
	for _, r := range rows {
		rowStartStr, _ := r["start_time"].(string)
		ts, perr := time.ParseInLocation("2006-01-02 15:04:05.000000", rowStartStr, shanghai)
		if perr != nil {
			if t2, e2 := time.ParseInLocation("2006-01-02 15:04:05", rowStartStr, shanghai); e2 == nil {
				ts = t2
			} else {
				continue
			}
		}
		dst, _ := r["dst"].(string)
		if dst == "" {
			continue
		}
		operation := fmt.Sprintf("%v", r["request_resource"])
		if operation == "" || operation == "<nil>" {
			operation = "unknown"
		}
		// 过滤 DeepFlow 内部/探针请求，避免污染业务日志
		if isInternalQuery(operation) {
			continue
		}
		// response_duration 单位：DeepFlow 为微秒（µs），转纳秒存储
		durUs := toUint(r["response_duration"])
		durNs := durUs * 1000
		code := int64(toUint(r["response_code"]))
		isErr := uint8(0)
		// 错误判定：5xx 或超时(>1s)判错；4xx(如 404 探针)不算错误
		if (code >= 500 && code <= 599) || (code == 0 && durNs >= 1000000000) {
			isErr = 1
		}
		// 用 start_time 生成稳定的 trace_id / span_id（十六进制）
		base := fmt.Sprintf("%d", ts.UnixNano())
		span := &model.Span{
			TenantID:      s.tenantID,
			TraceID:       hexHash(base, 32),
			SpanID:        hexHash(base+"s", 16),
			ParentSpanID:  "",
			ServiceName:   dst,
			OperationName: operation,
			SpanKind:      "SERVER",
			StatusCode:    uint8(code),
			StartTime:     ts.In(time.UTC),
			DurationNs:    durNs,
			Attributes:    map[string]string{"http.url": operation, "source": fmt.Sprintf("%v", r["src"])},
			HTTPMethod:    "GET",
			HTTPURL:       operation,
			HTTPStatusCode: uint16(code),
			IsSlow:        boolToU8(durNs >= 500000000),
			IsError:       isErr,
			ServiceInstanceID: fmt.Sprintf("%v", r["src"]),
		}
		s.spanWriter.Add(span)

		// 构造访问日志（src 服务访问 dst 服务）写入 log_records，支撑 /logs 页
		if s.logWriter != nil {
			severity := "INFO"
			if isErr == 1 {
				severity = "ERROR"
			}
			srcName := fmt.Sprintf("%v", r["src"])
			s.logWriter.Add(&model.LogRecord{
				TenantID:    s.tenantID,
				Timestamp:   ts.In(time.UTC),
				ServiceName: dst,
				Severity:    severity,
				Body:        fmt.Sprintf("%s -> %s %s [%d] %dms", srcName, dst, operation, code, durNs/1e6),
				Attributes:  map[string]string{"http.url": operation, "source": srcName, "http.status_code": fmt.Sprintf("%d", code)},
				TraceID:     span.TraceID,
				SpanID:      span.SpanID,
				TimeBucket:  ts.Truncate(time.Minute).Format("2006-01-02 15:04:05"),
				Date:        ts.Format("2006-01-02"),
			})
		}
		count++
	}
	log.Printf("DeepFlowSyncer: synced %d spans from %d l7 flows", count, len(rows))
	return nil
}

// isInternalQuery 判断是否系统内部/探针请求（应过滤，不污染业务日志/调用链）。
func isInternalQuery(op string) bool {
	if op == "" || op == "unknown" || op == "<nil>" {
		return true
	}
	lower := strings.ToLower(op)
	// ClickHouse HTTP 查询（/?query=...）——本系统 ingest/query-api 写入查询，非业务调用
	if strings.HasPrefix(lower, "/?query=") || strings.HasPrefix(lower, "?query=") {
		return true
	}
	// SQL 查询/写入（DeepFlow 探针自身的 ClickHouse 查询）
	if strings.HasPrefix(lower, "select ") || strings.HasPrefix(lower, "insert ") ||
		strings.HasPrefix(lower, "update ") || strings.HasPrefix(lower, "delete ") ||
		strings.Contains(lower, " format tabseparated") {
		return true
	}
	// DeepFlow 内部同步/探针接口
	if strings.Contains(lower, "/v1/sync/") || strings.Contains(lower, "/v1/query/") ||
		strings.Contains(lower, "/exec/") || strings.Contains(lower, "/api/v1/") ||
		strings.Contains(lower, "/metrics") || strings.Contains(lower, "/readyz") ||
		strings.Contains(lower, "/healthz") || strings.Contains(lower, "/ping") {
		return true
	}
	return false
}

// hexHash 生成固定长度的十六进制伪 ID（由 seed 推导）。
func hexHash(seed string, length int) string {
	h := uint64(14695981039346656037)
	for _, c := range seed {
		h = (h ^ uint64(c)) * 1099511628211
	}
	s := fmt.Sprintf("%016x%016x", h, h^0x9e3779b97f4a7c15)
	if len(s) >= length {
		return s[:length]
	}
	return s
}

// boolToU8 将 bool 转为 uint8。
func boolToU8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// queryDF 执行 SQL 并解析为 []map[string]interface{}。
// 使用 ClickHouse 的标准 JSON 输出（{"meta":...,"data":[...]}），解析 data 字段。
func (s *DeepFlowSyncer) queryDF(sql string) ([]map[string]interface{}, error) {
	u := s.dfEndpoint + "/?" + "default_format=JSON&" + "query=" + url.QueryEscape(sql)
	resp, err := s.dfClient.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("deepflow clickhouse error %d: %s", resp.StatusCode, string(body))
	}
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	var out struct {
		Data []map[string]interface{} `json:"data"`
	}
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// toUint 安全转换数值（ClickHouse JSON 可能返回 float64）。
func toUint(v interface{}) uint64 {
	switch n := v.(type) {
	case float64:
		return uint64(n)
	case json.Number:
		f, _ := n.Float64()
		return uint64(f)
	case string:
		var f float64
		if _, err := fmt.Sscanf(n, "%f", &f); err == nil {
			return uint64(f)
		}
	case int64:
		return uint64(n)
	case uint64:
		return n
	}
	return 0
}


