package clickhouse

import (
	"bytes"
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

// LogWriter batches log records and writes to ClickHouse via HTTP.
// 生产约束：写入失败不静默丢弃，保留到内存重试队列周期性重试；
// 重试队列带容量上限（超限丢弃最旧，避免 ClickHouse 不可用时 OOM）。
type LogWriter struct {
	endpoint   string
	mu         sync.Mutex
	buffer     []*model.LogRecord
	batchSize  int
	flushEvery time.Duration
	httpClient *http.Client
	stopCh     chan struct{}
	doneCh     chan struct{}

	// 内存缓冲上限（条数），超限丢弃最旧，形成背压防 OOM
	maxBufferRecords int
	// 待重试的序列化批次（序列化后的 TabSeparated bytes）
	retryPending  [][]byte
	maxRetryBatches int
}

// NewLogWriter creates a new LogWriter
func NewLogWriter(host string, port int) *LogWriter {
	w := &LogWriter{
		endpoint:         chEndpoint(host, port),
		batchSize:        1024,
		flushEvery:       5 * time.Second,
		httpClient:       newCHHTTPClient(),
		stopCh:           make(chan struct{}),
		doneCh:           make(chan struct{}),
		maxBufferRecords: 10240,
		retryPending:     make([][]byte, 0, 64),
		maxRetryBatches:  500,
	}
	go w.flushLoop()
	return w
}

// Add adds a LogRecord to the batch buffer.
// 内存保护：缓冲超过 maxBufferRecords 时丢弃最旧记录并记日志。
func (w *LogWriter) Add(lr *model.LogRecord) {
	w.mu.Lock()
	if w.maxBufferRecords > 0 && len(w.buffer) >= w.maxBufferRecords {
		copy(w.buffer, w.buffer[1:])
		w.buffer[len(w.buffer)-1] = nil
		w.buffer = w.buffer[:len(w.buffer)-1]
		w.mu.Unlock()
		log.Printf("LOGWRITER: buffer full (%d), dropping oldest record (backpressure)", w.maxBufferRecords)
		return
	}
	w.buffer = append(w.buffer, lr)
	shouldFlush := len(w.buffer) >= w.batchSize
	w.mu.Unlock()
	if shouldFlush {
		w.flush()
	}
}

func (w *LogWriter) flushLoop() {
	ticker := time.NewTicker(w.flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-w.stopCh:
			w.flush()
			w.flushRetry()
			close(w.doneCh)
			return
		case <-ticker.C:
			w.flush()
			w.flushRetry()
		}
	}
}

func (w *LogWriter) flush() {
	w.mu.Lock()
	if len(w.buffer) == 0 {
		w.mu.Unlock()
		return
	}
	batch := w.buffer
	w.buffer = make([]*model.LogRecord, 0, w.batchSize)
	w.mu.Unlock()

	rows := w.serializeRecords(batch)
	if err := w.insertBatchBytes(rows); err != nil {
		// 不丢弃：保留到重试队列
		log.Printf("ClickHouse log write failed (will retry): %v", err)
		w.addRetry(rows)
	} else {
		log.Printf("ClickHouse: wrote %d log records", len(batch))
	}
}

// addRetry 将序列化批次加入重试队列，超上限时丢弃最旧（防 OOM）。
func (w *LogWriter) addRetry(rows []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.retryPending = append(w.retryPending, rows)
	if len(w.retryPending) > w.maxRetryBatches {
		drop := w.retryPending[0]
		w.retryPending = w.retryPending[1:]
		log.Printf("LOGWRITER: retry queue full (%d), dropping oldest retry batch (%d bytes)", w.maxRetryBatches, len(drop))
	}
}

// flushRetry 重试所有未成功的日志批次。
func (w *LogWriter) flushRetry() {
	w.mu.Lock()
	if len(w.retryPending) == 0 {
		w.mu.Unlock()
		return
	}
	pending := make([][]byte, len(w.retryPending))
	copy(pending, w.retryPending)
	w.retryPending = w.retryPending[:0]
	w.mu.Unlock()

	var failed [][]byte
	for _, rows := range pending {
		if err := w.insertBatchBytes(rows); err != nil {
			log.Printf("ClickHouse log retry failed (kept for next retry): %v", err)
			failed = append(failed, rows)
			continue
		}
	}
	if len(failed) > 0 {
		w.mu.Lock()
		for _, rows := range failed {
			w.retryPending = append(w.retryPending, rows)
			if len(w.retryPending) > w.maxRetryBatches {
				w.retryPending = w.retryPending[1:]
			}
		}
		w.mu.Unlock()
	}
}

// serializeRecords 将日志记录序列化为 TabSeparated 行。
func (w *LogWriter) serializeRecords(records []*model.LogRecord) []byte {
	var buf bytes.Buffer
	for _, r := range records {
		attrParts := make([]string, 0, len(r.Attributes))
		for k, v := range r.Attributes {
			attrParts = append(attrParts, fmt.Sprintf("'%s':'%s'", escapeTSV(escapeCH(k)), escapeTSV(escapeCH(v))))
		}
		attrStr := "{" + strings.Join(attrParts, ",") + "}"

		fmt.Fprintf(&buf, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			escapeTSV(r.TenantID),
			r.Timestamp.Format("2006-01-02 15:04:05.000000000"),
			escapeTSV(r.ServiceName),
			escapeTSV(r.Severity),
			escapeTSV(r.Body),
			escapeTSV(attrStr),
			escapeTSV(r.TraceID),
			escapeTSV(r.SpanID),
			r.TimeBucket,
			r.Date,
		)
	}
	return buf.Bytes()
}

// insertBatchBytes 将序列化好的 TabSeparated 行批量写入 ClickHouse。
func (w *LogWriter) insertBatchBytes(rows []byte) error {
	query := "INSERT INTO observability.log_records FORMAT TabSeparated"
	u := w.endpoint + "/?" + "query=" + url.QueryEscape(query)

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

// Close stops the flush loop and performs a final flush
func (w *LogWriter) Close() {
	close(w.stopCh)
	<-w.doneCh
}
