package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIssueUsesDedicatedBearerAndRegisteredProfile(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/serviceaccounts/action/token") {
			t.Fatalf("unexpected TokenRequest: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") == "" {
			t.Fatal("missing Kubernetes API authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":{"token":"short-lived","expirationTimestamp":"2030-01-01T00:00:00Z"}}`))
	}))
	defer api.Close()

	s := &server{
		token: "broker-secret",
		profiles: map[string]profile{
			"cluster-ref": {Ref: "cluster-ref", Namespace: "prod", ServiceAcct: "action", Operations: map[string]bool{"rollout_restart": true}},
		},
		apiURL:     api.URL,
		apiToken:   "kube-token",
		httpClient: api.Client(),
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/credentials/token", strings.NewReader(`{"credential_ref":"cluster-ref","cluster_id":"3f3c3b3a-0000-4000-8000-000000000001","namespace":"prod","resource":"deployment:checkout","operation":"rollout_restart","ttl_seconds":60}`))
	req.Header.Set("Authorization", "Bearer broker-secret")
	rec := httptest.NewRecorder()
	s.issue(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "short-lived") {
		t.Fatalf("issue status=%d body=%s", rec.Code, rec.Body.String())
	}

	bad := httptest.NewRequest(http.MethodPost, "/v1/credentials/token", strings.NewReader(`{"credential_ref":"cluster-ref","cluster_id":"3f3c3b3a-0000-4000-8000-000000000001","namespace":"prod","resource":"deployment:checkout","operation":"rollout_restart","ttl_seconds":60}`))
	bad.Header.Set("X-Executor-Token", "broker-secret")
	badRec := httptest.NewRecorder()
	s.issue(badRec, bad)
	if badRec.Code != http.StatusUnauthorized {
		t.Fatalf("legacy executor header must be rejected, got %d", badRec.Code)
	}
}
