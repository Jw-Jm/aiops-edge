package telemetrylabels

import (
	"errors"
	"testing"
)

const (
	validTenant  = "11111111-1111-4111-8111-111111111111"
	validCluster = "22222222-2222-4222-8222-222222222222"
	validRes     = "33333333-3333-4333-8333-333333333333"
)

func TestValidateScopeLabelsRequiresCanonicalUUIDs(t *testing.T) {
	cases := []struct {
		name   string
		labels map[string]string
		scope  string
		wantOK bool
	}{
		{"valid cluster scope", map[string]string{"tenant_id": validTenant, "cluster_id": validCluster}, "cluster", true},
		{"valid resource scope with resource_id", map[string]string{"tenant_id": validTenant, "cluster_id": validCluster, "resource_id": validRes}, "resource", true},
		{"cluster_id slug orbstack", map[string]string{"tenant_id": validTenant, "cluster_id": "orbstack"}, "cluster", false},
		{"cluster_id numeric", map[string]string{"tenant_id": validTenant, "cluster_id": "1"}, "cluster", false},
		{"cluster_id default", map[string]string{"tenant_id": validTenant, "cluster_id": "default"}, "cluster", false},
		{"cluster_id empty", map[string]string{"tenant_id": validTenant, "cluster_id": ""}, "cluster", false},
		{"tenant_id empty", map[string]string{"tenant_id": "", "cluster_id": validCluster}, "cluster", false},
		{"tenant_id non-uuid", map[string]string{"tenant_id": "prod-tenant", "cluster_id": validCluster}, "cluster", false},
		{"resource scope missing resource_id", map[string]string{"tenant_id": validTenant, "cluster_id": validCluster}, "resource", false},
		{"cluster scope no resource_id ok", map[string]string{"tenant_id": validTenant, "cluster_id": validCluster}, "cluster", true},
		{"aggregate scope no resource_id ok", map[string]string{"tenant_id": validTenant, "cluster_id": validCluster}, "aggregate", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateScopeLabels(tc.labels, tc.scope)
			if tc.wantOK && err != nil {
				t.Fatalf("ValidateScopeLabels(%v, %s) error = %v, want ok", tc.labels, tc.scope, err)
			}
			if !tc.wantOK && err == nil {
				t.Fatalf("ValidateScopeLabels(%v, %s) = nil, want error", tc.labels, tc.scope)
			}
		})
	}
}

func TestValidateScopeLabelsRejectsNonCanonicalClusterID(t *testing.T) {
	for _, bad := range []string{"orbstack", "prod-cluster", "123", "default", ""} {
		err := ValidateScopeLabels(map[string]string{"tenant_id": validTenant, "cluster_id": bad}, "cluster")
		if err == nil {
			t.Fatalf("cluster_id=%q should be rejected", bad)
		}
		if !errors.Is(err, ErrInvalidClusterID) {
			t.Fatalf("cluster_id=%q error = %v, want ErrInvalidClusterID", bad, err)
		}
	}
}

func TestNormalizeScopeLabelsDropsEmpty(t *testing.T) {
	in := map[string]string{"tenant_id": validTenant, "cluster_id": validCluster, "resource_id": "", "service_name": "checkout"}
	out := NormalizeScopeLabels(in)
	if _, ok := out["resource_id"]; ok {
		t.Fatalf("empty resource_id should be dropped, got %v", out)
	}
	if out["tenant_id"] != validTenant || out["cluster_id"] != validCluster || out["service_name"] != "checkout" {
		t.Fatalf("normalize lost labels: %v", out)
	}
}
