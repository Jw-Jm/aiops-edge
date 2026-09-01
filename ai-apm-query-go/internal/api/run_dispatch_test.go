package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestRunDispatchDelivers(t *testing.T) {
	configureRunInvocationIssuer(t)
	hit := make(chan string, 1)
	bodyHit := make(chan map[string]interface{}, 1)
	orch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case hit <- r.Header.Get("X-Trusted-Request-Context"):
		default:
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]interface{}
		_ = json.Unmarshal(raw, &body)
		select {
		case bodyHit <- body:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer orch.Close()
	t.Setenv("AI_ORCHESTRATOR_URL", orch.URL)

	h, mock, cleanup := newTestRunsHandler()
	defer cleanup()
	windowStart := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 8, 31, 1, 0, 0, 0, time.UTC)

	// ScanPending → 1 行（含 dispatch fencing 列）
	mock.ExpectQuery(regexp.QuoteMeta("SELECT invocation_id, run_id")).
		WillReturnRows(sqlmock.NewRows([]string{"invocation_id", "run_id", "status", "dispatch_count",
			"next_retry_at", "dispatch_owner_id", "dispatch_epoch", "dispatch_token_hash",
			"dispatch_expires_at", "created_at", "updated_at"}).
			AddRow("99999999-9999-4999-8999-999999999999", "22222222-2222-4222-8222-222222222222", "pending", 0, nil, nil, 0, nil, nil, time.Now(), time.Now()))
	// Claim → ok（fencing：owner/epoch/token）
	mock.ExpectExec("UPDATE ai_run_outbox SET status = 'claimed'.*dispatch_epoch = LAST_INSERT_ID\\(dispatch_epoch \\+ 1\\)").
		WillReturnResult(sqlmock.NewResult(1, 1))
	// runDAO.Get
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(sqlmock.NewRows([]string{"run_id", "request_id", "tenant_id", "principal",
			"principal_type", "session_id", "scope_kind", "primary_cluster_id", "intent",
			"action_mode", "target_type", "target_resource_id", "time_range_start",
			"time_range_end", "status", "state_version", "parent_run_id", "created_at",
			"updated_at", "finished_at", "last_event_sequence"}).
			AddRow("22222222-2222-4222-8222-222222222222", "11111111-1111-4111-8111-111111111111", "7ed01afc-cc79-4ecd-8767-a2befa6168ad", "91480408-9c2d-11f1-8271-bea176fe9f9f", "user",
				"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "single_cluster", "91771a6e-9c2d-11f1-8271-bea176fe9f9f", "investigate",
				"read_only", nil, nil, windowStart, windowEnd, "created", 0, nil, time.Now(), time.Now(),
				nil, 0))
	// Deliver（fencing）
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_run_outbox SET status = 'delivered'")).
		WillReturnResult(sqlmock.NewResult(0, 1))

	h.dispatchPending()

	select {
	case ctxVal := <-hit:
		if ctxVal == "" {
			t.Fatalf("missing X-Trusted-Request-Context")
		}
	default:
		t.Fatalf("orchestrator was never hit")
	}
	select {
	case body := <-bodyHit:
		if body["time_range_start"] != windowStart.Format(time.RFC3339Nano) || body["time_range_end"] != windowEnd.Format(time.RFC3339Nano) || body["symptom_time"] != windowEnd.Format(time.RFC3339Nano) {
			t.Fatalf("dispatch did not carry frozen window: %#v", body)
		}
	default:
		t.Fatal("orchestrator body was not captured")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations not met: %v", err)
	}
}

func TestRunDispatchRetriesOnOrchestratorDown(t *testing.T) {
	configureRunInvocationIssuer(t)
	// orchestrator 返回 500
	orch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer orch.Close()
	t.Setenv("AI_ORCHESTRATOR_URL", orch.URL)

	h, mock, cleanup := newTestRunsHandler()
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT invocation_id, run_id")).
		WillReturnRows(sqlmock.NewRows([]string{"invocation_id", "run_id", "status", "dispatch_count",
			"next_retry_at", "dispatch_owner_id", "dispatch_epoch", "dispatch_token_hash",
			"dispatch_expires_at", "created_at", "updated_at"}).
			AddRow("99999999-9999-4999-8999-999999999999", "22222222-2222-4222-8222-222222222222", "pending", 0, nil, nil, 0, nil, nil, time.Now(), time.Now()))
	mock.ExpectExec("UPDATE ai_run_outbox SET status = 'claimed'.*dispatch_epoch = LAST_INSERT_ID\\(dispatch_epoch \\+ 1\\)").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(sqlmock.NewRows([]string{"run_id", "request_id", "tenant_id", "principal",
			"principal_type", "session_id", "scope_kind", "primary_cluster_id", "intent",
			"action_mode", "target_type", "target_resource_id", "time_range_start",
			"time_range_end", "status", "state_version", "parent_run_id", "created_at",
			"updated_at", "finished_at", "last_event_sequence"}).
			AddRow("22222222-2222-4222-8222-222222222222", "11111111-1111-4111-8111-111111111111", "7ed01afc-cc79-4ecd-8767-a2befa6168ad", "91480408-9c2d-11f1-8271-bea176fe9f9f", "user",
				"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "single_cluster", "91771a6e-9c2d-11f1-8271-bea176fe9f9f", "investigate",
				"read_only", nil, nil, nil, nil, "created", 0, nil, time.Now(), time.Now(),
				nil, 0))
	// 失败 → Retry（fencing）
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_run_outbox SET status = 'pending'")).
		WillReturnResult(sqlmock.NewResult(0, 1))

	h.dispatchPending()

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations not met: %v", err)
	}
}
