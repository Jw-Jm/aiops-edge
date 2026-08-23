package telemetry

import (
	"strings"
	"testing"
	"time"
)

func TestVMRejectsInvalidClusterID(t *testing.T) {
	w := NewVictoriaMetricsWriter("http://vm:8428")
	labels := map[string]string{
		"tenant_id":  "3f3c3b3a-0000-4000-8000-000000000001",
		"cluster_id": "orbstack", // 非法：slug，非 canonical UUID
		"__name__":   "http_requests_total",
	}
	res := w.Write(labels, 1, time.Now())
	if res.ErrorCode != "INVALID_SCOPE" {
		t.Fatalf("expected INVALID_SCOPE, got %q (status=%s)", res.ErrorCode, res.Status)
	}
}

func TestVMRejectsMissingTenant(t *testing.T) {
	w := NewVictoriaMetricsWriter("http://vm:8428")
	labels := map[string]string{
		"cluster_id": "3f3c3b3a-0000-4000-8000-000000000002",
		"__name__":   "http_requests_total",
	}
	res := w.Write(labels, 1, time.Now())
	if res.ErrorCode != "INVALID_SCOPE" {
		t.Fatalf("expected INVALID_SCOPE for missing tenant, got %q", res.ErrorCode)
	}
}

func TestVMResourceScopeRequiresResourceID(t *testing.T) {
	w := NewVictoriaMetricsWriter("http://vm:8428")
	labels := map[string]string{
		"tenant_id":  "3f3c3b3a-0000-4000-8000-000000000001",
		"cluster_id": "3f3c3b3a-0000-4000-8000-000000000002",
		"__name__":   "cpu_usage",
	}
	// 显式 resource scope：resource-scoped metric 必须带 canonical UUID resource_id。
	res := w.WriteScope(labels, ScopeResource, 0.5, time.Now())
	if res.ErrorCode != "INVALID_SCOPE" {
		t.Fatalf("expected INVALID_SCOPE for resource scope missing resource_id, got %q", res.ErrorCode)
	}
}

func TestVMWriteScopeClusterNoResourceRequired(t *testing.T) {
	w := NewVictoriaMetricsWriter("http://vm:8428")
	labels := map[string]string{
		"tenant_id":  "3f3c3b3a-0000-4000-8000-000000000001",
		"cluster_id": "3f3c3b3a-0000-4000-8000-000000000002",
		"__name__":   "service_requests_total",
	}
	// cluster scope：resource_id 可选，应通过校验。
	res := w.WriteScope(labels, ScopeCluster, 7, time.Now())
	if res.ErrorCode != "" {
		t.Fatalf("expected ok for cluster scope, got %q", res.ErrorCode)
	}
}

func TestVMSerializeLine(t *testing.T) {
	w := NewVictoriaMetricsWriter("http://vm:8428")
	labels := map[string]string{
		"tenant_id":  "3f3c3b3a-0000-4000-8000-000000000001",
		"cluster_id": "3f3c3b3a-0000-4000-8000-000000000002",
		"__name__":   "http_requests_total",
		"service":    "checkout",
	}
	ts := time.Unix(1700000000, 0).UTC()
	line := w.serializeLine(labels, 42.5, ts)
	// 必须含 name 前缀、value、timestamp 与 scope labels
	for _, want := range []string{
		"http_requests_total{",
		"tenant_id=\"3f3c3b3a-0000-4000-8000-000000000001\"",
		"cluster_id=\"3f3c3b3a-0000-4000-8000-000000000002\"",
		"service=\"checkout\"",
		" 42.5 1700000000000",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("serializeLine missing %q, got: %s", want, line)
		}
	}
}
