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
		ClusterID: "3f3c3b3a-0000-4000-8000-000000000001",
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

func TestActionPreflightRejectsRestartOperation(t *testing.T) {
	service := NewActionPreflightService(fakeActionTargetResolver{identity: KubeObjectIdentity{
		UID: "uid-1", ResourceVersion: "42", Namespace: "prod", Name: "orders",
	}})
	_, err := service.Resolve(context.Background(), PreflightInput{
		ClusterID: "3f3c3b3a-0000-4000-8000-000000000001",
		ResourceType: "deployment", Namespace: "prod", TargetName: "orders",
		Operation: "restart", Params: json.RawMessage(`{}`),
	})
	if err == nil {
		t.Fatal("restart must not be executable in Action V2")
	}
}

type fakeActionTargetResolver struct {
	identity KubeObjectIdentity
	err      error
}

func (f fakeActionTargetResolver) ResolveDeployment(ctx context.Context, clusterID, namespace, name string) (KubeObjectIdentity, error) {
	return f.identity, f.err
}
