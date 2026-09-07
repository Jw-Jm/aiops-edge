package api

import (
	"strings"
	"testing"
)

func TestParseClusterRegistrationRequestAcceptsCredentialReferenceOnly(t *testing.T) {
	req, err := parseClusterRegistrationRequest(strings.NewReader(`{
		"slug":"kind-aiops-kind-02",
		"name":"kind-aiops-kind-02",
		"environment":"local",
		"region":"local",
		"credential_ref":"k8s-secret://observability/aiops-managed-aiops-kind-02-kubeconfig",
		"type":"kubernetes",
		"capabilities":"nodes,namespaces,events",
		"labels":"managed=true"
	}`), "tenant-a")
	if err != nil {
		t.Fatalf("parseClusterRegistrationRequest() error = %v", err)
	}
	if req.TenantID != "tenant-a" || req.CredentialRef == "" || req.Slug != "kind-aiops-kind-02" {
		t.Fatalf("registration request = %+v, want canonical tenant and credential fields", req)
	}
}

func TestParseClusterRegistrationRequestRejectsRawKubeconfig(t *testing.T) {
	_, err := parseClusterRegistrationRequest(strings.NewReader(`{
		"name":"unsafe",
		"credential_ref":"k8s-secret://observability/unsafe",
		"kubeconfig":"apiVersion: v1\nkind: Config"
	}`), "tenant-a")
	if err == nil || !strings.Contains(err.Error(), "kubeconfig") {
		t.Fatalf("raw kubeconfig must be rejected, got %v", err)
	}
}

func TestClusterRegistrationRequestRequiresCredentialReference(t *testing.T) {
	_, err := parseClusterRegistrationRequest(strings.NewReader(`{"name":"missing-credential"}`), "tenant-a")
	if err == nil || !strings.Contains(err.Error(), "credential_ref") {
		t.Fatalf("missing credential_ref must be rejected, got %v", err)
	}
}
