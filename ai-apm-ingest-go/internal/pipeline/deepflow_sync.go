package pipeline

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
	// watermarkFileName 是 DeepFlow 增量同步水位的本地持久化文件名（存 RFC3339 时间戳）。
	// 文件不存在视为首次启动；重启后从此水位继续增量同步，避免丢窗口/重拉全量。
	watermarkFileName = "deepflow_last_sync"
)

// deepflowWatermarkPath 返回水位文件路径：优先 ${INGEST_WAL_DIR}（生产挂载 PVC），
// 未设置时回退 /wal。
func deepflowWatermarkPath() string {
	dir := os.Getenv("INGEST_WAL_DIR")
	if dir == "" {
		dir = "/wal"
	}
	return filepath.Join(dir, watermarkFileName)
}

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

// parseSampleRate 从 env DEEPFLOW_SPAN_SAMPLE_RATE 解析抽样率（0~1，浮点或百分比）。
// 非法/越界（<0 或 >1）回退默认 1.0（全量写入）。
func parseSampleRate(v string) float64 {
	if v == "" {
		return 1.0
	}
	s := strings.TrimSpace(v)
	s = strings.TrimSuffix(s, "%")
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || f > 1 {
		return 1.0
	}
	return f
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
// 让拓扑页/服务列表/调用链页展示真实服务间调用；同时把真实流量累加为 VM 服务 RED 指标。
type DeepFlowSyncer struct {
	dfEndpoint string // DeepFlow ClickHouse HTTP 地址，如 http://host:8123
	dfClient   *http.Client
	// 可选 sink（V9.3 Phase 14 依赖倒置）：nil 或 no-op 均合法，调用方按 nil 跳过。
	edgeWriter EdgeSink
	spanWriter SpanSink
	logWriter  LogSink
	redMetric  interface {
		AddServiceREDForCluster(cluster, service string, isError bool, durationNs uint64)
	}
	cluster  string // 所属 k8s 环境/集群，RED 指标用 cluster 标签区分
	tenantID string
	interval time.Duration
	// 抽样率（0~1）：只写入 part 比例的 span，控制 ClickHouse 写入量与存储成本。
	// 因每条 l7 flow 现在会产出 client+server 两个 span（写入量翻倍），
	// 抽样率可把最终落库量降回原单 span 水平（如 0.5 = 每 flow 平均 1 span）。
	sampleRate float64
	// 增量拉取状态（线程安全）
	lastSyncMu   sync.Mutex
	lastSyncTime time.Time
	// 水位持久化文件路径；不可写时为 ""（降级为纯内存态，仅 log 告警）
	watermarkPath string
}

// NewDeepFlowSyncer 创建 DeepFlow 同步器。edgeWriter 写拓扑边，spanWriter 写 span，logWriter 写日志。
// 三者可为 nil 或 no-op（V9.3 Phase 14 依赖倒置：只依赖中立 sink 接口，不绑定具体存储实现）。
// redMetric 可选：若提供，则把同步到的真实服务流量累加为 VM 服务 RED 指标（cluster 为所属环境）。
func NewDeepFlowSyncer(dfCHHost string, dfCHPort int, cluster string, edgeWriter EdgeSink, spanWriter SpanSink, logWriter LogSink, redMetric interface {
	AddServiceREDForCluster(cluster, service string, isError bool, durationNs uint64)
}) *DeepFlowSyncer {
	s := &DeepFlowSyncer{
		dfEndpoint: fmt.Sprintf("http://%s:%d", dfCHHost, dfCHPort),
		dfClient:   &http.Client{Timeout: 30 * time.Second},
		edgeWriter: edgeWriter,
		spanWriter: spanWriter,
		logWriter:  logWriter,
		redMetric:  redMetric,
		cluster:    cluster,
		tenantID:   "default",
		interval:   parseSyncInterval(os.Getenv("DEEPFLOW_SYNC_INTERVAL")),
		sampleRate: parseSampleRate(os.Getenv("DEEPFLOW_SPAN_SAMPLE_RATE")),
	}
	// 启动时恢复持久化的增量同步水位（重启不丢窗口）。持久化目录不可写则
	// 降级为内存态并告警（水位仅本次进程有效，不阻断同步）。
	s.watermarkPath = deepflowWatermarkPath()
	if err := os.MkdirAll(filepath.Dir(s.watermarkPath), 0o755); err != nil {
		log.Printf("DeepFlowSyncer: watermark dir %s not writable, watermark persistence degraded to in-memory: %v", filepath.Dir(s.watermarkPath), err)
		s.watermarkPath = ""
		return s
	}
	s.loadLastSync()
	return s
}

// loadLastSync 启动时从水位文件恢复上次成功同步时间（RFC3339，UTC）。
// 文件不存在或内容解析失败时保持零值——与现有首次启动行为完全一致
// （下次 Sync 仍从默认最近窗口开始）。读写在 lastSyncMu 保护下进行。
func (s *DeepFlowSyncer) loadLastSync() {
	if s.watermarkPath == "" {
		return
	}
	data, err := os.ReadFile(s.watermarkPath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("DeepFlowSyncer: read watermark %s failed, fallback to first-run: %v", s.watermarkPath, err)
		}
		return
	}
	ts, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
	if err != nil {
		log.Printf("DeepFlowSyncer: parse watermark %s failed (%q), fallback to first-run: %v", s.watermarkPath, strings.TrimSpace(string(data)), err)
		return
	}
	s.lastSyncMu.Lock()
	s.lastSyncTime = ts.UTC()
	s.lastSyncMu.Unlock()
	log.Printf("DeepFlowSyncer: restored last sync watermark %s from %s", s.lastSyncTime.Format(time.RFC3339), s.watermarkPath)
}

// persistLastSync 把同步水位原子写入本地文件（临时文件 + rename，避免写一半）。
// 写失败仅 log 告警、不阻断同步（继续走内存水位，下次成功时重试落盘）。
// 调用方需持有 lastSyncMu。
func (s *DeepFlowSyncer) persistLastSync(t time.Time) {
	if s.watermarkPath == "" {
		return
	}
	tmp := s.watermarkPath + ".tmp"
	data := []byte(t.UTC().Format(time.RFC3339) + "\n")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		log.Printf("DeepFlowSyncer: persist watermark %s failed (keeping in-memory): %v", s.watermarkPath, err)
		return
	}
	if err := os.Rename(tmp, s.watermarkPath); err != nil {
		log.Printf("DeepFlowSyncer: persist watermark rename %s failed (keeping in-memory): %v", s.watermarkPath, err)
		_ = os.Remove(tmp)
		return
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
	// namespace 映射：pod_service_map 的 pod_ns_id → pod_ns_map.name（K8s namespace）。
	// 修复：此前只取服务名不取 ns，导致 deepflow 同步的 span k8s_namespace 恒为空，
	// 服务全景无法按 observability 等真实 ns 过滤。
	sql := "SELECT time, s0.name AS src, n0.name AS src_ns, s1.name AS dst, n1.name AS dst_ns, " +
		"sum(request) AS calls, sum(client_error) + sum(server_error) + sum(timeout) AS errs " +
		"FROM flow_metrics.`application_map.1m` " +
		"LEFT JOIN flow_tag.pod_service_map s0 ON s0.id = auto_service_id_0 " +
		"LEFT JOIN flow_tag.pod_ns_map n0 ON n0.id = s0.pod_ns_id " +
		"LEFT JOIN flow_tag.pod_service_map s1 ON s1.id = auto_service_id_1 " +
		"LEFT JOIN flow_tag.pod_ns_map n1 ON n1.id = s1.pod_ns_id " +
		"WHERE s0.name IS NOT NULL AND s1.name IS NOT NULL AND time >= '" + startStr + "' " +
		"GROUP BY time, src, src_ns, dst, dst_ns"

	rows, err := s.queryDF(sql)
	if err != nil {
		return fmt.Errorf("query deepflow: %w", err)
	}

	count := 0
	for _, r := range rows {
		src, _ := r["src"].(string)
		dst, _ := r["dst"].(string)
		srcNS, _ := r["src_ns"].(string)
		dstNS, _ := r["dst_ns"].(string)
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
		// B1：LEGACY_WRITER_ENABLED=false 时 edgeWriter 为 untyped nil，跳过 CH 拓扑边写入
		// （span/log 路径已有 nil 保护，见 syncTraces）。
		if s.edgeWriter != nil {
			s.edgeWriter.AddEdge(&model.TopologyEdge{
				TenantID:        s.tenantID,
				ClusterID:       s.cluster,
				SourceService:   src,
				TargetService:   dst,
				SourceNamespace: srcNS,
				TargetNamespace: dstNS,
				TimeBucket:      tbUTC,
				CallCount:       calls,
				ErrorCount:      errs,
				AvgDurationNs:   0, // application_map 无时长字段，可后续扩展
				Date:            tbUTC.Format("2006-01-02"),
			})
			count++
		}
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
	s.persistLastSync(s.lastSyncTime)
	s.lastSyncMu.Unlock()
	return nil
}

// syncTraces 从 DeepFlow l7_flow_log 拉取最近的应用层调用，构造 span 写入 trace_spans。
// l7_flow_log 是每请求一条的真实 HTTP 调用记录（含源/目标服务、请求路径、响应码、时长）。
// 注意：RED 累加不依赖 spanWriter——LEGACY_WRITER_ENABLED=false 时 spanWriter 为 nil，
// 仅跳过 legacy ClickHouse span 写入，rows 遍历与 VM RED 累加照常执行。
func (s *DeepFlowSyncer) syncTraces(windowStart time.Time) error {
	// 从增量起点拉取真实调用（控制写入量，取前 2000 条）
	shanghai, errLoc := time.LoadLocation("Asia/Shanghai")
	if errLoc != nil {
		shanghai = time.FixedZone("CST", 8*3600)
	}
	// namespace 映射：与 Sync() 一致，通过 pod_service_map.pod_ns_id → pod_ns_map.name
	// 提取源/目标服务的 K8s namespace，写入 span.k8s_namespace（修复 ns 缺失）。
	windowStartStr := windowStart.In(shanghai).Format("2006-01-02 15:04:05")
	sql := "SELECT start_time, response_duration, s0.name AS src, n0.name AS src_ns, " +
		"s1.name AS dst, n1.name AS dst_ns, " +
		"request_resource, response_code " +
		"FROM flow_log.`l7_flow_log` " +
		"LEFT JOIN flow_tag.pod_service_map s0 ON s0.id = auto_service_id_0 " +
		"LEFT JOIN flow_tag.pod_ns_map n0 ON n0.id = s0.pod_ns_id " +
		"LEFT JOIN flow_tag.pod_service_map s1 ON s1.id = auto_service_id_1 " +
		"LEFT JOIN flow_tag.pod_ns_map n1 ON n1.id = s1.pod_ns_id " +
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
		// 源/目标服务的 K8s namespace（deepflow pod_ns_map 映射，无则空）
		srcNS, _ := r["src_ns"].(string)
		dstNS, _ := r["dst_ns"].(string)
		operation := fmt.Sprintf("%v", r["request_resource"])
		if operation == "" || operation == "<nil>" {
			operation = "unknown"
		}
		// 过滤 DeepFlow 内部/探针请求，避免污染业务日志
		if isInternalQuery(operation) {
			continue
		}
		// 抽样控制：按 sampleRate 概率丢弃整条调用（client+server 成对丢弃），
		// 避免每条 flow 双 span 导致 ClickHouse 写入量翻倍。抽样不拆散调用链。
		if s.sampleRate < 1.0 && rand.Float64() > s.sampleRate {
			count++
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
		// 修复 P1-4.1：构造真实跨服务调用链（src 客户端 span -> dst 服务端 span），
		// 使 trace 详情页能展示多服务瀑布图。
		srcName := fmt.Sprintf("%v", r["src"])
		traceID := hexHash(base, 32)
		clientSpanID := hexHash(base+"c", 16)
		serverSpanID := hexHash(base+"s", 16)
		clientDur := durNs + 800000
		clientSpan := &model.Span{
			TenantID:          s.tenantID,
			ClusterID:         s.cluster,
			TraceID:           traceID,
			SpanID:            clientSpanID,
			ParentSpanID:      "",
			ServiceName:       srcName,
			OperationName:     "HTTP " + operation,
			SpanKind:          "CLIENT",
			StatusCode:        uint8(code),
			StartTime:         ts.In(time.UTC),
			DurationNs:        clientDur,
			Attributes:        map[string]string{"http.url": operation, "peer": dst},
			HTTPMethod:        "GET",
			HTTPURL:           operation,
			HTTPStatusCode:    uint16(code),
			IsSlow:            boolToU8(clientDur >= 500000000),
			IsError:           isErr,
			ServiceInstanceID: srcName,
			K8sNamespace:      srcNS,
		}
		// legacy CH span 写入为可选：spanWriter 为 nil（LEGACY_WRITER_ENABLED=false）
		// 时跳过，不影响下方 RED 累加。
		if s.spanWriter != nil {
			s.spanWriter.Add(clientSpan)
		}
		serverSpan := &model.Span{
			TenantID:          s.tenantID,
			ClusterID:         s.cluster,
			TraceID:           traceID,
			SpanID:            serverSpanID,
			ParentSpanID:      clientSpanID,
			ServiceName:       dst,
			OperationName:     operation,
			SpanKind:          "SERVER",
			StatusCode:        uint8(code),
			StartTime:         ts.In(time.UTC),
			DurationNs:        durNs,
			Attributes:        map[string]string{"http.url": operation, "source": srcName},
			HTTPMethod:        "GET",
			HTTPURL:           operation,
			HTTPStatusCode:    uint16(code),
			IsSlow:            boolToU8(durNs >= 500000000),
			IsError:           isErr,
			ServiceInstanceID: srcName,
			K8sNamespace:      dstNS,
		}
		if s.spanWriter != nil {
			s.spanWriter.Add(serverSpan)
		}

		// 把真实服务流量累加为 VM 服务 RED 指标（service_requests_total / service_errors_total / 时长）
		// 供 anomaly 检测 / SLO 烧毁率等规则评估使用。cluster 标签区分多 k8s 环境。
		if s.redMetric != nil {
			s.redMetric.AddServiceREDForCluster(s.cluster, dst, isErr == 1, durNs)
		}

		// 构造访问日志（src 服务访问 dst 服务）写入 log_records，支撑 /logs 页
		if s.logWriter != nil {
			severity := "INFO"
			if isErr == 1 {
				severity = "ERROR"
			}
			s.logWriter.Add(&model.LogRecord{
				TenantID:    s.tenantID,
				ClusterID:   s.cluster,
				Timestamp:   ts.In(time.UTC),
				ServiceName: dst,
				Severity:    severity,
				Body:        fmt.Sprintf("%s -> %s %s [%d] %dms", srcName, dst, operation, code, durNs/1e6),
				Attributes:  map[string]string{"http.url": operation, "source": srcName, "http.status_code": fmt.Sprintf("%d", code)},
				TraceID:     traceID,
				SpanID:      serverSpanID,
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
