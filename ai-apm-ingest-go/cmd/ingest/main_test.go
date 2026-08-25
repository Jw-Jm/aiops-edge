package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-ingest-go/internal/telemetry"
)

func TestOTLPLogsReturnsRetryableFailureWhenSinkWriteFails(t *testing.T) {
	writeLog := func(string, string, string, string, string, time.Time) telemetry.WriteResult {
		return telemetry.WriteResult{Status: "error", ErrorCode: "WRITE_FAILED", Retryable: true, Message: "sink unavailable"}
	}
	h := newOTLPLogsHandler("cluster-1", 1<<20, writeLog)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/logs", strings.NewReader(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":"checkout"}]},"scopeLogs":[{"logRecords":[{"timeUnixNano":"1","severityText":"ERROR","body":{"stringValue":"boom"}}]}]}]}`))
	req.Header.Set("X-Tenant-ID", "tenant-1")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("sink failure status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
}
