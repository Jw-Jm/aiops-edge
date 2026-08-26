package api

import "testing"

func TestSystemComponentResultMarksOptionalComponentAsNotConfigured(t *testing.T) {
	component := systemComponent{
		name:       "minio",
		typ:        "middleware",
		kind:       "http",
		addr:       "http://minio.observability.svc.cluster.local:9000/minio/health/live",
		configured: false,
	}

	result := systemComponentResult(component, func(string, string) bool {
		t.Fatal("an unconfigured optional component must not be probed")
		return false
	})

	if got, want := result["status"], "not_configured"; got != want {
		t.Fatalf("status = %v, want %v", got, want)
	}
	if got, want := result["detail"], "optional component is not configured"; got != want {
		t.Fatalf("detail = %v, want %v", got, want)
	}
}
