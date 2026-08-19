package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRequestContextRoundTripAndValidation(t *testing.T) {
	payload := []byte(`{
        "version":1,
        "issuer":"ai-orchestrator",
        "audience":"ai-apm-query-go",
        "request_id":"11111111-1111-4111-8111-111111111111",
        "run_id":"22222222-2222-4222-8222-222222222222",
        "user_id":"33333333-3333-4333-8333-333333333333",
        "session_id":"44444444-4444-4444-8444-444444444444",
        "tenant_id":"55555555-5555-4555-8555-555555555555",
        "cluster_id":"66666666-6666-4666-8666-666666666666",
        "source":"planner",
        "capability":"kubernetes.read",
        "issued_at":"2026-08-19T10:00:00Z",
        "expires_at":"2026-08-19T10:00:30Z",
        "nonce":"77777777-7777-4777-8777-777777777777"
    }`)

	var context RequestContext
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
	if !strings.Contains(string(roundTrip), `"cluster_id":"66666666-6666-4666-8666-666666666666"`) {
		t.Fatalf("round trip lost canonical cluster_id: %s", roundTrip)
	}
}

func TestRequestContextRejectsClientAuthClaimsAndNonUUIDCluster(t *testing.T) {
	base := `{"version":1,"issuer":"ai-orchestrator","audience":"ai-apm-query-go","request_id":"11111111-1111-4111-8111-111111111111","run_id":"22222222-2222-4222-8222-222222222222","user_id":"33333333-3333-4333-8333-333333333333","session_id":"44444444-4444-4444-8444-444444444444","tenant_id":"55555555-5555-4555-8555-555555555555","cluster_id":"66666666-6666-4666-8666-666666666666","source":"planner","capability":"kubernetes.read","issued_at":"2026-08-19T10:00:00Z","expires_at":"2026-08-19T10:00:30Z","nonce":"77777777-7777-4777-8777-777777777777"}`

	var context RequestContext
	withRoles := strings.TrimSuffix(base, "}") + `,"roles":["admin"]}`
	if err := DecodeStrict([]byte(withRoles), &context); err == nil {
		t.Fatal("DecodeStrict() accepted roles in TrustedRequestContext")
	}

	withSlug := strings.Replace(base, `"cluster_id":"66666666-6666-4666-8666-666666666666"`, `"cluster_id":"prod-sg-01"`, 1)
	if err := DecodeStrict([]byte(withSlug), &context); err == nil {
		t.Fatal("DecodeStrict() accepted slug as canonical cluster_id")
	}
}
