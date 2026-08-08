package metrics

import (
	"strings"
	"testing"
)

func TestSnapshotServiceRED(t *testing.T) {
	m := New()
	m.AddServiceRED("frontend", false, 0) // 1 call
	m.AddServiceRED("frontend", false, 1) // 2nd call
	m.AddServiceRED("frontend", true, 0)  // 1 error

	out := m.Snapshot()
	if !strings.Contains(out, `service_requests_total{service="frontend"} 3`) {
		t.Fatalf("service_requests_total missing/wrong:\n%s", out)
	}
	if !strings.Contains(out, `service_errors_total{service="frontend"} 1`) {
		t.Fatalf("service_errors_total missing/wrong:\n%s", out)
	}
	if !strings.Contains(out, `service_request_duration_seconds_sum{service="frontend"} 0.000000001`) {
		t.Fatalf("service_request_duration_seconds_sum missing/wrong:\n%s", out)
	}
	if !strings.Contains(out, `service_request_duration_seconds_count{service="frontend"} 3`) {
		t.Fatalf("service_request_duration_seconds_count missing/wrong:\n%s", out)
	}
}

func TestServiceREDReset(t *testing.T) {
	m := New()
	m.AddServiceRED("a", false, 0)
	m.ResetServiceRED()
	out := m.Snapshot()
	if strings.Contains(out, `service_requests_total{service="a"}`) {
		t.Fatalf("service RED should reset:\n%s", out)
	}
}
