package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// walEntry 是 WAL 最小单元。value 为序列化批次（base64）。
// At 为 append 的 unix 毫秒，用于 backlog 最旧 pending 年龄观测；旧文件缺失时为 0。
type walEntry struct {
	Seq   uint64 `json:"seq"`
	Kind  string `json:"kind"`
	Value string `json:"value"`
	At    int64  `json:"at,omitempty"`
}

// ackState 持久化 ack 水位：consecutive 是已连续 acked 的最大 seq；acked 是乱序 ack 的集合。
type ackState struct {
	Consecutive uint64   `json:"consecutive"`
	Acked       []uint64 `json:"acked"`
}

// WAL 提供崩溃安全持久化：写入 CH 前先落盘，重启后从磁盘恢复未确认批次。
// 语义：Append 返回递增 seq → 成功写 CH 后 Ack(seq) → 重启从 consecutiveAck 之后 replay。
// 仿 ai-apm-ingest-go/internal/clickhouse/wal.go 的最小实现。
type WAL struct {
	mu               sync.Mutex
	path             string
	ackPath          string
	file             *os.File
	writer           *bufio.Writer
	seq              uint64
	acked            map[uint64]struct{}
	consecutiveAckSeq uint64
}

// NewWAL 打开（必要时创建）WAL 日志并恢复 ack 水位。
func NewWAL(dir, file string) (*WAL, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, file)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	w := &WAL{
		path:    path,
		ackPath: path + ".ack",
		file:    f,
		writer:  bufio.NewWriterSize(f, 64*1024),
		acked:   make(map[uint64]struct{}),
	}
	w.recover()
	return w, nil
}

func (w *WAL) recover() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if st, acked, err := w.readAck(); err == nil {
		w.consecutiveAckSeq = st
		for _, s := range acked {
			if s > st {
				w.acked[s] = struct{}{}
			}
		}
	}
	if last, err := scanLastSeq(w.path); err == nil && last > w.seq {
		w.seq = last
	}
	// seq 只前进，不小于已 ack 水位，避免重启后产生重复 seq。
	if w.seq < w.consecutiveAckSeq {
		w.seq = w.consecutiveAckSeq
	}
}

// Append 追加一条记录并返回其单调递增 seq。
func (w *WAL) Append(kind string, v []byte) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seq++
	e := walEntry{Seq: w.seq, Kind: kind, Value: base64.StdEncoding.EncodeToString(v), At: time.Now().UnixMilli()}
	b, err := json.Marshal(e)
	if err != nil {
		return 0, err
	}
	if _, err := w.writer.Write(append(b, '\n')); err != nil {
		return 0, err
	}
	if err := w.writer.Flush(); err != nil {
		return 0, err
	}
	return w.seq, nil
}

// Ack 标记 seq 已成功写入 CH，并推进连续 ack 水位，持久化到 .ack 文件。
func (w *WAL) Ack(seq uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if seq > w.consecutiveAckSeq {
		w.acked[seq] = struct{}{}
	}
	for {
		next := w.consecutiveAckSeq + 1
		if _, ok := w.acked[next]; !ok {
			break
		}
		delete(w.acked, next)
		w.consecutiveAckSeq = next
	}
	w.persistAck()
}

// ReadAll 返回尚未 ack 的全部条目（用于启动时恢复待重试批次）。
func (w *WAL) ReadAll() ([]walEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.readRemaining(w.consecutiveAckSeq)
}

// PendingStats 暴露 WAL 待处理（未 ack）积压的可观测指标。
// 用于 Gate 5 "backlog observable"：pending records / bytes / 最旧 pending 年龄。
type PendingStats struct {
	Records int   // 未 ack 记录数
	Bytes   int   // 未 ack 记录的 value 字节总数（不含元数据）
	Oldest  int64 // 最旧未 ack 记录的 unix 毫秒；无 pending 时为 0
}

// PendingStats 扫描当前未 ack 条目并统计积压。为可观测提供数据，不修改状态。
func (w *WAL) PendingStats() PendingStats {
	w.mu.Lock()
	defer w.mu.Unlock()
	entries, err := w.readRemaining(w.consecutiveAckSeq)
	if err != nil {
		return PendingStats{}
	}
	var st PendingStats
	now := time.Now().UnixMilli()
	for _, e := range entries {
		if e.Seq <= w.consecutiveAckSeq {
			continue
		}
		st.Records++
		if v, err := base64.StdEncoding.DecodeString(e.Value); err == nil {
			st.Bytes += len(v)
		}
		if e.At > 0 && (st.Oldest == 0 || e.At < st.Oldest) {
			st.Oldest = e.At
		}
	}
	if st.Oldest == 0 {
		st.Oldest = now
	}
	return st
}

// OldestAgeSeconds 返回最旧 pending 记录的滞留秒数（backlog 年龄；无 pending 返回 0）。
func (w *WAL) OldestAgeSeconds() int {
	st := w.PendingStats()
	if st.Records == 0 {
		return 0
	}
	ageMs := time.Now().UnixMilli() - st.Oldest
	if ageMs < 0 {
		ageMs = 0
	}
	return int(ageMs / 1000)
}

// Compact 截断已 ack 的日志前缀，仅保留未 ack 条目。
func (w *WAL) Compact() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.seq <= w.consecutiveAckSeq {
		_ = w.file.Truncate(0)
		_, _ = w.file.Seek(0, 0)
		_ = w.file.Sync()
		w.acked = make(map[uint64]struct{})
		return
	}
	remaining, err := w.readRemaining(w.consecutiveAckSeq)
	if err != nil {
		return
	}
	tmp := w.path + ".tmp"
	tf, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	bw := bufio.NewWriterSize(tf, 64*1024)
	for _, e := range remaining {
		if _, acked := w.acked[e.Seq]; acked {
			continue
		}
		b, _ := json.Marshal(e)
		_, _ = bw.Write(append(b, '\n'))
	}
	_ = bw.Flush()
	_ = tf.Sync()
	_ = tf.Close()
	_ = os.Rename(tmp, w.path)
	nf, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	w.file = nf
	w.writer = bufio.NewWriterSize(nf, 64*1024)
	w.acked = make(map[uint64]struct{})
	w.persistAck()
}

// Close 冲刷并关闭 WAL 底层文件。
func (w *WAL) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.writer.Flush()
	_ = w.file.Sync()
	_ = w.file.Close()
}

func (w *WAL) persistAck() {
	st := ackState{Consecutive: w.consecutiveAckSeq}
	for s := range w.acked {
		st.Acked = append(st.Acked, s)
	}
	sort.Slice(st.Acked, func(i, j int) bool { return st.Acked[i] < st.Acked[j] })
	b, _ := json.Marshal(st)
	_ = os.WriteFile(w.ackPath+".tmp", b, 0o644)
	_ = os.Rename(w.ackPath+".tmp", w.ackPath)
}

// readAck 读取 ack 状态；兼容纯数字旧格式（consecutive seq）。
func (w *WAL) readAck() (uint64, []uint64, error) {
	b, err := os.ReadFile(w.ackPath)
	if err != nil {
		return 0, nil, err
	}
	if n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64); err == nil {
		return n, nil, nil
	}
	var st ackState
	if err := json.Unmarshal(b, &st); err != nil {
		return 0, nil, err
	}
	return st.Consecutive, st.Acked, nil
}

func (w *WAL) readRemaining(after uint64) ([]walEntry, error) {
	f, err := os.Open(w.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var out []walEntry
	for sc.Scan() {
		var e walEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if _, acked := w.acked[e.Seq]; acked {
			continue
		}
		if e.Seq > after {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

func scanLastSeq(path string) (uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var last uint64
	for sc.Scan() {
		var e walEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if e.Seq > last {
			last = e.Seq
		}
	}
	return last, sc.Err()
}
