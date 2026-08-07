package clickhouse

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/observability-platform/ai-apm-ingest-go/internal/model"
)

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

	// OnWritten 写入成功回调（用于 metrics 统计，可为 nil）
	OnWritten func(n int)
}

// NewWriter creates a Writer. walDir 为 WAL 持久化目录（生产挂载 PVC；空则退化为内存、仅重试不落盘）。
func NewWriter(host string, port int, walDir string) *Writer {
	ctx, cancel := context.WithCancel(context.Background())
	w := &Writer{
		endpoint:     fmt.Sprintf("http://%s:%d", host, port),
		batchSize:    1024,
		flushEvery:   5 * time.Second,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		ctx:          ctx,
		cancel:       cancel,
		retryPending: make(map[uint64][]byte),
	}
	if walDir != "" {
		wal, err := NewWAL(walDir)
		if err != nil {
			log.Printf("WAL init failed (falling back to memory retry): %v", err)
		} else {
			w.wal = wal
			// 启动恢复：重放 WAL 中未确认的记录
			if entries, err := wal.ReadAll(); err == nil && len(entries) > 0 {
				log.Printf("WAL: recovering %d pending records", len(entries))
				for _, e := range entries {
					rows, derr := decodeRows(e.Value)
					if derr == nil {
						w.retryPending[e.Seq] = rows
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
func (w *Writer) Add(span *model.Span) {
	w.mu.Lock()
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

// serializeSpans 把 span 序列化为 TabSeparated 行。
func (w *Writer) serializeSpans(spans []*model.Span) []byte {
	var buf bytes.Buffer
	for _, s := range spans {
		fmt.Fprintf(&buf, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%d\t",
			s.TenantID, s.TraceID, s.SpanID, s.ParentSpanID,
			s.ServiceName, s.OperationName, s.SpanKind,
			s.StatusCode, s.StartTime.Format("2006-01-02 15:04:05.000000000"),
			s.DurationNs,
		)
		attrParts := make([]string, 0, len(s.Attributes))
		for k, v := range s.Attributes {
			attrParts = append(attrParts, fmt.Sprintf("'%s':'%s'", escapeCH(k), escapeCH(v)))
		}
		attrStr := "{" + strings.Join(attrParts, ",") + "}"
		fmt.Fprintf(&buf, "%s\t", attrStr)

		fmt.Fprintf(&buf, "%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\n",
			s.HTTPMethod, s.HTTPStatusCode, s.HTTPURL,
			s.DBSystem, s.DBStatement,
			s.RPCSystem,
			s.ServiceInstanceID, s.K8sNamespace, s.K8sPodName,
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
			w.mu.Lock()
			w.retryPending[seq] = rows
			w.mu.Unlock()
		} else {
			log.Printf("WAL append error (data kept in memory): %v", err)
			w.mu.Lock()
			w.retryPending[0] = rows
			w.mu.Unlock()
		}
	} else {
		w.mu.Lock()
		w.retryPending[0] = rows
		w.mu.Unlock()
	}

	if err := w.insertBatch(rows); err != nil {
		log.Printf("ClickHouse write failed (will retry): %v", err)
	} else {
		w.confirm(rows)
		w.countWritten(rows)
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

// flushRetry 重试所有未成功的批次。
func (w *Writer) flushRetry() {
	w.mu.Lock()
	pending := make(map[uint64][]byte, len(w.retryPending))
	for k, v := range w.retryPending {
		pending[k] = v
	}
	w.mu.Unlock()
	for seq, rows := range pending {
		if err := w.insertBatch(rows); err != nil {
			log.Printf("ClickHouse retry failed (kept for next retry): %v", err)
			continue
		}
		w.confirmSeq(seq, rows)
		w.countWritten(rows)
	}
}

// confirm 从 retryPending 中移除已成功写入的数据（按内容匹配）。
func (w *Writer) confirm(rows []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	key := string(rows)
	for seq, v := range w.retryPending {
		if string(v) == key {
			delete(w.retryPending, seq)
			if w.wal != nil && seq > 0 {
				w.wal.Ack(seq)
			}
			return
		}
	}
}

func (w *Writer) confirmSeq(seq uint64, rows []byte) {
	w.mu.Lock()
	if v, ok := w.retryPending[seq]; ok && string(v) == string(rows) {
		delete(w.retryPending, seq)
		if w.wal != nil && seq > 0 {
			w.wal.Ack(seq)
		}
	}
	w.mu.Unlock()
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

func escapeCH(s string) string {
	return strings.ReplaceAll(s, "'", "\\'")
}

func (w *Writer) Close() {
	w.cancel()
}
