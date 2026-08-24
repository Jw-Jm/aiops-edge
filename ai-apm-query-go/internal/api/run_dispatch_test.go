package api

import (
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
	orch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case hit <- r.Header.Get("X-Trusted-Request-Context"):
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer orch.Close()
	t.Setenv("AI_ORCHESTRATOR_URL", orch.URL)

	h, mock, cleanup := newTestRunsHandler()
	defer cleanup()

	// ScanPending → 1 行（含 dispatch fencing 列）
	mock.ExpectQuery(regexp.QuoteMeta("SELECT invocation_id, run_id")).
		WillReturnRows(sqlmock.NewRows([]string{"invocation_id", "run_id", "status", "dispatch_count",
			"next_retry_at", "dispatch_owner_id", "dispatch_epoch", "dispatch_token_hash",
			"dispatch_expires_at", "created_at", "updated_at"}).
			AddRow("inv-1", "run-1", "pending", 0, nil, nil, 0, nil, nil, time.Now(), time.Now()))
	// Claim → ok（fencing：owner/epoch/token）
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_run_outbox SET status = 'claimed'")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// runDAO.Get
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(sqlmock.NewRows([]string{"run_id", "request_id", "tenant_id", "principal",
			"principal_type", "session_id", "scope_kind", "primary_cluster_id", "intent",
			"action_mode", "target_type", "target_resource_id", "time_range_start",
			"time_range_end", "status", "state_version", "parent_run_id", "created_at",
			"updated_at", "finished_at", "last_event_sequence"}).
			AddRow("run-1", "req-1", "7ed01afc-cc79-4ecd-8767-a2befa6168ad", "91480408-9c2d-11f1-8271-bea176fe9f9f", "user",
				"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "single_cluster", "91771a6e-9c2d-11f1-8271-bea176fe9f9f", "investigate",
				"read_only", nil, nil, nil, nil, "created", 0, nil, time.Now(), time.Now(),
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
			AddRow("inv-1", "run-1", "pending", 0, nil, nil, 0, nil, nil, time.Now(), time.Now()))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_run_outbox SET status = 'claimed'")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(sqlmock.NewRows([]string{"run_id", "request_id", "tenant_id", "principal",
			"principal_type", "session_id", "scope_kind", "primary_cluster_id", "intent",
			"action_mode", "target_type", "target_resource_id", "time_range_start",
			"time_range_end", "status", "state_version", "parent_run_id", "created_at",
			"updated_at", "finished_at", "last_event_sequence"}).
			AddRow("run-1", "req-1", "7ed01afc-cc79-4ecd-8767-a2befa6168ad", "91480408-9c2d-11f1-8271-bea176fe9f9f", "user",
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
