package api

import (
	"context"
	"encoding/json"
	"testing"
)

func TestActionPreflightResolvesImmutableDeploymentIdentity(t *testing.T) {
	resolver := fakeActionTargetResolver{identity: KubeObjectIdentity{
		UID: "uid-1", ResourceVersion: "42", Namespace: "prod", Name: "orders",
	}}
	service := NewActionPreflightService(resolver)
	got, err := service.Resolve(context.Background(), PreflightInput{
		ClusterID:    "3f3c3b3a-0000-4000-8000-000000000001",
		ResourceType: "deployment", Namespace: "prod", TargetName: "orders",
		Operation: "scale", Params: json.RawMessage(`{"replicas":2}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.HashSchemaVersion != 2 || got.ActionHash == "" {
		t.Fatalf("preflight result = %#v", got)
	}
	if got.TargetUID != "uid-1" || got.ResourceVersion != "42" {
		t.Fatalf("target identity = %#v", got)
	}
}

func TestActionPreflightAcceptsAllCanonicalOperations(t *testing.T) {
	service := NewActionPreflightService(fakeActionTargetResolver{identity: KubeObjectIdentity{
		UID: "uid-1", ResourceVersion: "42", Namespace: "prod", Name: "orders",
	}})
	tests := []struct {
		resourceType string
		operation    string
		params       string
	}{
		{"deployment", "rollout_restart", `{}`},
		{"statefulset", "rollout_restart", `{}`},
		{"daemonset", "rollout_restart", `{}`},
		{"deployment", "scale", `{"replicas":2}`},
		{"pod", "delete_pod", `{"grace_period_seconds":30}`},
		{"pod", "evict_pod", `{"grace_period_seconds":30}`},
		{"node", "cordon", `{}`},
		{"node", "uncordon", `{}`},
		{"node", "drain", `{"drain_timeout":300}`},
	}
	for _, tt := range tests {
		_, err := service.Resolve(context.Background(), PreflightInput{
			ClusterID:    "3f3c3b3a-0000-4000-8000-000000000001",
			ResourceType: tt.resourceType, Namespace: "prod", TargetName: "orders",
			Operation: tt.operation, Params: json.RawMessage(tt.params),
		})
		if err != nil {
			t.Fatalf("%s/%s rejected: %v", tt.resourceType, tt.operation, err)
		}
	}
	if _, err := service.Resolve(context.Background(), PreflightInput{
		ClusterID: "3f3c3b3a-0000-4000-8000-000000000001", ResourceType: "deployment",
		Namespace: "prod", TargetName: "orders", Operation: "restart", Params: json.RawMessage(`{}`),
	}); err == nil {
		t.Fatal("unknown operation must be rejected")
	}
}

type fakeActionTargetResolver struct {
	identity KubeObjectIdentity
	err      error
}

func (f fakeActionTargetResolver) ResolveTarget(ctx context.Context, clusterID, resourceType, namespace, name string) (KubeObjectIdentity, error) {
	return f.identity, f.err
}
