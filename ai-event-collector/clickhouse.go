package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// observability.k8s_events 表的 DDL 已迁入 ClickHouse versioned bootstrap
// （deploy/helm/aiops/files/clickhouse/migrations/*.sql），由 ClickHouse 初始化 Job
// 在部署时创建。event-collector 作为 runtime 不再执行 CREATE TABLE（V9.2 Phase 4
// P4.5：runtime 只做只读 schema 兼容校验）。

// Event 一条待写事件（K8s 事件与 IPMI SEL 统一结构）。
type Event struct {
	Ts              time.Time
	Namespace       string
	Kind            string
	Name            string
	Reason          string
	Type            string
	Message         string
	InvolvedObject  string
	SourceComponent string
	Source          string // 'k8s' | 'ipmi-sel'
	Node            string
}

// EventWriter 批量写入 ClickHouse（HTTP + TabSeparated，纯标准库无第三方依赖）。
// 失败批次进入内存重试队列，指数退避重试，不崩溃。
type EventWriter struct {
	endpoint   string
	tenantID   string
	clusterID  string
	batchSize  int
	flushEvery time.Duration

	mu     sync.Mutex
	buffer []*Event
	retry  []retryBatch // 写入失败待重试批次

	// wal（Phase 5）：可选崩溃安全持久化。非 nil 时 flush 失败先落盘再入重试，
	// 成功写入 CH 后 Ack；重启时从未 ack 水位恢复。
	wal *WAL

	httpClient *http.Client
	ctx        context.Context
	cancel     context.CancelFunc

	flushed      atomic.Int64 // 累计成功写入行数（日志统计）
	retryDropped atomic.Int64 // 重试队列满时丢弃的批次总数（/metrics 暴露）
}

// retryBatch 一条待重试批次。walSeq>0 时对应 WAL 中的条目 seq（成功后需 Ack）；
// walSeq=0 表示纯内存批次（无 WAL）。
type retryBatch struct {
	walSeq uint64
	rows   []byte
}

// NewEventWriter 创建写入器并确保 ClickHouse 表存在。
func NewEventWriter(cfg *Config) (*EventWriter, error) {
	ctx, cancel := context.WithCancel(context.Background())
	w := &EventWriter{
		endpoint:   chEndpoint(cfg),
		tenantID:   cfg.TenantID,
		clusterID:  cfg.ClusterID,
		batchSize:  cfg.BatchSize,
		flushEvery: time.Duration(cfg.FlushInterval) * time.Second,
		httpClient: newCHHTTPClient(),
		ctx:        ctx,
		cancel:     cancel,
	}
	// Phase 5：可选 WAL。配置 WAL_DIR 时启用崩溃安全持久化，并在启动时恢复
	// 上次未确认写入 CH 的批次到重试队列（跨重启不丢事件）。
	if cfg.WALDir != "" {
		wal, err := NewWAL(cfg.WALDir, "events-wal.log")
		if err != nil {
			cancel()
			return nil, fmt.Errorf("open WAL: %w", err)
		}
		w.wal = wal
		entries, rerr := wal.ReadAll()
		if rerr != nil {
			cancel()
			return nil, fmt.Errorf("replay WAL: %w", rerr)
		}
		for _, e := range entries {
			if v, derr := decodeWALValue(e.Value); derr == nil {
				w.retry = append(w.retry, retryBatch{walSeq: e.Seq, rows: v})
			}
		}
		if len(entries) > 0 {
			log.Printf("CH: recovered %d unacked batch(es) from WAL", len(entries))
		}
	}
	if err := w.ensureSchema(); err != nil {
		cancel()
		return nil, err
	}
	go w.flushLoop()
	go w.retryLoop()
	return w, nil
}

// chEndpoint 构造 ClickHouse HTTP endpoint。CLICKHOUSE_USER/PASSWORD 经 Secret 注入时
// 嵌入 URL userinfo，http.Client 自动以 Basic Auth 发送（与 ai-apm-ingest-go 风格一致）；
// 未配置时保持无凭据，兼容本地/dev。
func chEndpoint(cfg *Config) string {
	if cfg.CHUser != "" && cfg.CHPassword != "" {
		u := url.UserPassword(cfg.CHUser, cfg.CHPassword)
		return "http://" + u.String() + "@" + net.JoinHostPort(cfg.CHHost, fmt.Sprintf("%d", cfg.CHPort))
	}
	return fmt.Sprintf("http://%s:%d", cfg.CHHost, cfg.CHPort)
}

// chQueryParams 将查询编码进 ClickHouse HTTP query 参数，并关闭进度/流式响应头，
// 避免大批量写入时响应被进度头干扰。
func chQueryParams(query string) string {
	return "query=" + url.QueryEscape(query) + "&wait_end_of_query=1&send_progress_in_http_headers=0"
}

// ensureSchema 只读校验（V9.2 P4.5）：确认 observability 库与 k8s_events 表已由
// ClickHouse versioned bootstrap 建立。缺失则 fail-closed（不 CREATE，依赖迁移器）。
// 防止 runtime 在 schema 未就绪时静默写入失败。
func (w *EventWriter) ensureSchema() error {
	exists, err := w.tableExists("k8s_events")
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("CH schema not ready: observability.k8s_events missing (run ClickHouse bootstrap migration)")
	}
	log.Printf("CH: schema verified (observability.k8s_events)")
	return nil
}

// tableExists 只读检查 observability.<table> 是否存在。
func (w *EventWriter) tableExists(table string) (bool, error) {
	q := "SELECT count() FROM system.tables WHERE database = 'observability' AND name = " + chSQLString(table)
	u := w.endpoint + "/?" + chQueryParams(q)
	resp, err := w.httpClient.Get(u)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("clickhouse schema check error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body, _ := io.ReadAll(resp.Body)
	return strings.TrimSpace(string(body)) == "1", nil
}

// Add 追加一条事件到批缓冲；达到 batchSize 立即触发 flush。
func (w *EventWriter) Add(ev *Event) {
	if ev.Ts.IsZero() {
		ev.Ts = time.Now().UTC()
	}
	w.mu.Lock()
	w.buffer = append(w.buffer, ev)
	shouldFlush := len(w.buffer) >= w.batchSize
	w.mu.Unlock()
	if shouldFlush {
		w.flush()
	}
}

func (w *EventWriter) flushLoop() {
	ticker := time.NewTicker(w.flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-w.ctx.Done():
			w.flush()
			return
		case <-ticker.C:
			w.flush()
		}
	}
}

// flush 将当前缓冲序列化为批次写 CH；失败进入重试队列。
func (w *EventWriter) flush() {
	w.mu.Lock()
	if len(w.buffer) == 0 {
		w.mu.Unlock()
		return
	}
	batch := w.buffer
	w.buffer = make([]*Event, 0, w.batchSize)
	w.mu.Unlock()

	rows := w.serializeEvents(batch)
	if err := w.insertBatch(rows); err != nil {
		log.Printf("CH: write failed (queued for retry): %v", err)
		// 有 WAL 时先持久化再入重试，保证崩溃不丢已落盘事件。
		var seq uint64
		if w.wal != nil {
			if s, werr := w.wal.Append("event", rows); werr == nil {
				seq = s
			}
		}
		w.enqueueRetry(retryBatch{walSeq: seq, rows: rows})
		return
	}
	w.countFlushed(rows)
}

// enqueueRetry 将失败批次加入重试队列（上限 100 批，超限丢弃最旧，避免 CH 长时间不可用时内存无界增长）。
func (w *EventWriter) enqueueRetry(b retryBatch) {
	w.mu.Lock()
	defer w.mu.Unlock()
	const maxRetryBatches = 100
	if len(w.retry) >= maxRetryBatches {
		dropped := w.retry[0]
		w.retry = w.retry[1:]
		// 有 WAL 时 Ack 被丢弃批次，使其不再从 WAL 重复恢复。
		if w.wal != nil && dropped.walSeq > 0 {
			w.wal.Ack(dropped.walSeq)
		}
		w.retryDropped.Add(1)
		log.Printf("CH: retry queue full (%d), dropping oldest batch", maxRetryBatches)
	}
	w.retry = append(w.retry, b)
}

// retryLoop 周期性重试失败批次，指数退避（1s 起，翻倍至上限 60s）。
func (w *EventWriter) retryLoop() {
	backoff := time.Second
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	for {
		select {
		case <-w.ctx.Done():
			return
		case <-timer.C:
		}
		w.mu.Lock()
		hasRetry := len(w.retry) > 0
		w.mu.Unlock()
		if !hasRetry {
			backoff = time.Second
		} else if err := w.flushRetry(); err != nil {
			backoff *= 2
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
			log.Printf("CH: retry failed (next in %s): %v", backoff, err)
		} else {
			backoff = time.Second
		}
		timer.Reset(backoff)
	}
}

// flushRetry 重试所有失败批次；全部成功返回 nil。
func (w *EventWriter) flushRetry() error {
	w.mu.Lock()
	pending := w.retry
	w.retry = nil
	w.mu.Unlock()

	var failed []retryBatch
	var firstErr error
	for _, b := range pending {
		if err := w.insertBatch(b.rows); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			failed = append(failed, b)
			continue
		}
		// 成功写入 CH：有 WAL 时 Ack，不再从 WAL 恢复。
		if w.wal != nil && b.walSeq > 0 {
			w.wal.Ack(b.walSeq)
		}
		w.countFlushed(b.rows)
	}
	if len(failed) > 0 {
		w.mu.Lock()
		w.retry = append(failed, w.retry...)
		w.mu.Unlock()
		return firstErr
	}
	// 全部成功：压缩 WAL，回收已 ack 空间。
	if w.wal != nil {
		w.wal.Compact()
	}
	return nil
}

// serializeEvents 将事件批量序列化为 TabSeparated 行。所有文本字段经 escapeTSV 转义，
// 防止含 \t \n \r \\ 时行被拆裂/字段错位导致 ClickHouse 解析失败。
func (w *EventWriter) serializeEvents(events []*Event) []byte {
	var buf bytes.Buffer
	for _, ev := range events {
		ts := ev.Ts
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		fmt.Fprintf(&buf, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			escapeTSV(w.tenantID),
			escapeTSV(w.clusterID),
			ts.Format("2006-01-02 15:04:05.000000000"),
			escapeTSV(ev.Namespace),
			escapeTSV(ev.Kind),
			escapeTSV(ev.Name),
			escapeTSV(ev.Reason),
			escapeTSV(ev.Type),
			escapeTSV(ev.Message),
			escapeTSV(ev.InvolvedObject),
			escapeTSV(ev.SourceComponent),
			escapeTSV(ev.Source),
			escapeTSV(ev.Node),
			ts.Truncate(time.Minute).Format("2006-01-02 15:04:05"),
		)
	}
	return buf.Bytes()
}

// insertBatch 以 HTTP POST + FORMAT TabSeparated 写入一行批次。
func (w *EventWriter) insertBatch(rows []byte) error {
	u := w.endpoint + "/?" + chQueryParams("INSERT INTO observability.k8s_events FORMAT TabSeparated")
	resp, err := w.httpClient.Post(u, "text/plain", bytes.NewReader(rows))
	if err != nil {
		return fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("clickhouse error %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// latestTSQuery 构造 checkpoint 查询，key 限定 tenant_id + cluster_id + source（V9.2 §71：
// checkpoint key = tenant+cluster+source）。三个字段缺一不可，避免多租户/多集群串读断点。
func latestTSQuery(source, tenantID, clusterID string) string {
	return "SELECT max(ts) FROM observability.k8s_events WHERE source = " + chSQLString(source) +
		" AND tenant_id = " + chSQLString(tenantID) +
		" AND cluster_id = " + chSQLString(clusterID)
}

// chSQLString quotes a ClickHouse string literal. Backslashes are escaped
// before quotes so user/config-derived scope values cannot terminate it.
func chSQLString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

// QueryLatestTS 查询指定 source 在 CH 中的最新事件时间戳（断点续采 checkpoint），
// checkpoint key 限定当前 writer 的 tenant+cluster（已由启动时 scope.Validate 强校验）。
// 无数据返回零值 time.Time。
func (w *EventWriter) QueryLatestTS(source string) (time.Time, error) {
	q := latestTSQuery(source, w.tenantID, w.clusterID)
	u := w.endpoint + "/?" + chQueryParams(q)
	resp, err := w.httpClient.Get(u)
	if err != nil {
		return time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return time.Time{}, fmt.Errorf("clickhouse error %d: %s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return time.Time{}, err
	}
	s := strings.TrimSpace(string(body))
	if s == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable max(ts) %q", s)
}

// countFlushed 统计一次成功写入的行数（以换行数为准）。
func (w *EventWriter) countFlushed(rows []byte) {
	n := 0
	for _, c := range rows {
		if c == '\n' {
			n++
		}
	}
	w.flushed.Add(int64(n))
	log.Printf("CH: flushed %d events (total %d)", n, w.flushed.Load())
}

// Ping 快速探测 ClickHouse 连通性（短超时，供 /health 探针使用）。
func (w *EventWriter) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	u := w.endpoint + "/?" + chQueryParams("SELECT 1")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("clickhouse error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// RetryQueueSize 返回当前重试队列批次数（供 /health 与 /metrics 使用）。
func (w *EventWriter) RetryQueueSize() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.retry)
}

// FlushedTotal 返回累计成功写入行数（/metrics 暴露）。
func (w *EventWriter) FlushedTotal() int64 {
	return w.flushed.Load()
}

// RetryDroppedTotal 返回重试队列满时丢弃的批次总数（/metrics 暴露）。
func (w *EventWriter) RetryDroppedTotal() int64 {
	return w.retryDropped.Load()
}

// WALPendingRecords 返回 WAL 未 ack 记录数（backlog 观测；未配置 WAL 返回 0）。
func (w *EventWriter) WALPendingRecords() int64 {
	if w.wal == nil {
		return 0
	}
	return int64(w.wal.PendingStats().Records)
}

// WALPendingBytes 返回 WAL 未 ack 记录的 value 字节总数（backlog 观测）。
func (w *EventWriter) WALPendingBytes() int64 {
	if w.wal == nil {
		return 0
	}
	return int64(w.wal.PendingStats().Bytes)
}

// WALOldestPendingAgeSeconds 返回最旧未 ack WAL 记录滞留秒数（backlog 年龄）。
func (w *EventWriter) WALOldestPendingAgeSeconds() int64 {
	if w.wal == nil {
		return 0
	}
	return int64(w.wal.OldestAgeSeconds())
}

// Close 取消后台循环并尽力冲刷缓冲与重试队列，最后关闭 WAL。
func (w *EventWriter) Close() {
	w.cancel()
	w.flush()
	w.flushRetry()
	if w.wal != nil {
		w.wal.Close()
	}
}

// decodeWALValue 将 WAL 条目中 base64 编码的批次行解码回原始 bytes。
// 解码失败返回 err，调用方忽略该条目（corrupt 条目不阻塞恢复）。
func decodeWALValue(v string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// newCHHTTPClient 返回针对 ClickHouse 高频写入优化的 HTTP 客户端。
// 显式配置 Transport 连接池，避免使用 Go 默认（MaxIdleConnsPerHost=2）导致连接复用不足。
func newCHHTTPClient() *http.Client {
	transport := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   50,
		MaxConnsPerHost:       100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: transport}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	return s
}

// escapeTSV 按 ClickHouse TabSeparated 格式转义字段内容，防止含 \t \n \r \\ 时行被拆裂。
func escapeTSV(s string) string {
	if !strings.ContainsAny(s, "\\\t\n\r") {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
