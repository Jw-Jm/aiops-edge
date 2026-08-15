package clickhouse

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
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
	edgesBuf   []*model.TopologyEdge
	batchSize  int
	flushEvery time.Duration
	httpClient *http.Client
	stopCh     chan struct{}
	doneCh     chan struct{}

	wal *WAL
	// 待重试：kind+seq -> rows
	retryPending map[string][]byte
	// 无 WAL 时用内存递增序号生成唯一 key，避免所有批次用固定 key 互相覆盖丢数据
	memSeq uint64

	// 内存/重试上限（防止 ClickHouse 长时间不可用时 OOM）
	maxBufferEdges  int   // 边缓冲最大条数，超限丢弃最旧
	maxRetryBatches int   // 重试队列最大批次条数，超限丢弃最旧

	// OnEdgesWritten 写入成功回调
	OnEdgesWritten func(n int)
}

// NewMetricsWriter creates a new MetricsWriter.
func NewMetricsWriter(host string, port int, walDir string) *MetricsWriter {
	w := &MetricsWriter{
		endpoint:        chEndpoint(host, port),
		batchSize:       1024,
		flushEvery:      10 * time.Second,
		httpClient:      newCHHTTPClient(),
		stopCh:          make(chan struct{}),
		doneCh:          make(chan struct{}),
		retryPending:    make(map[string][]byte),
		maxBufferEdges:  10240,
		maxRetryBatches: 500,
	}
	if walDir != "" {
		wal, err := NewWALFile(walDir, walEdgeFile)
		if err != nil {
			log.Printf("MetricsWriter WAL init failed (memory retry): %v", err)
		} else {
			w.wal = wal
			if entries, err := wal.ReadAll(); err == nil && len(entries) > 0 {
				log.Printf("MetricsWriter WAL: recovering %d pending records", len(entries))
				for _, e := range entries {
					// 分文件后仅含 edge 条目，防御性跳过其他 kind
					if e.Kind != "edge" {
						continue
					}
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

// AddEdge adds a TopologyEdge to the batch buffer.
// 内存保护：缓冲超过 maxBufferEdges 时丢弃最旧边并记日志，防 OOM。
func (w *MetricsWriter) AddEdge(e *model.TopologyEdge) {
	w.mu.Lock()
	if w.maxBufferEdges > 0 && len(w.edgesBuf) >= w.maxBufferEdges {
		copy(w.edgesBuf, w.edgesBuf[1:])
		w.edgesBuf[len(w.edgesBuf)-1] = nil
		w.edgesBuf = w.edgesBuf[:len(w.edgesBuf)-1]
		w.mu.Unlock()
		log.Printf("METRICWRITER: buffer full (%d), dropping oldest edge (backpressure)", w.maxBufferEdges)
		return
	}
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
	if len(w.edgesBuf) == 0 {
		w.mu.Unlock()
		return
	}
	edges := w.edgesBuf
	w.edgesBuf = make([]*model.TopologyEdge, 0, w.batchSize)
	w.mu.Unlock()

	// metric_service_red 死表已停写（服务 RED 改由 ingest /metrics 暴露进 VM），仅保留 edge 写入。
	if len(edges) > 0 {
		rows := w.serializeEdges(edges)
		w.writeRetry("edge", rows, w.insertEdges)
	}
}

func (w *MetricsWriter) serializeEdges(edges []*model.TopologyEdge) []byte {
	var buf bytes.Buffer
	for _, e := range edges {
		clusterID := e.ClusterID
		if clusterID == "" {
			clusterID = "default"
		}
		fmt.Fprintf(&buf, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%d\t%s\n",
			e.TenantID, clusterID, e.SourceService, e.TargetService,
			e.TimeBucket.Format("2006-01-02 15:04:05"),
			e.CallCount, e.ErrorCount, e.AvgDurationNs, e.Date,
		)
	}
	return buf.Bytes()
}

// writeRetry 写入数据，失败时进入重试队列并落 WAL。
// 内存保护：重试队列超过 maxRetryBatches 时丢弃最旧（并 Ack 对应 WAL seq），防 OOM/磁盘打满。
func (w *MetricsWriter) writeRetry(kind string, rows []byte, insert func([]byte) error) {
	key := ""
	if w.wal != nil {
		if seq, err := w.wal.Append(kind, rows); err == nil {
			key = fmt.Sprintf("%s-%d", kind, seq)
		}
	}
	if key == "" {
		// 无 WAL：用递增序号保证 key 唯一，避免固定 key 覆盖导致只重试最后一批
		w.mu.Lock()
		w.memSeq++
		w.mu.Unlock()
		key = fmt.Sprintf("%s-mem-%d", kind, w.memSeq)
	}
	w.addRetryLocked(key, rows)

	if err := insert(rows); err != nil {
		log.Printf("ClickHouse %s write failed (will retry): %v", kind, err)
	} else {
		w.confirm(kind, key, rows)
		w.countWritten(kind, rows)
	}
}

// addRetryLocked 将一批加入重试队列，超上限时丢弃最旧批次并 Ack WAL seq。
func (w *MetricsWriter) addRetryLocked(key string, rows []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.retryPending[key] = rows
	for len(w.retryPending) > w.maxRetryBatches {
		var oldest string
		first := true
		for k := range w.retryPending {
			if first || k < oldest {
				oldest = k
			}
			first = false
		}
		if v, ok := w.retryPending[oldest]; ok {
			delete(w.retryPending, oldest)
			// 尝试解析 seq 推进 WAL ack（丢弃即视为确认，释放磁盘）
			if w.wal != nil {
				idx := strings.Index(oldest, "-")
				if idx >= 0 {
					var seq uint64
					if _, err := fmt.Sscanf(oldest[idx+1:], "%d", &seq); err == nil && seq > 0 {
						w.wal.Ack(seq)
					}
				}
			}
			log.Printf("METRICWRITER: retry queue full (%d), dropping oldest batch %s (%d bytes)", w.maxRetryBatches, oldest, len(v))
		}
	}
}

func (w *MetricsWriter) countWritten(kind string, rows []byte) {
	n := 0
	for _, c := range rows {
		if c == '\n' {
			n++
		}
	}
	if kind == "edge" && w.OnEdgesWritten != nil {
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
	// 按 seq 升序重试，配合 WAL 连续确认水位，避免高 seq 先确认导致 compact 误删低 seq 未确认条目
	keys := make([]string, 0, len(pending))
	for k := range pending {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return retryKeySeq(keys[i]) < retryKeySeq(keys[j]) })
	for _, key := range keys {
		kind := key[:strings.Index(key, "-")]
		if kind != "edge" {
			// metric 维度已停写（metric_service_red 死表），忽略旧 WAL/内存 metric 项
			continue
		}
		rows := pending[key]
		if err := w.insertEdges(rows); err != nil {
			continue
		}
		w.confirm(kind, key, rows)
		w.countWritten(kind, rows)
	}
}

// retryKeySeq 从 retryPending 的 key（edge-123 / edge-mem-123）中解析数值序号，用于升序重试排序。
func retryKeySeq(key string) uint64 {
	idx := strings.LastIndex(key, "-")
	if idx < 0 {
		return 0
	}
	var seq uint64
	if _, err := fmt.Sscanf(key[idx+1:], "%d", &seq); err == nil {
		return seq
	}
	return 0
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
