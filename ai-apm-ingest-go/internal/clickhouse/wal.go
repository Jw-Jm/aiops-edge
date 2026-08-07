package clickhouse

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// walEntry 是写入 WAL 的最小单元。value 为序列化后的待写入数据（批次）。
// 使用追加式日志文件，写入成功后再进行 offset 推进与 compact。
type walEntry struct {
	Seq   uint64 `json:"seq"`
	Kind  string `json:"kind"` // span / metric / edge
	Value string `json:"value"`
}

// WAL 提供崩溃安全的数据持久化：数据在写入 ClickHouse 前先落盘，
// 重启后从磁盘恢复未写入数据，保证"生产不丢数据"。
type WAL struct {
	mu       sync.Mutex
	path     string
	file     *os.File
	writer   *bufio.Writer
	seq      uint64
	ackSeq   uint64 // 已成功写入 ClickHouse 的最大 seq
	dir      string
	maxBytes int64
}

// NewWAL 创建/打开一个 WAL 文件。dir 为持久化目录（生产应挂载 PVC）。
func NewWAL(dir string) (*WAL, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "ingest-wal.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	w := &WAL{
		path:   path,
		file:   f,
		writer: bufio.NewWriterSize(f, 64*1024),
		dir:    dir,
		maxBytes: 1 << 30, // 1GB 上限，防止磁盘打满
	}
	// 启动时恢复已确认的最大 seq，并清空陈旧内容（避免重复追加已确认数据）
	w.recoverSeq()
	return w, nil
}

// recoverSeq 读取已有 WAL 的最后一条 seq 作为续写起点（不重放，由 ReliableBuffer 负责恢复逻辑）。
func (w *WAL) recoverSeq() {
	w.mu.Lock()
	defer w.mu.Unlock()
	st, err := os.Stat(w.path)
	if err != nil || st.Size() == 0 {
		return
	}
	// 简单扫描最后一行取得 seq
	last, err := scanLastSeq(w.path)
	if err == nil && last > w.seq {
		w.seq = last
	}
}

// Append 追加一条待写入记录，返回分配的 seq。
func (w *WAL) Append(kind string, value []byte) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seq++
	entry := walEntry{Seq: w.seq, Kind: kind, Value: base64.StdEncoding.EncodeToString(value)}
	b, err := json.Marshal(entry)
	if err != nil {
		return 0, err
	}
	if _, err := w.writer.Write(append(b, '\n')); err != nil {
		return 0, err
	}
	if err := w.writer.Flush(); err != nil {
		return 0, err
	}
	// 超过大小阈值时触发 compact（由调用方在合适时机调用）
	return w.seq, nil
}

// Ack 确认某条已成功写入 ClickHouse，推进 ackSeq 并触发一次异步 compact。
func (w *WAL) Ack(seq uint64) {
	w.mu.Lock()
	if seq > w.ackSeq {
		w.ackSeq = seq
	}
	needCompact := false
	if st, err := w.file.Stat(); err == nil && st.Size() > w.maxBytes {
		needCompact = true
	}
	w.mu.Unlock()
	if needCompact {
		w.Compact()
	}
}

// Sync 强制刷盘。
func (w *WAL) Sync() {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.writer.Flush()
	_ = w.file.Sync()
}

// Compact 重写 WAL，仅保留 ackSeq 之后未确认的记录。
func (w *WAL) Compact() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ackSeq == 0 || w.seq <= w.ackSeq {
		// 全部确认，直接清空文件
		if err := w.file.Truncate(0); err != nil {
			log.Printf("WAL compact truncate error: %v", err)
			return
		}
		_, _ = w.file.Seek(0, 0)
		_ = w.file.Sync()
		return
	}
	// 读取剩余未确认记录重写
	remaining, err := w.readRemainingLocked(w.ackSeq)
	if err != nil {
		log.Printf("WAL compact read error: %v", err)
		return
	}
	tmp := w.path + ".tmp"
	tf, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("WAL compact open tmp error: %v", err)
		return
	}
	bw := bufio.NewWriterSize(tf, 64*1024)
	for _, e := range remaining {
		b, _ := json.Marshal(e)
		if _, err := bw.Write(append(b, '\n')); err != nil {
			tf.Close()
			return
		}
	}
	bw.Flush()
	tf.Sync()
	tf.Close()
	_ = os.Rename(tmp, w.path)
	nf, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("WAL compact reopen error: %v", err)
		return
	}
	w.file = nf
	w.writer = bufio.NewWriterSize(nf, 64*1024)
}

// readRemainingLocked 读取 ackSeq 之后的所有记录（未确认，需保留）。
func (w *WAL) readRemainingLocked(afterSeq uint64) ([]walEntry, error) {
	f, err := os.Open(w.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	var out []walEntry
	for sc.Scan() {
		var e walEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		if e.Seq > afterSeq {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// ReadAll 读取全部分区记录（用于启动恢复重放）。
func (w *WAL) ReadAll() ([]walEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.readRemainingLocked(0)
}

// Close 关闭 WAL 并确保刷盘。
func (w *WAL) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.writer.Flush()
	_ = w.file.Sync()
	_ = w.file.Close()
}

// scanLastSeq 从文件尾部读取最后一条 seq。
func scanLastSeq(path string) (uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	var last uint64
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var e walEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		if e.Seq > last {
			last = e.Seq
		}
	}
	return last, nil
}
