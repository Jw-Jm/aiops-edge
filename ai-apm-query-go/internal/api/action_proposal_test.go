package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

func TestValidateActionProposalRequestAcceptsAllCanonicalK8sActions(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		operation    string
		params       string
	}{
		{"restart deployment", "deployment", "rollout_restart", `{}`},
		{"restart statefulset", "statefulset", "rollout_restart", `{}`},
		{"restart daemonset", "daemonset", "rollout_restart", `{}`},
		{"scale deployment", "deployment", "scale", `{"replicas":2}`},
		{"scale statefulset", "statefulset", "scale", `{"replicas":2}`},
		{"delete pod", "pod", "delete_pod", `{"grace_period_seconds":30}`},
		{"evict pod", "pod", "evict_pod", `{"grace_period_seconds":30}`},
		{"cordon node", "node", "cordon", `{}`},
		{"uncordon node", "node", "uncordon", `{}`},
		{"drain node", "node", "drain", `{"drain_timeout":300}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := ActionProposalRequest{
				IdempotencyKey: "retry-1", ClusterID: "3f3c3b3a-0000-4000-8000-000000000001",
				ResourceType: tt.resourceType, Namespace: "prod", TargetName: "orders",
				Operation: tt.operation, Params: json.RawMessage(tt.params),
			}
			if err := validateActionProposalRequest(req); err != nil {
				t.Fatalf("valid proposal rejected: %v", err)
			}
		})
	}
}

func TestValidateActionProposalRequestRejectsMismatchedResourceAndOperation(t *testing.T) {
	tests := []ActionProposalRequest{
		{ResourceType: "deployment", Operation: "delete_pod", Namespace: "prod", TargetName: "orders", Params: json.RawMessage(`{"grace_period_seconds":30}`)},
		{ResourceType: "pod", Operation: "scale", Namespace: "prod", TargetName: "orders", Params: json.RawMessage(`{"replicas":2}`)},
		{ResourceType: "node", Operation: "rollout_restart", TargetName: "node-a", Params: json.RawMessage(`{}`)},
		{ResourceType: "service", Operation: "scale", Namespace: "prod", TargetName: "orders", Params: json.RawMessage(`{"replicas":2}`)},
	}
	for _, req := range tests {
		if err := validateActionProposalRequest(req); err == nil {
			t.Fatalf("mismatched proposal unexpectedly accepted: %#v", req)
		}
	}
}

func TestValidateActionProposalRequestRejectsUnsafeParameters(t *testing.T) {
	tests := []ActionProposalRequest{
		{ResourceType: "deployment", Operation: "scale", Namespace: "prod", TargetName: "orders", Params: json.RawMessage(`{"replicas":-1}`)},
		{ResourceType: "deployment", Operation: "scale", Namespace: "prod", TargetName: "orders", Params: json.RawMessage(`{"replicas":1.5}`)},
		{ResourceType: "pod", Operation: "delete_pod", Namespace: "prod", TargetName: "orders", Params: json.RawMessage(`{"grace_period_seconds":601}`)},
		{ResourceType: "node", Operation: "drain", TargetName: "node-a", Params: json.RawMessage(`{"drain_timeout":0}`)},
		{ResourceType: "deployment", Operation: "rollout_restart", Namespace: "prod", TargetName: "orders", Params: json.RawMessage(`{"raw_json_patch":[{"op":"replace"}]}`)},
	}
	for _, req := range tests {
		if err := validateActionProposalRequest(req); err == nil {
			t.Fatalf("unsafe proposal unexpectedly accepted: %#v", req)
		}
	}
}

func TestActionRiskIsServerOwned(t *testing.T) {
	if got := actionRiskForOperation("scale"); got != "R2" {
		t.Fatalf("scale risk = %q, want R2", got)
	}
	for _, operation := range []string{"delete_pod", "evict_pod", "cordon", "drain"} {
		if got := actionRiskForOperation(operation); got != "R3" {
			t.Fatalf("%s risk = %q, want R3", operation, got)
		}
	}
	if got := actionRiskForOperation("rollout_restart"); got != "R2" {
		t.Fatalf("rollout_restart risk = %q, want R2", got)
	}
	if got := actionRiskForOperation(strings.Repeat("x", 10)); got != "R0" {
		t.Fatalf("unknown operation risk = %q, want R0", got)
	}
}

func TestActionProposalPublicHandlerCreatesAwaitingApprovalAction(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(previous) })

	clusterID := "3f3c3b3a-0000-4000-8000-000000000001"
	tenantID := "tenant-1"
	now := time.Unix(100, 0)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status, kubernetes_identity_uid, created_at, updated_at")).
		WithArgs(clusterID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "cluster_id", "tenant_id", "slug", "name", "environment", "region", "credential_ref", "lifecycle_status", "kubernetes_identity_uid", "created_at", "updated_at"}).
			AddRow(int64(1), clusterID, tenantID, "orb", "Orb", "dev", "local", "", "ready", "cluster-uid", now, now))
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_runs")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_actions")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	h := &Handler{
		runDAO: &store.AIRunDAO{}, actionDAO: &store.AIActionDAO{},
		actionPreflight: NewActionPreflightService(fakeActionTargetResolver{identity: KubeObjectIdentity{
			UID: "uid-1", ResourceVersion: "42", Namespace: "prod", Name: "orders",
		}}),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/actions", bytes.NewBufferString(`{"idempotency_key":"retry-1","cluster_id":"`+clusterID+`","resource_type":"deployment","namespace":"prod","target_name":"orders","operation":"scale","params":{"replicas":2}}`))
	req = withAuthorizationContext(req, AuthorizationContext{UserID: "user-1", SessionID: "session-1", TenantID: tenantID})
	rec := httptest.NewRecorder()
	h.ActionProposalPublicHandler(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("proposal status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var got map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["status"] != "proposed" || got["run_status"] != "awaiting_approval" || got["execution_status"] != "proposed" {
		t.Fatalf("unexpected proposal projection: %#v", got)
	}
	if got["action_hash"] == "" || got["target_uid"] != "uid-1" {
		t.Fatalf("proposal did not expose canonical identity: %#v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestActionProposalPublicHandlerRejectsUnavailableDependencies(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/actions", bytes.NewBufferString(`{}`))
	req = withAuthorizationContext(req, AuthorizationContext{UserID: "user-1", TenantID: "tenant-1"})
	rec := httptest.NewRecorder()
	h.ActionProposalPublicHandler(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable handler status = %d, want 503", rec.Code)
	}
}
