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

// MetricsWriter batches service metrics and topology edges, writes to ClickHouse
// via HTTP with WAL-backed durability and retry.
type MetricsWriter struct {
	endpoint   string
	mu         sync.Mutex
	metricsBuf []*model.ServiceMetric
	edgesBuf   []*model.TopologyEdge
	batchSize  int
	flushEvery time.Duration
	httpClient *http.Client
	stopCh     chan struct{}
	doneCh     chan struct{}

	wal *WAL
	// 待重试：kind+seq -> rows
	retryPending map[string][]byte

	// OnMetricsWritten / OnEdgesWritten 写入成功回调
	OnMetricsWritten func(n int)
	OnEdgesWritten   func(n int)
}

// NewMetricsWriter creates a new MetricsWriter.
func NewMetricsWriter(host string, port int, walDir string) *MetricsWriter {
	w := &MetricsWriter{
		endpoint:     fmt.Sprintf("http://%s:%d", host, port),
		batchSize:    1024,
		flushEvery:   10 * time.Second,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
		retryPending: make(map[string][]byte),
	}
	if walDir != "" {
		wal, err := NewWAL(walDir)
		if err != nil {
			log.Printf("MetricsWriter WAL init failed (memory retry): %v", err)
		} else {
			w.wal = wal
			if entries, err := wal.ReadAll(); err == nil && len(entries) > 0 {
				log.Printf("MetricsWriter WAL: recovering %d pending records", len(entries))
				for _, e := range entries {
					if rows, derr := decodeRows(e.Value); derr == nil {
						w.retryPending[fmt.Sprintf("%s-%d", e.Kind, e.Seq)] = rows
					}
				}
			}
		}
	}
	go w.flushLoop()
	return w
}

// AddMetric adds a ServiceMetric to the batch buffer.
func (w *MetricsWriter) AddMetric(m *model.ServiceMetric) {
	w.mu.Lock()
	w.metricsBuf = append(w.metricsBuf, m)
	shouldFlush := len(w.metricsBuf) >= w.batchSize
	w.mu.Unlock()
	if shouldFlush {
		w.flush()
	}
}

// AddEdge adds a TopologyEdge to the batch buffer.
func (w *MetricsWriter) AddEdge(e *model.TopologyEdge) {
	w.mu.Lock()
	w.edgesBuf = append(w.edgesBuf, e)
	shouldFlush := len(w.edgesBuf) >= w.batchSize
	w.mu.Unlock()
	if shouldFlush {
		w.flush()
	}
}

func (w *MetricsWriter) flushLoop() {
	ticker := time.NewTicker(w.flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-w.stopCh:
			w.flush()
			w.flushRetry()
			if w.wal != nil {
				w.wal.Close()
			}
			close(w.doneCh)
			return
		case <-ticker.C:
			w.flush()
			w.flushRetry()
		}
	}
}

func (w *MetricsWriter) flush() {
	w.mu.Lock()
	if len(w.metricsBuf) == 0 && len(w.edgesBuf) == 0 {
		w.mu.Unlock()
		return
	}
	metrics := w.metricsBuf
	edges := w.edgesBuf
	w.metricsBuf = make([]*model.ServiceMetric, 0, w.batchSize)
	w.edgesBuf = make([]*model.TopologyEdge, 0, w.batchSize)
	w.mu.Unlock()

	if len(metrics) > 0 {
		rows := w.serializeMetrics(metrics)
		w.writeRetry("metric", rows, w.insertMetrics)
	}
	if len(edges) > 0 {
		rows := w.serializeEdges(edges)
		w.writeRetry("edge", rows, w.insertEdges)
	}
}

func (w *MetricsWriter) serializeMetrics(metrics []*model.ServiceMetric) []byte {
	var buf bytes.Buffer
	for _, m := range metrics {
		fmt.Fprintf(&buf, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%s\n",
			m.TenantID, m.ServiceName, m.CallerService,
			m.TimeBucket.Format("2006-01-02 15:04:05"),
			m.CallCount, m.ErrorCount, m.DurationSumNs, m.DurationCount, m.Date,
		)
	}
	return buf.Bytes()
}

func (w *MetricsWriter) serializeEdges(edges []*model.TopologyEdge) []byte {
	var buf bytes.Buffer
	for _, e := range edges {
		fmt.Fprintf(&buf, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%s\n",
			e.TenantID, e.SourceService, e.TargetService,
			e.TimeBucket.Format("2006-01-02 15:04:05"),
			e.CallCount, e.ErrorCount, e.AvgDurationNs, e.Date,
		)
	}
	return buf.Bytes()
}

// writeRetry 写入数据，失败时进入重试队列并落 WAL。
func (w *MetricsWriter) writeRetry(kind string, rows []byte, insert func([]byte) error) {
	key := ""
	if w.wal != nil {
		if seq, err := w.wal.Append(kind, rows); err == nil {
			key = fmt.Sprintf("%s-%d", kind, seq)
		}
	}
	if key == "" {
		key = fmt.Sprintf("%s-mem", kind)
	}
	w.mu.Lock()
	w.retryPending[key] = rows
	w.mu.Unlock()

	if err := insert(rows); err != nil {
		log.Printf("ClickHouse %s write failed (will retry): %v", kind, err)
	} else {
		w.confirm(kind, key, rows)
		w.countWritten(kind, rows)
	}
}

func (w *MetricsWriter) countWritten(kind string, rows []byte) {
	n := 0
	for _, c := range rows {
		if c == '\n' {
			n++
		}
	}
	if kind == "metric" && w.OnMetricsWritten != nil {
		w.OnMetricsWritten(n)
	} else if kind == "edge" && w.OnEdgesWritten != nil {
		w.OnEdgesWritten(n)
	}
}

func (w *MetricsWriter) flushRetry() {
	w.mu.Lock()
	pending := make(map[string][]byte, len(w.retryPending))
	for k, v := range w.retryPending {
		pending[k] = v
	}
	w.mu.Unlock()
	for key, rows := range pending {
		kind := key[:strings.Index(key, "-")]
		var insert func([]byte) error
		if kind == "metric" {
			insert = w.insertMetrics
		} else {
			insert = w.insertEdges
		}
		if err := insert(rows); err != nil {
			continue
		}
		w.confirm(kind, key, rows)
		w.countWritten(kind, rows)
	}
}

func (w *MetricsWriter) confirm(kind, key string, rows []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if v, ok := w.retryPending[key]; ok && string(v) == string(rows) {
		delete(w.retryPending, key)
		if w.wal != nil {
			// 从 key 解析 seq 推进 ack
			var seq uint64
			if _, err := fmt.Sscanf(key[len(kind)+1:], "%d", &seq); err == nil && seq > 0 {
				w.wal.Ack(seq)
			}
		}
	}
}

// insertMetrics 已停用：metric_service_red 是只写不读的死表。
// 服务 RED 指标改由 ingest /metrics 暴露，经 VM 采集（VictoriaMetrics 为唯一指标库）。
// 保留空实现以兼容调用链，但不再写 ClickHouse。
func (w *MetricsWriter) insertMetrics(metrics []byte) error {
	return nil
}

func (w *MetricsWriter) insertEdges(edges []byte) error {
	query := "INSERT INTO observability.service_topology FORMAT TabSeparated"
	u := w.endpoint + "/?" + "query=" + url.QueryEscape(query)
	resp, err := w.httpClient.Post(u, "text/plain", bytes.NewReader(edges))
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

// Close stops the flush loop and performs a final flush.
func (w *MetricsWriter) Close() {
	close(w.stopCh)
	<-w.doneCh
}
