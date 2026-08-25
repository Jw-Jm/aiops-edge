package contract

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func mustTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}

const (
	clusterA = "66666666-6666-4666-8666-666666666666"
	clusterB = "88888888-8888-4888-8888-888888888888"
	tenantID = "55555555-5555-4555-8555-555555555555"
)

func validRunInvocationContext(t *testing.T) RunInvocationContext {
	t.Helper()
	now := mustTime("2026-08-19T10:00:00Z")
	ctx := NewRunInvocationContext(
		"query-api", "ai-orchestrator", "11111111-1111-4111-8111-111111111111",
		"system", "33333333-3333-4333-8333-333333333333", "", tenantID, "run",
		[]string{clusterA}, now, now.Add(30*time.Second), "77777777-7777-4777-8777-777777777777",
	)
	ctx.Capability = "ai.investigate"
	return ctx
}

func TestInvestigationInvocationRequiresExistingRunIdentity(t *testing.T) {
	ctx := validRunInvocationContext(t)
	if err := ctx.Validate(); err == nil {
		t.Fatal("investigation invocation without run identity must be rejected")
	}
	ctx.RunID = "22222222-2222-4222-8222-222222222222"
	ctx.InvocationID = "99999999-9999-4999-8999-999999999999"
	if err := ctx.Validate(); err != nil {
		t.Fatalf("complete investigation invocation should validate: %v", err)
	}
}

func TestChatInvocationRejectsRunIdentity(t *testing.T) {
	ctx := validRunInvocationContext(t)
	ctx.Capability = "ai.chat"
	ctx.RunID = "22222222-2222-4222-8222-222222222222"
	ctx.InvocationID = "99999999-9999-4999-8999-999999999999"
	if err := ctx.Validate(); err == nil {
		t.Fatal("chat invocation must not carry run identity")
	}
}

func TestTrustedRequestContextClusterScope(t *testing.T) {
	payload := []byte(`{
        "version":1,
        "context_type":"trusted_request",
        "issuer":"ai-orchestrator",
        "audience":"ai-apm-query-go",
        "request_id":"11111111-1111-4111-8111-111111111111",
        "run_id":"22222222-2222-4222-8222-222222222222",
        "principal_type":"user",
        "principal_id":"33333333-3333-4333-8333-333333333333",
        "session_id":"44444444-4444-4444-8444-444444444444",
        "tenant_id":"` + tenantID + `",
        "scope_kind":"cluster",
        "cluster_id":"` + clusterA + `",
        "capability":"observability.logs.read",
        "source":"log_agent",
        "issued_at":"2026-08-19T10:00:00Z",
        "expires_at":"2026-08-19T10:00:30Z",
        "nonce":"77777777-7777-4777-8777-777777777777"
    }`)

	var context TrustedRequestContext
	if err := DecodeStrict(payload, &context); err != nil {
		t.Fatalf("DecodeStrict() error = %v", err)
	}
	if err := context.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	roundTrip, err := json.Marshal(context)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !strings.Contains(string(roundTrip), `"cluster_id":"`+clusterA+`"`) {
		t.Fatalf("round trip lost canonical cluster_id: %s", roundTrip)
	}
}

func TestTrustedRequestContextWorkloadKind(t *testing.T) {
	ctx := NewTrustedRequestContext(
		"ai-orchestrator", "ai-apm-query-go", "11111111-1111-4111-8111-111111111111",
		"system", "33333333-3333-4333-8333-333333333333", "", tenantID,
		"22222222-2222-4222-8222-222222222222", "cluster", clusterA,
		"observability.logs.read", "log_agent", mustTime("2026-08-19T10:00:00Z"),
		mustTime("2026-08-19T10:00:30Z"), "77777777-7777-4777-8777-777777777777",
	)
	for _, kind := range []string{"investigation", "chat", "platform"} {
		ctx.WorkloadKind = kind
		if err := ctx.Validate(); err != nil {
			t.Fatalf("workload_kind %q should validate: %v", kind, err)
		}
	}
	ctx.WorkloadKind = "admin"
	if err := ctx.Validate(); err == nil {
		t.Fatal("unsupported workload_kind must reject")
	}
}

func TestTrustedRequestContextRejectsAuthClaimsAndNonUUIDCluster(t *testing.T) {
	base := `{"version":1,"context_type":"trusted_request","issuer":"ai-orchestrator","audience":"ai-apm-query-go","request_id":"11111111-1111-4111-8111-111111111111","run_id":"22222222-2222-4222-8222-222222222222","principal_type":"user","principal_id":"33333333-3333-4333-8333-333333333333","session_id":"44444444-4444-4444-8444-444444444444","tenant_id":"` + tenantID + `","scope_kind":"cluster","cluster_id":"` + clusterA + `","capability":"observability.logs.read","source":"log_agent","issued_at":"2026-08-19T10:00:00Z","expires_at":"2026-08-19T10:00:30Z","nonce":"77777777-7777-4777-8777-777777777777"}`

	var context TrustedRequestContext
	withRoles := strings.TrimSuffix(base, "}") + `,"roles":["admin"]}`
	if err := DecodeStrict([]byte(withRoles), &context); err == nil {
		t.Fatal("DecodeStrict() accepted roles in TrustedRequestContext")
	}

	withSlug := strings.Replace(base, `"cluster_id":"`+clusterA+`"`, `"cluster_id":"prod-sg-01"`, 1)
	if err := DecodeStrict([]byte(withSlug), &context); err == nil {
		t.Fatal("DecodeStrict() accepted slug as canonical cluster_id")
	}
}

func TestTrustedRequestRunScopeForbidsClusterAndNonControlPlane(t *testing.T) {
	base := `{"version":1,"context_type":"trusted_request","issuer":"ai-orchestrator","audience":"ai-apm-query-go","request_id":"11111111-1111-4111-8111-111111111111","run_id":"22222222-2222-4222-8222-222222222222","principal_type":"user","principal_id":"33333333-3333-4333-8333-333333333333","session_id":"44444444-4444-4444-8444-444444444444","tenant_id":"` + tenantID + `","scope_kind":"run","cluster_id":"","capability":"control_plane.run.read","source":"orchestrator","issued_at":"2026-08-19T10:00:00Z","expires_at":"2026-08-19T10:00:30Z","nonce":"77777777-7777-4777-8777-777777777777"}`

	var context TrustedRequestContext
	if err := DecodeStrict([]byte(base), &context); err != nil {
		t.Fatalf("run scope control_plane should validate: %v", err)
	}

	withNonControl := strings.Replace(base, `"capability":"control_plane.run.read"`, `"capability":"observability.logs.read"`, 1)
	if err := DecodeStrict([]byte(withNonControl), &context); err == nil {
		t.Fatal("run scope with non-control-plane capability must reject")
	}

	withCluster := strings.Replace(base, `"cluster_id":""`, `"cluster_id":"`+clusterA+`"`, 1)
	if err := DecodeStrict([]byte(withCluster), &context); err == nil {
		t.Fatal("run scope with cluster_id must reject")
	}
}

func TestResourceRefDoesNotIncludeTenant(t *testing.T) {
	ns := "production"
	ref := ResourceRef{
		ClusterID:    clusterA,
		ResourceType: "service",
		Namespace:    &ns,
		Name:         "orders",
		ResourceID:   "service:" + clusterA + ":production:orders",
		TenantID:     tenantID,
	}
	if err := ref.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if strings.Contains(ref.ResourceID, tenantID) {
		t.Fatal("resource_id must NOT include tenant_id")
	}
}

func TestResourceRefRejectsTenantInIdAndSlug(t *testing.T) {
	ns := "production"
	withTenant := ResourceRef{
		ClusterID:    clusterA,
		ResourceType: "service",
		Namespace:    &ns,
		Name:         "orders",
		ResourceID:   "urn:aiops:" + tenantID + ":" + clusterA + ":service:production:orders",
		TenantID:     tenantID,
	}
	if err := withTenant.Validate(); err == nil {
		t.Fatal("resource_id with tenant must reject")
	}

	withSlug := ResourceRef{
		ClusterID:    clusterA,
		ResourceType: "service",
		Namespace:    &ns,
		Name:         "orders",
		ResourceID:   "service:prod-sg-01:production:orders",
		TenantID:     tenantID,
	}
	if err := withSlug.Validate(); err == nil {
		t.Fatal("resource_id with slug must reject")
	}
}

func TestToolResultSuccessEmptyIsNoData(t *testing.T) {
	empty := ToolResult{
		ToolName:   "k8sgpt_diagnose",
		ClusterID:  clusterA,
		Success:    true,
		Status:     "no_data",
		Summary:    "k8sgpt executed successfully but produced no diagnostics",
		StartedAt:  mustTime("2026-08-19T10:00:01Z"),
		FinishedAt: mustTime("2026-08-19T10:00:02Z"),
	}
	if err := empty.Validate(); err != nil {
		t.Fatalf("success=true status=no_data should validate: %v", err)
	}

	bad := ToolResult{
		ToolName:   "k8sgpt_diagnose",
		ClusterID:  clusterA,
		Success:    true,
		Status:     "failed",
		StartedAt:  mustTime("2026-08-19T10:00:01Z"),
		FinishedAt: mustTime("2026-08-19T10:00:02Z"),
	}
	if err := bad.Validate(); err == nil {
		t.Fatal("success=true status=failed must reject")
	}
}

func TestToolResultErrorNotOnWire(t *testing.T) {
	// Bugbot C3：Go 非空 Error 也不得输出第 16 个 "error" 字段，三端 wire 严格 15 字段。
	tr := ToolResult{
		ToolName:     "query_logs",
		ClusterID:    clusterA,
		Success:      false,
		Status:       "failed",
		Summary:      "boom",
		ErrorCode:    "INTERNAL_ERROR",
		ErrorMessage: "structured failure",
		Error: &StructuredError{
			ErrorCode: "INTERNAL_ERROR",
			Message:   "structured failure",
		},
		StartedAt:  mustTime("2026-08-19T10:00:01Z"),
		FinishedAt: mustTime("2026-08-19T10:00:02Z"),
	}
	raw, err := json.Marshal(tr)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}
	if strings.Contains(string(raw), `"error":`) {
		t.Fatalf("wire 上不得出现第 16 个 'error' 字段（三端 15 字段对齐），got: %s", raw)
	}
}
