package clickhouse

import (
	"testing"
)

// TestWALRestartRestoresAckWatermark 覆盖 H2 数据丢失 bug：
// 重启后 consecutiveAckSeq 必须从侧车文件恢复，Compact 只截断已 ack 前缀，
// 未 ack 条目（含乱序确认留下的低 seq）不得被 Truncate(0) 清空。
func TestWALRestartRestoresAckWatermark(t *testing.T) {
	dir := t.TempDir()

	// 实例 1：写入 3 条，ack 1 和 3（2 未确认，乱序确认），不 compact 直接关闭（模拟崩溃/重启）
	wal1, err := NewWALFile(dir, "wal-test.log")
	if err != nil {
		t.Fatal(err)
	}
	s1, err := wal1.Append("span", []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	s2, err := wal1.Append("span", []byte("row-2"))
	if err != nil {
		t.Fatal(err)
	}
	s3, err := wal1.Append("span", []byte("row-3"))
	if err != nil {
		t.Fatal(err)
	}
	wal1.Ack(s1)
	wal1.Ack(s3) // 乱序确认：水位应停在 1
	if wal1.consecutiveAckSeq != 1 {
		t.Fatalf("expected consecutiveAckSeq=1, got %d", wal1.consecutiveAckSeq)
	}
	wal1.Close()

	// 实例 2：模拟重启，水位应从侧车文件恢复（修复前为 0）
	wal2, err := NewWALFile(dir, "wal-test.log")
	if err != nil {
		t.Fatal(err)
	}
	defer wal2.Close()
	if wal2.consecutiveAckSeq != 1 {
		t.Fatalf("restart: expected consecutiveAckSeq=1 restored, got %d", wal2.consecutiveAckSeq)
	}

	// Compact（文件超阈值时触发）：只应截断已 ack 前缀，未 ack 的 seq=2 必须保留
	wal2.Compact()
	entries, err := wal2.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	foundUnacked := false
	for _, e := range entries {
		if e.Seq == s2 {
			foundUnacked = true
		}
		if e.Seq == s1 || e.Seq == s3 {
			t.Fatalf("acked entry seq=%d should be removed after compact, got %v", e.Seq, entries)
		}
	}
	if !foundUnacked {
		t.Fatalf("unacked entry seq=%d lost after compact: %v", s2, entries)
	}
}

// TestWALCompactKeepsUnackedAfterRestartWriteAck 覆盖完整生命周期：
// 重启后写入更多、ack、压缩，未 ack 数据始终保留。
func TestWALCompactKeepsUnackedAfterRestartWriteAck(t *testing.T) {
	dir := t.TempDir()

	wal1, err := NewWALFile(dir, "wal-test2.log")
	if err != nil {
		t.Fatal(err)
	}
	s1, _ := wal1.Append("span", []byte("row-1"))
	s2, _ := wal1.Append("span", []byte("row-2"))
	wal1.Ack(s1)
	wal1.Close()

	// 重启
	wal2, err := NewWALFile(dir, "wal-test2.log")
	if err != nil {
		t.Fatal(err)
	}
	defer wal2.Close()
	s3, _ := wal2.Append("span", []byte("row-3"))
	s4, _ := wal2.Append("span", []byte("row-4"))
	wal2.Ack(s2) // 水位推进到 2
	wal2.Ack(s3) // 水位推进到 3

	// 压缩：只保留 >3 的条目（即 seq=4）
	wal2.Compact()
	entries, err := wal2.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Seq != s4 {
		t.Fatalf("after compact expected only seq=%d, got %v", s4, entries)
	}

	// 再写一条未 ack，再压缩，不能丢
	s5, _ := wal2.Append("span", []byte("row-5"))
	wal2.Compact()
	entries, err = wal2.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	got := map[uint64]bool{}
	for _, e := range entries {
		got[e.Seq] = true
	}
	if len(entries) != 2 || !got[s4] || !got[s5] {
		t.Fatalf("expected seq=%d and seq=%d after compact, got %v", s4, s5, entries)
	}
}

// TestWALRestartReplaySkipsAcked 验证重启后 ReadAll 只重放未确认记录，
// 已确认（已写入 CH）的条目不重复插入。
func TestWALRestartReplaySkipsAcked(t *testing.T) {
	dir := t.TempDir()

	wal1, err := NewWALFile(dir, "wal-test3.log")
	if err != nil {
		t.Fatal(err)
	}
	s1, _ := wal1.Append("span", []byte("row-1"))
	_, _ = wal1.Append("span", []byte("row-2"))
	s3, _ := wal1.Append("span", []byte("row-3"))
	wal1.Ack(s1)
	wal1.Ack(s3)
	wal1.Close()

	wal2, err := NewWALFile(dir, "wal-test3.log")
	if err != nil {
		t.Fatal(err)
	}
	defer wal2.Close()
	entries, err := wal2.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Seq != 2 {
		t.Fatalf("expected only unacked seq=2 on replay, got %v", entries)
	}
}
