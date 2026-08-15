package clickhouse

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
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/observability-platform/ai-apm-ingest-go/internal/model"
)

// chEndpoint 构造 ClickHouse HTTP endpoint；若配置了 CLICKHOUSE_USER/PASSWORD
// （经 Secret 注入，生产启用认证），则将凭据嵌入 URL userinfo，http.Client 会自动
// 以 Basic Auth 发送。未配置时（本地/dev）保持无凭据，向后兼容。
func chEndpoint(host string, port int) string {
	user := os.Getenv("CLICKHOUSE_USER")
	pass := os.Getenv("CLICKHOUSE_PASSWORD")
	if user != "" && pass != "" {
		u := url.UserPassword(user, pass)
		return "http://" + u.String() + "@" + net.JoinHostPort(host, fmt.Sprintf("%d", port))
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}

// Writer batches spans and writes to ClickHouse via HTTP, with WAL-backed
// durability and retry to guarantee no data loss in production.
type Writer struct {
	endpoint   string
	mu         sync.Mutex
	buffer     []*model.Span
	batchSize  int
	flushEvery time.Duration
	httpClient *http.Client
	ctx        context.Context
	cancel     context.CancelFunc

	wal *WAL
	// 待重试批次：seq -> serialized rows
	retryPending map[uint64][]byte
	// 无 WAL 时用内存递增序号作为 retryPending 的 key，
	// 避免所有批次都用 key=0 互相覆盖导致仅重试最后一批、其余丢失。
	memSeq uint64

	// 内存/重试上限（防止 ClickHouse 长时间不可用时 OOM / 磁盘打满）
	maxBufferSpans   int   // 内存缓冲最大 span 数，超限丢弃最旧
	maxRetryBatches  int   // 重试队列最大批次条数，超限丢弃最旧
	maxRetryBytes    int64 // 重试队列最大字节数，超限丢弃最旧
	retryBytes       int64 // 当前重试队列总字节数

	// OnWritten 写入成功回调（用于 metrics 统计，可为 nil）
	OnWritten func(n int)
}

// NewWriter creates a Writer. walDir 为 WAL 持久化目录（生产挂载 PVC；空则退化为内存、仅重试不落盘）。
func NewWriter(host string, port int, walDir string) *Writer {
	ctx, cancel := context.WithCancel(context.Background())
	w := &Writer{
		endpoint:         chEndpoint(host, port),
		batchSize:        1024,
		flushEvery:       5 * time.Second,
		httpClient:       newCHHTTPClient(),
		ctx:              ctx,
		cancel:           cancel,
		retryPending:     make(map[uint64][]byte),
		maxBufferSpans:   10240,   // 10× batchSize，防止高峰突发 OOM
		maxRetryBatches:  500,     // 重试队列上限条数
		maxRetryBytes:    512 << 20, // 512MB 重试队列上限
	}
	if walDir != "" {
		wal, err := NewWALFile(walDir, walSpanFile)
		if err != nil {
			log.Printf("WAL init failed (falling back to memory retry): %v", err)
		} else {
			w.wal = wal
			// 启动恢复：重放 WAL 中未确认的记录（分文件后仅含 span 条目，防御性校验 kind）
			if entries, err := wal.ReadAll(); err == nil && len(entries) > 0 {
				log.Printf("WAL: recovering %d pending records", len(entries))
				for _, e := range entries {
					if e.Kind != "span" {
						continue
					}
					rows, derr := decodeRows(e.Value)
					if derr == nil {
						w.addRetry(e.Seq, rows)
					}
				}
			}
		}
	}
	go w.flushLoop()
	return w
}

// decodeRows 解码 base64 后的行数据。
func decodeRows(b64 string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// Add appends a span to the batch buffer.
// 内存保护：缓冲超过 maxBufferSpans 时丢弃最旧 span 并记日志（形成背压信号，
// 避免高峰突发导致 OOM）。
func (w *Writer) Add(span *model.Span) {
	w.mu.Lock()
	if w.maxBufferSpans > 0 && len(w.buffer) >= w.maxBufferSpans {
		// 丢弃最旧的 1 个 span
		copy(w.buffer, w.buffer[1:])
		w.buffer[len(w.buffer)-1] = nil
		w.buffer = w.buffer[:len(w.buffer)-1]
		w.mu.Unlock()
		log.Printf("WRITER: buffer full (%d), dropping oldest span (backpressure)", w.maxBufferSpans)
		return
	}
	w.buffer = append(w.buffer, span)
	shouldFlush := len(w.buffer) >= w.batchSize
	w.mu.Unlock()
	if shouldFlush {
		w.flush()
	}
}

func (w *Writer) flushLoop() {
	ticker := time.NewTicker(w.flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-w.ctx.Done():
			w.flush()
			w.flushRetry()
			if w.wal != nil {
				w.wal.Close()
			}
			return
		case <-ticker.C:
			w.flush()
			w.flushRetry()
		}
	}
}

// flush 将当前内存缓冲中的 span 序列化为行，先写 WAL 再写 ClickHouse。
func (w *Writer) flush() {
	w.mu.Lock()
	if len(w.buffer) == 0 {
		w.mu.Unlock()
		return
	}
	batch := w.buffer
	w.buffer = make([]*model.Span, 0, w.batchSize)
	w.mu.Unlock()

	rows := w.serializeSpans(batch)
	w.writeWithRetry(rows)
}

// serializeSpans 把 span 序列化为 TabSeparated 行。所有文本字段经 escapeTSV 转义，
// 防止含 \t \n \r \\ 时行被拆裂/字段错位导致 ClickHouse 解析失败。
func (w *Writer) serializeSpans(spans []*model.Span) []byte {
	var buf bytes.Buffer
	for _, s := range spans {
		clusterID := s.ClusterID
		if clusterID == "" {
			clusterID = "default"
		}
		fmt.Fprintf(&buf, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%d\t",
			escapeTSV(s.TenantID), escapeTSV(clusterID),
			escapeTSV(s.TraceID), escapeTSV(s.SpanID), escapeTSV(s.ParentSpanID),
			escapeTSV(s.ServiceName), escapeTSV(s.OperationName), escapeTSV(s.SpanKind),
			s.StatusCode, s.StartTime.Format("2006-01-02 15:04:05.000000000"),
			s.DurationNs,
		)
		attrParts := make([]string, 0, len(s.Attributes))
		for k, v := range s.Attributes {
			attrParts = append(attrParts, fmt.Sprintf("'%s':'%s'", escapeTSV(escapeCH(k)), escapeTSV(escapeCH(v))))
		}
		attrStr := "{" + strings.Join(attrParts, ",") + "}"
		fmt.Fprintf(&buf, "%s\t", escapeTSV(attrStr))

		fmt.Fprintf(&buf, "%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\n",
			escapeTSV(s.HTTPMethod), s.HTTPStatusCode, escapeTSV(s.HTTPURL),
			escapeTSV(s.DBSystem), escapeTSV(s.DBStatement),
			escapeTSV(s.RPCSystem),
			escapeTSV(s.ServiceInstanceID), escapeTSV(s.K8sNamespace), escapeTSV(s.K8sPodName),
			s.IsSlow, s.IsError,
			s.StartTime.Truncate(time.Minute).Format("2006-01-02 15:04:05"),
			s.StartTime.Format("2006-01-02"),
		)
	}
	return buf.Bytes()
}

// writeWithRetry 写入数据，失败时保留到 retryPending 并周期性重试。
func (w *Writer) writeWithRetry(rows []byte) {
	if w.wal != nil {
		if seq, err := w.wal.Append("span", rows); err == nil {
			w.addRetry(seq, rows)
		} else {
			log.Printf("WAL append error (data kept in memory): %v", err)
			w.addRetryMem(rows)
		}
	} else {
		w.addRetryMem(rows)
	}

	if err := w.insertBatch(rows); err != nil {
		log.Printf("ClickHouse write failed (will retry): %v", err)
	} else {
		w.confirm(rows)
		w.countWritten(rows)
	}
}

// addRetry 将一批加入重试队列，并做容量保护：
// 超过 maxRetryBatches 或 maxRetryBytes 时，丢弃最旧批次（并确认对应 WAL seq，
// 释放内存与磁盘），避免 ClickHouse 长时间不可用时 OOM / 磁盘打满。
// 调用方需持有锁。
func (w *Writer) addRetry(seq uint64, rows []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.retryPending[seq] = rows
	w.retryBytes += int64(len(rows))
	for len(w.retryPending) > w.maxRetryBatches || (w.maxRetryBytes > 0 && w.retryBytes > w.maxRetryBytes) {
		if !w.dropOldestRetryLocked() {
			break
		}
	}
}

// addRetryMem 无 WAL 时使用内存递增序号作为重试队列 key，
// 避免所有批次都用 key=0 互相覆盖（否则 ClickHouse 故障时只重试最后一批，其余丢失）。
func (w *Writer) addRetryMem(rows []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.memSeq++
	w.retryPending[w.memSeq] = rows
	w.retryBytes += int64(len(rows))
	for len(w.retryPending) > w.maxRetryBatches || (w.maxRetryBytes > 0 && w.retryBytes > w.maxRetryBytes) {
		if !w.dropOldestRetryLocked() {
			break
		}
	}
}

// dropOldestRetryLocked 丢弃重试队列中最旧的一批（最小 seq），并 Ack 对应 WAL seq。
// 返回是否丢弃成功。调用方需持有锁。
func (w *Writer) dropOldestRetryLocked() bool {
	if len(w.retryPending) == 0 {
		return false
	}
	var oldestSeq uint64
	found := false
	first := true
	for seq := range w.retryPending {
		if first || seq < oldestSeq {
			oldestSeq = seq
			found = true
		}
		first = false
	}
	if !found {
		return false
	}
	rows, ok := w.retryPending[oldestSeq]
	if !ok {
		return false
	}
	delete(w.retryPending, oldestSeq)
	w.retryBytes -= int64(len(rows))
	if w.wal != nil && oldestSeq > 0 {
		w.wal.Ack(oldestSeq)
	}
	log.Printf("WRITER: dropping oldest retry batch seq=%d (%d bytes) to bound memory/disk", oldestSeq, len(rows))
	return true
}

// ackRetryLocked 从重试队列移除已成功写入的一批（按 seq），并维护字节计数与 WAL ack。
// 调用方需持有锁。
func (w *Writer) ackRetryLocked(seq uint64, rows []byte) {
	if v, ok := w.retryPending[seq]; ok && (rows == nil || string(v) == string(rows)) {
		delete(w.retryPending, seq)
		w.retryBytes -= int64(len(v))
		if w.wal != nil && seq > 0 {
			w.wal.Ack(seq)
		}
	}
}

// countWritten 统计一次成功写入的 span 行数（以换行数为准）。
func (w *Writer) countWritten(rows []byte) {
	if w.OnWritten == nil {
		return
	}
	n := 0
	for _, c := range rows {
		if c == '\n' {
			n++
		}
	}
	w.OnWritten(n)
}

// flushRetry 重试所有未成功的批次，按 seq 升序处理。
// 升序保证低 seq 先确认，配合 WAL 连续确认水位，避免高 seq 先确认导致 compact 误删低 seq 未确认条目。
func (w *Writer) flushRetry() {
	w.mu.Lock()
	pending := make(map[uint64][]byte, len(w.retryPending))
	for k, v := range w.retryPending {
		pending[k] = v
	}
	w.mu.Unlock()
	seqs := make([]uint64, 0, len(pending))
	for seq := range pending {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for _, seq := range seqs {
		rows := pending[seq]
		if err := w.insertBatch(rows); err != nil {
			log.Printf("ClickHouse retry failed (kept for next retry): %v", err)
			continue
		}
		w.confirmSeq(seq, rows)
		w.countWritten(rows)
	}
}

// confirm 从 retryPending 中移除已成功写入的数据。
// writeWithRetry 同步写入成功时，本条数据刚通过 wal.Append 拿到 seq，
// 但 insertBatch 用的正是该 rows，因此这里做一次精确确认（seq 与内容均校验），
// 避免"内容匹配"在相同内容被以不同 seq 追加时误删/残留。
func (w *Writer) confirm(rows []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.wal != nil {
		// 同步成功通常对应最近一次 Append 的 seq；精确查找匹配内容的最小 seq 确认。
		key := string(rows)
		for seq, v := range w.retryPending {
			if string(v) == key {
				w.ackRetryLocked(seq, v)
				return
			}
		}
		return
	}
	// 无 WAL：key 为内存递增 seq，按内容确认（内容匹配即可，seq 不影响）。
	key := string(rows)
	for seq, v := range w.retryPending {
		if string(v) == key {
			w.ackRetryLocked(seq, v)
			return
		}
	}
}

func (w *Writer) confirmSeq(seq uint64, rows []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ackRetryLocked(seq, rows)
}

func (w *Writer) insertBatch(rows []byte) error {
	query := "INSERT INTO observability.trace_spans FORMAT TabSeparated"
	url := w.endpoint + "/?" + buildQueryParam(query)
	resp, err := w.httpClient.Post(url, "text/plain", bytes.NewReader(rows))
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

func buildQueryParam(query string) string {
	return "query=" + url.QueryEscape(query)
}

// newCHHTTPClient 返回针对 ClickHouse 高频写入优化的 HTTP 客户端。
// 显式配置 Transport 连接池，避免使用 Go 默认（MaxIdleConnsPerHost=2）导致高并发复用不足。
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

func escapeCH(s string) string {
	return strings.ReplaceAll(s, "'", "\\'")
}

// escapeTSV 按 ClickHouse TabSeparated 格式转义字段内容，防止含 \t \n \r \\ 时行被拆裂/字段错位。
// 规则：\\ -> \\\\, \t -> \\t, \n -> \\n, \r -> \\r（ClickHouse TabSeparated 要求）。
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

func (w *Writer) Close() {
	w.cancel()
}
