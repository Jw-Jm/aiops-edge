package clickhouse

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// walEntry 是写入 WAL 的最小单元。value 为序列化后的待写入数据（批次）。
// 使用追加式日志文件，写入成功后再进行 offset 推进与 compact。
type walEntry struct {
	Seq   uint64 `json:"seq"`
	Kind  string `json:"kind"` // span / edge / log
	Value string `json:"value"`
}

// WAL 提供崩溃安全的数据持久化：数据在写入 ClickHouse 前先落盘，
// 重启后从磁盘恢复未写入数据，保证"生产不丢数据"。
type WAL struct {
	mu      sync.Mutex
	path    string
	ackPath string // 连续确认水位侧车文件（H2：重启恢复 consecutiveAckSeq）
	file    *os.File
	writer  *bufio.Writer
	seq     uint64
	// 已确认但高于连续水位、仍保留在文件中的 seq（乱序确认：seq=7 已确认而 5/6 未确认）
	acked             map[uint64]struct{}
	consecutiveAckSeq uint64 // 已连续确认的高水位：<= 它的 seq 均已确认，可安全删除
	dir               string
	maxBytes          int64
}

// 各 kind 使用独立 WAL 文件，避免 span/edge/log 共享 seq 与确认水位，崩溃恢复时互相污染。
const (
	walSpanFile = "ingest-wal-span.log"
	walEdgeFile = "ingest-wal-edge.log"
	walLogFile  = "ingest-wal-log.log"
)

// NewWAL 创建/打开默认 WAL 文件（旧 ingest-wal.log，仅向后兼容；新代码请使用 NewWALFile）。
func NewWAL(dir string) (*WAL, error) {
	return NewWALFile(dir, "ingest-wal.log")
}

// NewWALFile 创建/打开指定文件名的 WAL 文件。dir 为持久化目录（生产应挂载 PVC）。
// 不同 kind（span/edge/log）必须使用不同文件，避免各自独立的 seq/ack 水位互相污染。
func NewWALFile(dir, fileName string) (*WAL, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, fileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	w := &WAL{
		path:     path,
		ackPath:  path + ".ack",
		file:     f,
		writer:   bufio.NewWriterSize(f, 64*1024),
		dir:      dir,
		acked:    make(map[uint64]struct{}),
		maxBytes: 1 << 30, // 1GB 上限，防止磁盘打满
	}
	// 启动时恢复已写入的最大 seq 与连续确认水位（H2：consecutiveAckSeq 必须持久化，
	// 否则重启后为 0，Compact 会 Truncate(0) 清空含未 ack 的全部数据）。
	w.recoverSeq()
	return w, nil
}

// ackState 持久化到侧车文件的确认状态（H2）：
// 连续确认水位 + 乱序确认集合（重启后两者都需恢复，否则 Compact 会误删/重放）。
type ackState struct {
	Consecutive uint64   `json:"consecutive"`
	Acked       []uint64 `json:"acked"`
}

// recoverSeq 恢复续写起点：从侧车文件恢复确认状态（水位 + 乱序 ack 集合），
// 从 WAL 尾部恢复最大 seq。不重放（由 Writer 负责恢复逻辑）。
func (w *WAL) recoverSeq() {
	w.mu.Lock()
	defer w.mu.Unlock()
	// H2：先恢复确认状态（侧车文件），保证重启后 Compact 只截断已 ack 前缀
	if ack, acked, err := w.readAckLocked(); err == nil {
		if ack > w.consecutiveAckSeq {
			w.consecutiveAckSeq = ack
		}
		for _, s := range acked {
			if s > w.consecutiveAckSeq {
				w.acked[s] = struct{}{}
			}
		}
	}
	st, err := os.Stat(w.path)
	if err != nil || st.Size() == 0 {
		// 文件为空（可能已被全量 compact）但侧车记录了水位：续写 seq 从水位继续，
		// 避免 seq 回绕导致新条目的 Ack 无法推进水位。
		if w.consecutiveAckSeq > w.seq {
			w.seq = w.consecutiveAckSeq
		}
		return
	}
	// 简单扫描最后一行取得 seq
	last, err := scanLastSeq(w.path)
	if err == nil && last > w.seq {
		w.seq = last
	}
	// 防御：seq 永不低于已确认水位
	if w.seq < w.consecutiveAckSeq {
		w.seq = w.consecutiveAckSeq
	}
}

// readAckLocked 读取侧车文件中的确认状态。调用方需持有锁。
func (w *WAL) readAckLocked() (uint64, []uint64, error) {
	b, err := os.ReadFile(w.ackPath)
	if err != nil {
		return 0, nil, err
	}
	// 兼容旧格式：纯数字（仅水位）
	if n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64); err == nil {
		return n, nil, nil
	}
	var st ackState
	if err := json.Unmarshal(b, &st); err != nil {
		return 0, nil, err
	}
	return st.Consecutive, st.Acked, nil
}

// persistAckLocked 将确认状态（水位 + 乱序 ack 集合）原子写入侧车文件（tmp + rename），
// 保证崩溃/重启后状态不丢失。调用方需持有锁。
func (w *WAL) persistAckLocked() {
	st := ackState{Consecutive: w.consecutiveAckSeq}
	for seq := range w.acked {
		st.Acked = append(st.Acked, seq)
	}
	sort.Slice(st.Acked, func(i, j int) bool { return st.Acked[i] < st.Acked[j] })
	b, err := json.Marshal(st)
	if err != nil {
		log.Printf("WAL persist ack marshal error: %v", err)
		return
	}
	tmp := w.ackPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		log.Printf("WAL persist ack error: %v", err)
		return
	}
	if err := os.Rename(tmp, w.ackPath); err != nil {
		log.Printf("WAL persist ack rename error: %v", err)
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

// Ack 确认某条已成功写入 ClickHouse。ack 允许乱序：只推进"连续确认高水位"，
// Compact 仅删除 <= 连续水位的条目，未确认的低 seq 不会因高 seq 先确认而被误删。
// H2：水位推进后持久化到侧车文件，重启后恢复，避免 Compact 误删未 ack 数据。
func (w *WAL) Ack(seq uint64) {
	w.mu.Lock()
	changed := false
	if seq > w.consecutiveAckSeq {
		w.acked[seq] = struct{}{}
		changed = true
	}
	// 推进连续确认高水位：seq=5、7 已确认而 6 未确认时，水位停在 4。
	advanced := false
	for {
		next := w.consecutiveAckSeq + 1
		if _, ok := w.acked[next]; !ok {
			break
		}
		delete(w.acked, next)
		w.consecutiveAckSeq = next
		advanced = true
	}
	// 水位推进或乱序 ack 集合变化都需持久化（否则重启后丢失乱序 ack 状态）
	if advanced || changed {
		w.persistAckLocked()
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

// Compact 重写 WAL，仅保留"连续确认高水位"之后的记录。
// 乱序确认（如 seq=7 已确认、5/6 未确认）时保留 5/6，避免丢未确认数据。
// H2 修复：仅当"全部条目均已确认"（seq <= consecutiveAckSeq）时才 Truncate(0)；
// 存在未确认条目（含重启后 consecutiveAckSeq 恢复为 0 但文件仍有数据的情况）一律走
// 重写保留分支，绝不 Truncate(0) 清空未 ack 数据。
func (w *WAL) Compact() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.seq <= w.consecutiveAckSeq {
		// 全部确认（无未确认条目），直接清空文件
		if err := w.file.Truncate(0); err != nil {
			log.Printf("WAL compact truncate error: %v", err)
			return
		}
		_, _ = w.file.Seek(0, 0)
		_ = w.file.Sync()
		return
	}
	// 读取剩余未确认记录重写（consecutiveAckSeq 可能为 0 但文件存在未确认条目，
	// 此时 readRemainingLocked(0) 返回全部条目，全部保留，不丢数据）
	remaining, err := w.readRemainingLocked(w.consecutiveAckSeq)
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
		// 乱序确认的条目（在 acked 集合中）已成功写入 CH，不再保留
		if _, acked := w.acked[e.Seq]; acked {
			continue
		}
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
	// 已确认条目已从文件移除，清空乱序 ack 集合并持久化（避免重启后误判）
	w.acked = make(map[uint64]struct{})
	w.persistAckLocked()
}

// readRemainingLocked 读取连续确认高水位之后的所有记录（未确认或乱序确认、仍需保留）。
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

// ReadAll 读取全部未确认记录（用于启动恢复重放）。
// H2：重启后确认状态已从侧车恢复，只重放 > consecutiveAckSeq 且不在乱序 ack 集合中的记录——
// 已确认（已成功写入 CH）的条目不再重复插入。
func (w *WAL) ReadAll() ([]walEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	entries, err := w.readRemainingLocked(w.consecutiveAckSeq)
	if err != nil {
		return nil, err
	}
	out := entries[:0]
	for _, e := range entries {
		if _, acked := w.acked[e.Seq]; acked {
			continue
		}
		out = append(out, e)
	}
	return out, nil
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
