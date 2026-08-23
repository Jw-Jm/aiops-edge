package telemetry

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

var errBackendDown = errors.New("backend down")

// TestReliableWriterBackendDownThenRecover 证明 new HTTP writer 的 WAL 闭环：
// backend down → WAL pending 增加（数据不丢）→ backend recover → replay 成功 → 全部投递。
func TestReliableWriterBackendDownThenRecover(t *testing.T) {
	var delivered int64
	backendUp := int64(0) // 0=down, 1=up
	sender := func(entry reliableEntry) error {
		if atomic.LoadInt64(&backendUp) == 0 {
			return errBackendDown // backend down → 留在 pending
		}
		atomic.AddInt64(&delivered, 1)
		return nil
	}
	rw, err := NewReliableWriter(t.TempDir(), "vm-metric", sender)
	if err != nil {
		t.Fatal(err)
	}
	defer rw.Close()

	// backend down：连续写入 5 条 → 全部 pending，delivered=0（数据不丢）。
	for i := 0; i < 5; i++ {
		if err := rw.Enqueue(reliableEntry{Kind: "vm-metric", Payload: "line"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	if rw.Pending() != 5 {
		t.Fatalf("expected 5 pending when backend down, got %d", rw.Pending())
	}
	if atomic.LoadInt64(&delivered) != 0 {
		t.Fatalf("no entries should be delivered while backend down, got %d", delivered)
	}

	// backend recover：replay 应投递 pending（bounded retry）。
	atomic.StoreInt64(&backendUp, 1)
	if err := rw.Replay(); err != nil {
		t.Fatalf("replay after recover should succeed: %v", err)
	}
	if atomic.LoadInt64(&delivered) < 5 {
		t.Fatalf("expected >=5 delivered after recover, got %d", delivered)
	}
	if rw.Pending() != 0 {
		t.Fatalf("expected 0 pending after replay, got %d", rw.Pending())
	}
}

func TestReliableWriterBackoff(t *testing.T) {
	// 指数退避上限 30s。
	if got := Backoff(1, 100*time.Millisecond); got != 100*time.Millisecond {
		t.Fatalf("Backoff(1) = %v", got)
	}
	if got := Backoff(10, 100*time.Millisecond); got != 30*time.Second {
		t.Fatalf("Backoff(10) should cap at 30s, got %v", got)
	}
}
