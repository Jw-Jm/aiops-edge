package main

// Durable event acceptance WAL. The HTTP event endpoint appends the validated
// batch and fsyncs before returning 202. A bounded replay loop drains records
// to ClickHouse and only then advances the ack log. This keeps the collector
// contract independent from ClickHouse availability and makes restart replay
// deterministic.

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type eventWALRecord struct {
	Seq       uint64 `json:"seq"`
	ReceiptID string `json:"receipt_id"`
	TenantID  string `json:"tenant_id"`
	ClusterID string `json:"cluster_id"`
	Body      string `json:"body"` // base64-encoded TabSeparated batch
	At        int64  `json:"at"`
}

type eventWAL struct {
	mu      sync.Mutex
	path    string
	ackPath string
	file    *os.File
	ackFile *os.File
	seq     uint64
	pending map[uint64]eventWALRecord
}

var errEventWALFull = fmt.Errorf("event acceptance WAL is full")

func newEventWAL(dir string) (*eventWAL, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "event-acceptance.log")
	ackPath := path + ".ack"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, err
	}
	af, err := os.OpenFile(ackPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	w := &eventWAL{path: path, ackPath: ackPath, file: f, ackFile: af, pending: make(map[uint64]eventWALRecord)}
	if err := w.recover(); err != nil {
		_ = f.Close()
		_ = af.Close()
		return nil, err
	}
	return w, nil
}

func (w *eventWAL) recover() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	acked := make(map[uint64]struct{})
	if f, err := os.Open(w.ackPath); err == nil {
		s := bufio.NewScanner(f)
		for s.Scan() {
			var seq uint64
			if _, err := fmt.Sscanf(strings.TrimSpace(s.Text()), "%d", &seq); err == nil {
				acked[seq] = struct{}{}
			}
		}
		_ = f.Close()
		if err := s.Err(); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.Open(w.path)
	if err != nil {
		return err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	buf := make([]byte, 64*1024)
	s.Buffer(buf, 32*1024*1024)
	for s.Scan() {
		var rec eventWALRecord
		if err := json.Unmarshal(s.Bytes(), &rec); err != nil {
			return fmt.Errorf("decode event WAL: %w", err)
		}
		if rec.Seq > w.seq {
			w.seq = rec.Seq
		}
		if _, ok := acked[rec.Seq]; !ok {
			w.pending[rec.Seq] = rec
		}
	}
	return s.Err()
}

func (w *eventWAL) Append(tenantID, clusterID string, body []byte) (eventWALRecord, error) {
	return w.appendBounded(tenantID, clusterID, body, 0)
}

func (w *eventWAL) AppendBounded(tenantID, clusterID string, body []byte, maxBytes int64) (eventWALRecord, error) {
	return w.appendBounded(tenantID, clusterID, body, maxBytes)
}

func (w *eventWAL) appendBounded(tenantID, clusterID string, body []byte, maxBytes int64) (eventWALRecord, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seq++
	receipt, err := randomReceiptID()
	if err != nil {
		return eventWALRecord{}, err
	}
	rec := eventWALRecord{
		Seq: w.seq, ReceiptID: receipt, TenantID: tenantID, ClusterID: clusterID,
		Body: base64.StdEncoding.EncodeToString(body), At: time.Now().UnixMilli(),
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return eventWALRecord{}, err
	}
	if maxBytes > 0 {
		if st, statErr := w.file.Stat(); statErr != nil {
			return eventWALRecord{}, statErr
		} else if st.Size()+int64(len(raw)+1) > maxBytes {
			return eventWALRecord{}, errEventWALFull
		}
	}
	if _, err := w.file.Write(append(raw, '\n')); err != nil {
		return eventWALRecord{}, err
	}
	if err := w.file.Sync(); err != nil {
		return eventWALRecord{}, err
	}
	w.pending[rec.Seq] = rec
	return rec, nil
}

func (w *eventWAL) Pending(limit int) []eventWALRecord {
	w.mu.Lock()
	defer w.mu.Unlock()
	items := make([]eventWALRecord, 0, len(w.pending))
	for _, rec := range w.pending {
		items = append(items, rec)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Seq < items[j].Seq })
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

func (w *eventWAL) Ack(seq uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.pending[seq]; !ok {
		return nil
	}
	if _, err := fmt.Fprintf(w.ackFile, "%d\n", seq); err != nil {
		return err
	}
	if err := w.ackFile.Sync(); err != nil {
		return err
	}
	delete(w.pending, seq)
	return nil
}

func (w *eventWAL) PendingCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.pending)
}

func (w *eventWAL) SizeBytes() int64 {
	if w == nil {
		return 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	st, err := w.file.Stat()
	if err != nil {
		return 0
	}
	return st.Size()
}

func (w *eventWAL) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var first error
	if err := w.file.Sync(); err != nil {
		first = err
	}
	if err := w.ackFile.Sync(); err != nil && first == nil {
		first = err
	}
	if err := w.file.Close(); err != nil && first == nil {
		first = err
	}
	if err := w.ackFile.Close(); err != nil && first == nil {
		first = err
	}
	return first
}

// StartReplay drains durable records in sequence order. A failed record stays
// pending and is retried on the next tick; no record is acknowledged before
// the sink callback returns nil.
func (w *eventWAL) StartReplay(ctx context.Context, sink func(context.Context, []byte) error, interval time.Duration) {
	if w == nil || sink == nil {
		return
	}
	if interval <= 0 {
		interval = time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			for _, rec := range w.Pending(16) {
				body, err := decodeEventWALBody(rec)
				if err != nil {
					// A corrupt record is never silently ACKed. Keep it pending so
					// readiness/operational tooling can quarantine it explicitly.
					continue
				}
				if err := sink(ctx, body); err != nil {
					break
				}
				_ = w.Ack(rec.Seq)
			}
		}
	}()
}

func decodeEventWALBody(rec eventWALRecord) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(rec.Body)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	return b, nil
}

func randomReceiptID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "evt_" + hex.EncodeToString(b), nil
}
