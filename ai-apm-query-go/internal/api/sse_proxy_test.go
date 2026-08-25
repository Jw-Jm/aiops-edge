package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestStreamRunEventsRejectsUnauthenticated(t *testing.T) {
	h, _, cleanup := newTestRunsHandler()
	defer cleanup()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/runs/run-1/events", nil)
	rec := httptest.NewRecorder()
	h.StreamRunEvents(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestStreamRunEventsRejectsCrossTenant(t *testing.T) {
	h, mock, cleanup := newTestRunsHandler()
	defer cleanup()
	// runDAO.Get returns run with different tenant
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(airunMockRows("run-1", "created", 0))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/runs/run-1/events", nil)
	req = withAuthorizationContext(req, AuthorizationContext{
		UserID: "91480408-9c2d-11f1-8271-bea176fe9f9f", TenantID: "other-tenant-not-this",
	})
	rec := httptest.NewRecorder()
	h.StreamRunEvents(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestStreamRunEventsRejectsExpiredCursor(t *testing.T) {
	h, mock, cleanup := newTestRunsHandler()
	defer cleanup()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(airunMockRows("run-1", "created", 0))
	// retention 前置检查：LastSequence=10000，after_sequence=0 → 超窗立即拒绝。
	mock.ExpectQuery(regexp.QuoteMeta("SELECT last_event_sequence FROM ai_runs")).
		WillReturnRows(sqlmock.NewRows([]string{"last_event_sequence"}).AddRow(int64(sseRetentionWindow + 5000)))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/runs/run-1/events", nil)
	req = withAuthorizationContext(req, AuthorizationContext{
		UserID:    "91480408-9c2d-11f1-8271-bea176fe9f9f",
		TenantID:  "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
		SessionID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
	})
	rec := httptest.NewRecorder()
	h.StreamRunEvents(rec, req)
	if !strings.Contains(rec.Body.String(), "SSE_RETENTION_EXCEEDED") {
		t.Fatalf("expected SSE_RETENTION_EXCEEDED, got: %q", rec.Body.String())
	}
}

func TestStreamRunEventsReplayWritesEvents(t *testing.T) {
	h, mock, cleanup := newTestRunsHandler()
	defer cleanup()
	// runDAO.Get → tenant matches
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(airunMockRows("run-1", "created", 0))
	// P1-5：retention 检查前置 → LastSequence（cursor 未超窗）
	mock.ExpectQuery(regexp.QuoteMeta("SELECT last_event_sequence FROM ai_runs")).
		WillReturnRows(sqlmock.NewRows([]string{"last_event_sequence"}).AddRow(int64(1)))
	// eventDAO.ReplayAfter → 1 event
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, sequence, event_id")).
		WillReturnRows(sqlmock.NewRows([]string{"run_id", "sequence", "event_id", "event_type", "payload_json", "created_at"}).
			AddRow("run-1", int64(1), "e1", "status", []byte(`{"x":1}`), time.Now()))

	// cancellable context：启动 handler 读一次 replay，然后 cancel 结束 live-tail 循环。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/runs/run-1/events", nil).WithContext(ctx)
	req = withAuthorizationContext(req, AuthorizationContext{
		UserID:    "91480408-9c2d-11f1-8271-bea176fe9f9f",
		TenantID:  "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
		SessionID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
	})
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.StreamRunEvents(rec, req)
		close(done)
	}()
	// 给 goroutine 一点时间写 replay，然后 cancel。
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not exit after cancel")
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: run_event") {
		t.Fatalf("expected run_event frame in SSE body, got: %q", body)
	}
	if !strings.Contains(body, `"event_id":"e1"`) {
		t.Fatalf("expected e1 in SSE body, got: %q", body)
	}
	if !strings.Contains(body, `"payload":{"x":1}`) {
		t.Fatalf("event payload must remain JSON, got: %q", body)
	}
	if !strings.Contains(body, "id: 1\n") {
		t.Fatalf("event sequence must be exposed as SSE id, got: %q", body)
	}
}
