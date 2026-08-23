package main

import "testing"

func TestWALAppendAckReplay(t *testing.T) {
	dir := t.TempDir()
	w1, err := NewWAL(dir, "events.log")
	if err != nil {
		t.Fatal(err)
	}
	s1, _ := w1.Append("event", []byte("row-1"))
	s2, _ := w1.Append("event", []byte("row-2"))
	w1.Ack(s1)
	w1.Close() // 模拟崩溃/重启，ack 水位已持久化

	w2, err := NewWAL(dir, "events.log")
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	entries, err := w2.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Seq != s2 {
		t.Fatalf("expected only unacked seq=%d on replay, got %v", s2, entries)
	}
}

func TestWALCompactKeepsUnacked(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWAL(dir, "events.log")
	if err != nil {
		t.Fatal(err)
	}
	s1, _ := w.Append("event", []byte("row-1"))
	s2, _ := w.Append("event", []byte("row-2"))
	w.Ack(s1)
	w.Compact()
	entries, err := w.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Seq != s2 {
		t.Fatalf("expected only unacked seq=%d after compact, got %v", s2, entries)
	}
	w.Close()
}

func TestWALCompactionResetsSeqSafe(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWAL(dir, "events.log")
	if err != nil {
		t.Fatal(err)
	}
	s1, _ := w.Append("event", []byte("row-1"))
	w.Ack(s1)
	// 全 acked 时 compaction 应截断文件
	w.Compact()
	entries, err := w.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty after full ack compact, got %v", entries)
	}
	// 后续 append 不应产生重复 seq
	s3, _ := w.Append("event", []byte("row-3"))
	if s3 <= s1 {
		t.Fatalf("expected new seq > acked seq %d, got %d", s1, s3)
	}
	w.Close()
}

func TestWALPendingStatsBacklogObservable(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWAL(dir, "events.log")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	s1, _ := w.Append("event", []byte("aaaaaaaa"))
	s2, _ := w.Append("event", []byte("bbbbbbbb"))
	w.Ack(s1) // s1 已确认，s2 仍 pending

	st := w.PendingStats()
	if st.Records != 1 {
		t.Fatalf("expected 1 pending record after acking s1, got %d", st.Records)
	}
	if st.Bytes != 8 {
		t.Fatalf("expected 8 pending bytes, got %d", st.Bytes)
	}
	if st.Oldest == 0 {
		t.Fatal("expected oldest pending timestamp set")
	}
	if w.OldestAgeSeconds() < 0 {
		t.Fatal("oldest age must be >= 0")
	}
	_ = s2

	// 全部确认后积压归零。
	w.Ack(s2)
	if st := w.PendingStats(); st.Records != 0 {
		t.Fatalf("expected 0 pending after full ack, got %d", st.Records)
	}
	if age := w.OldestAgeSeconds(); age != 0 {
		t.Fatalf("expected 0 age when no pending, got %d", age)
	}
}

func TestWALPendingStatsCountsUnackedOnly(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWAL(dir, "events.log")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	s1, _ := w.Append("event", []byte("row-1"))
	s2, _ := w.Append("event", []byte("row-2"))
	s3, _ := w.Append("event", []byte("row-3"))
	w.Ack(s1)
	w.Ack(s3) // 乱序 ack，s2 仍 pending
	st := w.PendingStats()
	if st.Records != 1 {
		t.Fatalf("expected exactly s2 pending (1), got %d", st.Records)
	}
	_ = s1
	_ = s2
	_ = s3
}
