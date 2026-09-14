package api

import (
	"strings"
	"testing"
)

// 跨租户读取是 S0：调用方自报的 tenant/cluster 不能作为可读范围的依据。
func TestEnforceLogScopeStripsClientReportedScope(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"plain", `_time:60m`},
		{"client tenant only", `_time:60m tenant_id:"attacker-tenant"`},
		{"client cluster only", `_time:60m cluster_id:"other-cluster"`},
		{"both self reported", `_time:60m tenant_id:"x" cluster_id:"y"`},
		{"unquoted self reported", `_time:60m tenant_id:x cluster_id:y service_name:checkout`},
		{"uppercase attempt", `_time:60m TENANT_ID:"x" CLUSTER_ID:"y"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := enforceLogScope(tc.query, "authoritative-tenant", "authoritative-cluster")
			if strings.Contains(got, "attacker-tenant") || strings.Contains(got, `"x"`) || strings.Contains(got, `"y"`) {
				t.Fatalf("client-reported scope leaked into enforced query: %s", got)
			}
			if !strings.Contains(got, `tenant_id:"authoritative-tenant"`) {
				t.Fatalf("authoritative tenant missing: %s", got)
			}
			if !strings.Contains(got, `cluster_id:"authoritative-cluster"`) {
				t.Fatalf("authoritative cluster missing: %s", got)
			}
			// 只能有一份 tenant/cluster 过滤项，避免 OR 逻辑绕过
			if strings.Count(got, "tenant_id:") != 1 || strings.Count(got, "cluster_id:") != 1 {
				t.Fatalf("scope filters must appear exactly once: %s", got)
			}
		})
	}
}

func TestEnforceLogScopeKeepsUserFiltersAndTimeWindow(t *testing.T) {
	got := enforceLogScope(`_time:30m service_name:checkout level:ERROR`, "t1", "c1")
	for _, want := range []string{"_time:30m", "service_name:checkout", "level:ERROR", `tenant_id:"t1"`, `cluster_id:"c1"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q preserved in %s", want, got)
		}
	}
}

func TestEnforceLogScopeFallsBackToDefaultWindow(t *testing.T) {
	got := enforceLogScope(`tenant_id:"x"`, "t1", "c1")
	if !strings.Contains(got, "_time:5m") {
		t.Fatalf("empty query must fall back to a bounded default window: %s", got)
	}
	if !strings.Contains(got, `tenant_id:"t1"`) {
		t.Fatalf("authoritative tenant missing: %s", got)
	}
}
