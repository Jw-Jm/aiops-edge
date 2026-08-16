package main

import (
	"bytes"
	"context"
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

// chDDL observability.k8s_events 建表 DDL（组件启动时 CREATE TABLE IF NOT EXISTS 自建）。
const chDDL = `CREATE TABLE IF NOT EXISTS observability.k8s_events (
  tenant_id String,
  cluster_id String DEFAULT 'default',
  ts DateTime64(9),
  namespace String,
  kind String,
  name String,
  reason String,
  type String,
  message String,
  involved_object String,
  source_component String,
  source String,
  node String DEFAULT '',
  time_bucket DateTime
) ENGINE = ReplacingMergeTree
ORDER BY (tenant_id, cluster_id, ts, involved_object, reason)
TTL time_bucket + INTERVAL 30 DAY`

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
	retry  [][]byte // 写入失败待重试批次（序列化后的行）

	httpClient *http.Client
	ctx        context.Context
	cancel     context.CancelFunc

	flushed atomic.Int64 // 累计成功写入行数（日志统计）
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

// ensureSchema 启动时自建 database 与表（幂等）。
func (w *EventWriter) ensureSchema() error {
	queries := []string{
		"CREATE DATABASE IF NOT EXISTS observability",
		chDDL,
	}
	for _, q := range queries {
		u := w.endpoint + "/?" + chQueryParams(q)
		resp, err := w.httpClient.Post(u, "text/plain", nil)
		if err != nil {
			return fmt.Errorf("schema query %q: %w", firstLine(q), err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("schema query %q failed: %d %s", firstLine(q), resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}
	log.Printf("CH: schema ensured (observability.k8s_events)")
	return nil
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
		w.enqueueRetry(rows)
		return
	}
	w.countFlushed(rows)
}

// enqueueRetry 将失败批次加入重试队列（上限 100 批，超限丢弃最旧，避免 CH 长时间不可用时内存无界增长）。
func (w *EventWriter) enqueueRetry(rows []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	const maxRetryBatches = 100
	if len(w.retry) >= maxRetryBatches {
		w.retry = w.retry[1:]
		log.Printf("CH: retry queue full (%d), dropping oldest batch", maxRetryBatches)
	}
	w.retry = append(w.retry, rows)
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

	var failed [][]byte
	var firstErr error
	for _, rows := range pending {
		if err := w.insertBatch(rows); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			failed = append(failed, rows)
			continue
		}
		w.countFlushed(rows)
	}
	if len(failed) > 0 {
		w.mu.Lock()
		w.retry = append(failed, w.retry...)
		w.mu.Unlock()
		return firstErr
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

// QueryLatestTS 查询指定 source 在 CH 中的最新事件时间戳（断点续采 checkpoint）。
// 无数据返回零值 time.Time。
func (w *EventWriter) QueryLatestTS(source string) (time.Time, error) {
	q := fmt.Sprintf("SELECT max(ts) FROM observability.k8s_events WHERE source = '%s' AND cluster_id = '%s'", source, w.clusterID)
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

// Close 取消后台循环并尽力冲刷缓冲与重试队列。
func (w *EventWriter) Close() {
	w.cancel()
	w.flush()
	w.flushRetry()
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
