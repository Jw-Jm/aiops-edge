package telemetry

import (
	"strings"
	"testing"
	"time"
)

func TestVLogsRejectsInvalidClusterID(t *testing.T) {
	w := NewVictoriaLogsWriter("http://vlogs:9428")
	labels := map[string]string{
		"tenant_id":  "3f3c3b3a-0000-4000-8000-000000000001",
		"cluster_id": "prod-cluster", // 非法：slug，非 canonical UUID
		"service_name": "checkout",
	}
	res := w.WriteLog(labels, "order placed", time.Now())
	if res.ErrorCode != "INVALID_SCOPE" {
		t.Fatalf("expected INVALID_SCOPE, got %q (status=%s)", res.ErrorCode, res.Status)
	}
}

func TestVLogsRejectsMissingTenant(t *testing.T) {
	w := NewVictoriaLogsWriter("http://vlogs:9428")
	labels := map[string]string{
		"cluster_id": "3f3c3b3a-0000-4000-8000-000000000002",
	}
	res := w.WriteLog(labels, "log line", time.Now())
	if res.ErrorCode != "INVALID_SCOPE" {
		t.Fatalf("expected INVALID_SCOPE for missing tenant, got %q", res.ErrorCode)
	}
}

func TestVLogsResourceScopeRequiresResourceID(t *testing.T) {
	w := NewVictoriaLogsWriter("http://vlogs:9428")
	labels := map[string]string{
		"tenant_id":  "3f3c3b3a-0000-4000-8000-000000000001",
		"cluster_id": "3f3c3b3a-0000-4000-8000-000000000002",
	}
	res := w.WriteLogScope(labels, ScopeResource, "log", time.Now())
	if res.ErrorCode != "INVALID_SCOPE" {
		t.Fatalf("expected INVALID_SCOPE for resource scope missing resource_id, got %q", res.ErrorCode)
	}
}

func TestVLogsSerializeJSONLine(t *testing.T) {
	w := NewVictoriaLogsWriter("http://vlogs:9428")
	labels := map[string]string{
		"tenant_id":   "3f3c3b3a-0000-4000-8000-000000000001",
		"cluster_id":  "3f3c3b3a-0000-4000-8000-000000000002",
		"service_name": "checkout",
	}
	ts := time.Unix(1700000000, 0).UTC()
	line := w.serializeJSONLine(labels, "order placed", ts)
	// JSON line 必须含 tenant_id、cluster_id 与 _msg。
	for _, want := range []string{
		`"tenant_id":"3f3c3b3a-0000-4000-8000-000000000001"`,
		`"cluster_id":"3f3c3b3a-0000-4000-8000-000000000002"`,
		`"_msg":"order placed"`,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("serializeJSONLine missing %q, got: %s", want, line)
		}
	}
}
