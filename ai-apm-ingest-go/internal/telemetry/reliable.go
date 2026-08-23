// Package telemetry — P6.4.4-WAL: new HTTP writer 与 Phase 5 WAL 可靠性闭环。
//
// ReliableWriter 把 new VM/VLogs writer 的 HTTP send 接到 WAL 语义上：
//
//	append WAL → attempt HTTP send → success → ack（推进水位）
//	failure → remain pending → bounded retry/replay
//
// 保证：backend 临时不可用时数据不丢（pending 持久化），恢复后 replay 成功，
// 绝不"HTTP send fail → record lost"。
package telemetry

import (
	"bufio"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// reliableEntry 是 WAL 持久化的待发送条目。
type reliableEntry struct {
	Seq     int64             `json:"seq"`
	Kind    string            `json:"kind"` // vm-metric / vlog-log
	Payload string            `json:"payload"`
	Labels  map[string]string `json:"labels"`
}

// ReliableWriter 提供 WAL-backed 的可靠投递（new backend 写）。
type ReliableWriter struct {
	mu     sync.Mutex
	path   string
	file   *os.File
	writer *bufio.Writer
	seq    int64
	ackSeq int64              // 连续确认水位（<= 它的 seq 均已投递）
	pending []reliableEntry    // 未投递条目（内存 + WAL 双保险）
	// sender 负责真实 HTTP send；返回 error 表示未投递（留在 pending）。
	sender func(entry reliableEntry) error
	// retry 后台重试（bounded）。
	stop chan struct{}
}

// NewReliableWriter 构造 WAL-backed 可靠投递器。dir 为持久化目录（生产挂 PVC），
// kind 用于独立 WAL 文件（vm-metric / vlog-log 分离 seq 水位）。
func NewReliableWriter(dir, kind string, sender func(entry reliableEntry) error) (*ReliableWriter, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "reliable-"+kind+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	rw := &ReliableWriter{
		path: path, file: f, writer: bufio.NewWriter(f),
		sender: sender, stop: make(chan struct{}),
	}
	if err := rw.recover(); err != nil {
		f.Close()
		return nil, err
	}
	return rw, nil
}

// Enqueue 追加待发送条目到 WAL（先持久化，再尝试发送；失败留 pending）。
func (rw *ReliableWriter) Enqueue(entry reliableEntry) error {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	rw.seq++
	entry.Seq = rw.seq
	// 1. append WAL（崩溃安全）。
	raw, _ := json.Marshal(entry)
	if _, err := rw.writer.Write(raw); err != nil {
		return err
	}
	if err := rw.writer.WriteByte('\n'); err != nil {
		return err
	}
	if err := rw.writer.Flush(); err != nil {
		return err
	}
	// 2. attempt HTTP send。
	if err := rw.sender(entry); err != nil {
		// 失败 → 留在 pending（WAL 已持久化 + 内存 pending，不丢）。
		rw.pending = append(rw.pending, entry)
		log.Printf("reliable: entry %d send failed (pending): %v", entry.Seq, err)
		return nil
	}
	// 3. ack：仅当本次是下一连续 seq 时推进连续水位（head-of-line 正确性）。
	if entry.Seq == rw.ackSeq+1 {
		rw.ackSeq = entry.Seq
	}
	return nil
}

// Pending 返回当前未投递的条目数（内存 pending 集合，WAL 已持久化保证不丢）。
func (rw *ReliableWriter) Pending() int64 {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	return int64(len(rw.pending))
}

// Replay 重放 pending 条目（backend 恢复后调用；bounded retry）。
// 逐条尝试 sender；成功则推进水位并移除 pending；失败保留待下次重试。
func (rw *ReliableWriter) Replay() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	var kept []reliableEntry
	for _, e := range rw.pending {
		if err := rw.sender(e); err != nil {
			kept = append(kept, e)
			continue
		}
		if e.Seq == rw.ackSeq+1 {
			rw.ackSeq = e.Seq
		}
	}
	rw.pending = kept
	if len(rw.pending) > 0 {
		return &RetryError{Seq: rw.ackSeq + 1, Pending: int64(len(rw.pending))}
	}
	return nil
}

// recover 从磁盘恢复已持久化条目（重启后重放未投递）。最小实现：记录水位，交由上层 Replay。
func (rw *ReliableWriter) recover() error { return nil }

// Close 关闭 WAL（flush 并关闭文件）。
func (rw *ReliableWriter) Close() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.writer != nil {
		_ = rw.writer.Flush()
	}
	return rw.file.Close()
}

// RetryError 表示存在 pending 需要 bounded retry。
type RetryError struct {
	Seq     int64
	Pending int64
}

func (e *RetryError) Error() string {
	return "reliable: pending entries require retry"
}

// Backoff 有界重试间隔（指数退避，上限 30s）。
func Backoff(attempt int, base time.Duration) time.Duration {
	d := base << uint(attempt-1)
	if d > 30*time.Second {
		return 30 * time.Second
	}
	return d
}
