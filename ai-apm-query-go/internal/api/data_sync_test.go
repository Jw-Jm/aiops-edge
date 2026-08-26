package api

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestLogShipperPayloadUsesCanonicalScopeAndServiceName(t *testing.T) {
	payload := buildLogShipperPayload("2026-08-26T05:00:00Z", "F01-crashloop-started", "aiops-test", "f01-crashloop", "Running", "tenant-1", "cluster-1")
	want := map[string]string{
		"_time":        "2026-08-26T05:00:00Z",
		"_msg":         "F01-crashloop-started",
		"service":      "aiops-test/f01-crashloop",
		"service_name": "aiops-test/f01-crashloop",
		"namespace":    "aiops-test",
		"pod":          "f01-crashloop",
		"phase":        "Running",
		"tenant_id":    "tenant-1",
		"cluster_id":   "cluster-1",
	}
	if !reflect.DeepEqual(payload, want) {
		t.Fatalf("payload = %#v, want %#v", payload, want)
	}
}

func TestLogShipperNamespaceListIncludesDiscoveredNamespaces(t *testing.T) {
	body, err := json.Marshal(map[string]any{
		"items": []map[string]any{
			{"metadata": map[string]string{"name": "observability"}},
			{"metadata": map[string]string{"name": "aiops-test"}},
			{"metadata": map[string]string{"name": "default"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseLogShipperNamespaces(body)
	if err != nil {
		t.Fatalf("parseLogShipperNamespaces() error = %v", err)
	}
	want := []string{"aiops-test", "default", "observability"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("namespaces = %#v, want %#v", got, want)
	}
}

func TestConfiguredLogShipperNamespacesOverridesDiscovery(t *testing.T) {
	got := configuredLogShipperNamespaces("observability, aiops-test, observability")
	want := []string{"aiops-test", "observability"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("configured namespaces = %#v, want %#v", got, want)
	}
}
