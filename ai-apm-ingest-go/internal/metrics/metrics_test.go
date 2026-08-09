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
	if !strings.Contains(out, `service_requests_total{cluster="default", service="frontend"} 3`) {
		t.Fatalf("service_requests_total missing/wrong:\n%s", out)
	}
	if !strings.Contains(out, `service_errors_total{cluster="default", service="frontend"} 1`) {
		t.Fatalf("service_errors_total missing/wrong:\n%s", out)
	}
	if !strings.Contains(out, `service_request_duration_seconds_sum{cluster="default", service="frontend"} 0.000000001`) {
		t.Fatalf("service_request_duration_seconds_sum missing/wrong:\n%s", out)
	}
	if !strings.Contains(out, `service_request_duration_seconds_count{cluster="default", service="frontend"} 3`) {
		t.Fatalf("service_request_duration_seconds_count missing/wrong:\n%s", out)
	}
}

func TestServiceREDClusterIsolation(t *testing.T) {
	m := New()
	m.AddServiceREDForCluster("cluster-a", "payments", false, 0) // cluster-a 1 req
	m.AddServiceREDForCluster("cluster-b", "payments", true, 0)  // cluster-b 1 err (same svc, diff cluster)

	out := m.Snapshot()
	if !strings.Contains(out, `service_requests_total{cluster="cluster-a", service="payments"} 1`) {
		t.Fatalf("cluster-a requests missing:\n%s", out)
	}
	if !strings.Contains(out, `service_errors_total{cluster="cluster-b", service="payments"} 1`) {
		t.Fatalf("cluster-b errors missing:\n%s", out)
	}
	// cluster-a 不应有 error（仅 1 次成功调用）
	if strings.Contains(out, `service_errors_total{cluster="cluster-a", service="payments"} 1`) {
		t.Fatalf("cluster isolation broken (cluster-a should have 0 errors):\n%s", out)
	}
	// cluster-b 应有 reqs=1, errs=1（一次错误调用计为 1 req + 1 err），且与 cluster-a 隔离
	if !strings.Contains(out, `service_errors_total{cluster="cluster-b", service="payments"} 1`) {
		t.Fatalf("cluster-b errors missing:\n%s", out)
	}
	if strings.Contains(out, `service_requests_total{cluster="cluster-b", service="payments"} 2`) {
		t.Fatalf("cluster-b should have 1 req not 2:\n%s", out)
	}
}

func TestServiceREDReset(t *testing.T) {
	m := New()
	m.AddServiceRED("a", false, 0)
	m.ResetServiceRED()
	out := m.Snapshot()
	if strings.Contains(out, `service_requests_total{cluster="default", service="a"}`) {
		t.Fatalf("service RED should reset:\n%s", out)
	}
}
