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

// LogWriter batches log records and writes to ClickHouse via HTTP.
// 生产约束：写入失败不静默丢弃，先落 WAL 再入重试队列周期性重试（无 WAL 时退化为内存重试）；
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
	// WAL 持久化（独立 ingest-wal-log.log，不与 span/edge 共享）
	wal *WAL
	// 待重试的序列化批次（seq -> 序列化后的 TabSeparated bytes）
	retryPending  map[uint64][]byte
	memSeq        uint64 // 无 WAL 时用内存递增序号作为 key，避免互相覆盖丢数据
	maxRetryBatches int
	retryBytes    int64
}

// NewLogWriter creates a new LogWriter. walDir 为 WAL 持久化目录（生产挂载 PVC；空则退化为内存重试）。
func NewLogWriter(host string, port int, walDir string) *LogWriter {
	w := &LogWriter{
		endpoint:         chEndpoint(host, port),
		batchSize:        1024,
		flushEvery:       5 * time.Second,
		httpClient:       newCHHTTPClient(),
		stopCh:           make(chan struct{}),
		doneCh:           make(chan struct{}),
		maxBufferRecords: 10240,
		retryPending:     make(map[uint64][]byte),
		maxRetryBatches:  500,
	}
	if walDir != "" {
		wal, err := NewWALFile(walDir, walLogFile)
		if err != nil {
			log.Printf("LogWriter WAL init failed (falling back to memory retry): %v", err)
		} else {
			w.wal = wal
			// 启动恢复：重放 WAL 中未确认的记录
			if entries, err := wal.ReadAll(); err == nil && len(entries) > 0 {
				log.Printf("LogWriter WAL: recovering %d pending records", len(entries))
				for _, e := range entries {
					if rows, derr := decodeRows(e.Value); derr == nil {
						w.addRetry(e.Seq, rows)
					}
				}
			}
		}
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
	w.writeWithRetry(rows)
}

// writeWithRetry 写入数据，失败时保留到 retryPending 并周期性重试。
// 与 span/edge 一致：先写 WAL 再写 ClickHouse，崩溃不丢日志。
func (w *LogWriter) writeWithRetry(rows []byte) {
	if w.wal != nil {
		if seq, err := w.wal.Append("log", rows); err == nil {
			w.addRetry(seq, rows)
		} else {
			log.Printf("WAL append error (data kept in memory): %v", err)
			w.addRetryMem(rows)
		}
	} else {
		w.addRetryMem(rows)
	}

	if err := w.insertBatchBytes(rows); err != nil {
		log.Printf("ClickHouse log write failed (will retry): %v", err)
	} else {
		w.confirm(rows)
		log.Printf("ClickHouse: wrote %d log records", bytes.Count(rows, []byte{'\n'}))
	}
}

// addRetry 将一批加入重试队列，并做容量保护：超过 maxRetryBatches 时丢弃最旧批次
// （并 Ack 对应 WAL seq，释放内存与磁盘），避免 ClickHouse 长时间不可用时 OOM / 磁盘打满。
func (w *LogWriter) addRetry(seq uint64, rows []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.retryPending[seq] = rows
	w.retryBytes += int64(len(rows))
	for len(w.retryPending) > w.maxRetryBatches {
		if !w.dropOldestRetryLocked() {
			break
		}
	}
}

// addRetryMem 无 WAL 时使用内存递增序号作为重试队列 key，避免所有批次用 key=0 互相覆盖丢数据。
func (w *LogWriter) addRetryMem(rows []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.memSeq++
	w.retryPending[w.memSeq] = rows
	w.retryBytes += int64(len(rows))
	for len(w.retryPending) > w.maxRetryBatches {
		if !w.dropOldestRetryLocked() {
			break
		}
	}
}

// dropOldestRetryLocked 丢弃重试队列中最旧的一批（最小 seq），并 Ack 对应 WAL seq。
// 返回是否丢弃成功。调用方需持有锁。
func (w *LogWriter) dropOldestRetryLocked() bool {
	if len(w.retryPending) == 0 {
		return false
	}
	var oldestSeq uint64
	first := true
	for seq := range w.retryPending {
		if first || seq < oldestSeq {
			oldestSeq = seq
		}
		first = false
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
	log.Printf("LOGWRITER: dropping oldest retry batch seq=%d (%d bytes) to bound memory/disk", oldestSeq, len(rows))
	return true
}

// flushRetry 重试所有未成功的日志批次，按 seq 升序处理。
// 升序保证低 seq 先确认，配合 WAL 连续确认水位，避免高 seq 先确认导致 compact 误删低 seq 未确认条目。
func (w *LogWriter) flushRetry() {
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
		if err := w.insertBatchBytes(rows); err != nil {
			log.Printf("ClickHouse log retry failed (kept for next retry): %v", err)
			continue
		}
		w.confirmSeq(seq, rows)
	}
}

func (w *LogWriter) confirmSeq(seq uint64, rows []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if v, ok := w.retryPending[seq]; ok && string(v) == string(rows) {
		delete(w.retryPending, seq)
		w.retryBytes -= int64(len(v))
		if w.wal != nil && seq > 0 {
			w.wal.Ack(seq)
		}
	}
}

// confirm 从 retryPending 中移除已成功写入的数据（按内容精确匹配）。
// writeWithRetry 同步写入成功时数据刚通过 wal.Append 拿到 seq，按内容确认即可，
// 避免"内容匹配"在相同内容被以不同 seq 追加时误删/残留。
func (w *LogWriter) confirm(rows []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	key := string(rows)
	for seq, v := range w.retryPending {
		if string(v) == key {
			delete(w.retryPending, seq)
			w.retryBytes -= int64(len(v))
			if w.wal != nil && seq > 0 {
				w.wal.Ack(seq)
			}
			return
		}
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
		clusterID := r.ClusterID
		if clusterID == "" {
			clusterID = "default"
		}

		fmt.Fprintf(&buf, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			escapeTSV(r.TenantID),
			escapeTSV(clusterID),
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
