package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEventWALRejectsBeforeExceedingConfiguredLimit(t *testing.T) {
	w, err := newEventWAL(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.AppendBounded("tenant", "cluster", []byte("row\n"), 1); err != errEventWALFull {
		t.Fatalf("expected WAL full error, got %v", err)
	}
}

func TestEventWALAcceptsBeforeSinkAndReplaysAfterRestart(t *testing.T) {
	dir := t.TempDir()
	w, err := newEventWAL(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := w.Append("tenant", "cluster", []byte("row\n"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.ReceiptID == "" || w.PendingCount() != 1 {
		t.Fatalf("append did not create pending durable receipt: %+v pending=%d", rec, w.PendingCount())
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	w2, err := newEventWAL(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	items := w2.Pending(10)
	if len(items) != 1 || items[0].ReceiptID != rec.ReceiptID {
		t.Fatalf("restart did not recover pending receipt: %+v", items)
	}
	if err := w2.Ack(rec.Seq); err != nil {
		t.Fatal(err)
	}
	if w2.PendingCount() != 0 {
		t.Fatalf("ack did not remove pending record")
	}
}

func TestEventWALReplayDoesNotAckFailedSink(t *testing.T) {
	w, err := newEventWAL(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	_, err = w.Append("tenant", "cluster", []byte("row\n"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := make(chan struct{}, 1)
	w.StartReplay(ctx, func(context.Context, []byte) error {
		called <- struct{}{}
		return errors.New("backend down")
	}, time.Millisecond)
	select {
	case <-called:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("replay worker did not attempt sink")
	}
	if w.PendingCount() != 1 {
		t.Fatalf("failed sink must remain pending, got %d", w.PendingCount())
	}
}
