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

// LogWriter batches log records and writes to ClickHouse via HTTP
type LogWriter struct {
	endpoint   string
	mu         sync.Mutex
	buffer     []*model.LogRecord
	batchSize  int
	flushEvery time.Duration
	httpClient *http.Client
	stopCh     chan struct{}
	doneCh     chan struct{}
}

// NewLogWriter creates a new LogWriter
func NewLogWriter(host string, port int) *LogWriter {
	w := &LogWriter{
		endpoint:   fmt.Sprintf("http://%s:%d", host, port),
		batchSize:  1024,
		flushEvery: 5 * time.Second,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
	go w.flushLoop()
	return w
}

// Add adds a LogRecord to the batch buffer
func (w *LogWriter) Add(lr *model.LogRecord) {
	w.mu.Lock()
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
			close(w.doneCh)
			return
		case <-ticker.C:
			w.flush()
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

	if err := w.insertBatch(batch); err != nil {
		log.Printf("ClickHouse log write failed: %v (dropping %d log records)", err, len(batch))
	} else {
		log.Printf("ClickHouse: wrote %d log records", len(batch))
	}
}

func (w *LogWriter) insertBatch(records []*model.LogRecord) error {
	var buf bytes.Buffer
	for _, r := range records {
		// Attributes as ClickHouse Map
		attrParts := make([]string, 0, len(r.Attributes))
		for k, v := range r.Attributes {
			attrParts = append(attrParts, fmt.Sprintf("'%s':'%s'", escapeCH(k), escapeCH(v)))
		}
		attrStr := "{" + strings.Join(attrParts, ",") + "}"

		fmt.Fprintf(&buf, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.TenantID,
			r.Timestamp.Format("2006-01-02 15:04:05.000000000"),
			r.ServiceName,
			r.Severity,
			r.Body,
			attrStr,
			r.TraceID,
			r.SpanID,
			r.TimeBucket,
			r.Date,
		)
	}

	query := "INSERT INTO observability.log_records FORMAT TabSeparated"
	u := w.endpoint + "/?" + "query=" + url.QueryEscape(query)

	resp, err := w.httpClient.Post(u, "text/plain", &buf)
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
