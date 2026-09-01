package main

import (
	"fmt"
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

func TestParseEnvBoolDefault(t *testing.T) {
	t.Setenv("OTLP_GRPC_ENABLED", "off")
	if got := parseEnvBoolDefault("OTLP_GRPC_ENABLED", true); got {
		t.Fatal("off should disable OTLP/gRPC")
	}
	t.Setenv("OTLP_GRPC_ENABLED", "true")
	if got := parseEnvBoolDefault("OTLP_GRPC_ENABLED", false); !got {
		t.Fatal("true should enable OTLP/gRPC")
	}
	t.Setenv("OTLP_GRPC_ENABLED", "")
	if got := parseEnvBoolDefault("OTLP_GRPC_ENABLED", true); !got {
		t.Fatal("empty value should use the supplied default")
	}
}

func TestValidateEventBatchRejectsMalformedTimestampBeforeWAL(t *testing.T) {
	tenant := "7ed01afc-cc79-4ecd-8767-a2befa6168ad"
	cluster := "91771a6e-9c2d-11f1-8271-bea176fe9f9f"
	valid := func(ts, bucket string) []byte {
		return []byte(fmt.Sprintf("%s\t%s\t%s\tdefault\tPod\tmarker\tValidation\tWarning\tmarker\tPod/marker\tcollector\tkubernetes\t\t%s\t%s\n", tenant, cluster, ts, bucket, strings.Repeat("a", 64)))
	}
	if err := validateEventBatch(valid("2026-09-01 15:20:00.123456789", "2026-09-01 15:20:00"), tenant, cluster); err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
	if err := validateEventBatch(valid("2026-09-01T15:20:00.123456789Z", "2026-09-01 15:20:00"), tenant, cluster); err == nil {
		t.Fatal("ISO timestamp with timezone must be rejected before WAL append")
	}
	if err := validateEventBatch(valid("2026-09-01 15:20:00.123456789", "2026-09-01 15:21:00"), tenant, cluster); err == nil {
		t.Fatal("mismatched time_bucket must be rejected before WAL append")
	}
}
