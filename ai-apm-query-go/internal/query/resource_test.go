package query

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mockResourceCH(t *testing.T, dispatch map[string]string) *ResourceRepository {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("query")
		for needle, out := range dispatch {
			if strings.Contains(q, needle) {
				_, _ = w.Write([]byte(out))
				return
			}
		}
		_, _ = w.Write([]byte(""))
	}))
	t.Cleanup(srv.Close)
	return NewResourceRepository(NewClickHouseRepo(srv.URL, nil))
}

func TestResourceRepoActiveServices(t *testing.T) {
	r := mockResourceCH(t, map[string]string{
		"DISTINCT service_name": "{\"service_name\":\"frontend\"}\n{\"service_name\":\"backend\"}\n",
	})
	svcs, err := r.ActiveServices(context.Background(), ResourceScope{TenantID: "t1"}, false)
	if err != nil {
		t.Fatalf("ActiveServices: %v", err)
	}
	if len(svcs) != 2 || svcs[0] != "frontend" || svcs[1] != "backend" {
		t.Fatalf("ActiveServices = %v", svcs)
	}
}

func TestResourceRepoServiceMetrics(t *testing.T) {
	r := mockResourceCH(t, map[string]string{
		"GROUP BY service_name": "{\"service\":\"frontend\",\"calls\":100,\"errs\":2,\"avg_ms\":15.5}\n" +
			"{\"service\":\"backend\",\"calls\":50,\"errs\":1,\"avg_ms\":20.0}\n",
	})
	metrics, err := r.ServiceMetrics(context.Background(), ResourceScope{TenantID: "t1"})
	if err != nil {
		t.Fatalf("ServiceMetrics: %v", err)
	}
	if len(metrics) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(metrics))
	}
	if metrics[0].Service != "frontend" || metrics[0].Calls != 100 || metrics[0].Errors != 2 || metrics[0].AvgMS != 15.5 {
		t.Fatalf("metrics[0] = %+v", metrics[0])
	}
}

func TestResourceRepoSQLOwnershipScope(t *testing.T) {
	var gotQ string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQ = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(""))
	}))
	defer srv.Close()
	r := NewResourceRepository(NewClickHouseRepo(srv.URL, nil))
	scope := ResourceScope{
		TenantID:  "3f3c3b3a-0000-4000-8000-000000000001",
		ClusterID: "3f3c3b3a-0000-4000-8000-000000000002",
		Services:  []string{"frontend"},
	}
	r.ActiveServices(context.Background(), scope, false)
	for _, want := range []string{
		"tenant_id='3f3c3b3a-0000-4000-8000-000000000001'",
		"cluster_id='3f3c3b3a-0000-4000-8000-000000000002'",
		"service_name IN ('frontend')",
		"date >= today()-1",
	} {
		if !strings.Contains(gotQ, want) {
			t.Errorf("repo SQL missing %q; got: %s", want, gotQ)
		}
	}
}

func TestResourceRepoTimeWindowUsesMinutesFromScope(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		queries = append(queries, q)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(q, "DISTINCT service_name") {
			_, _ = w.Write([]byte(`{"service_name":"frontend"}` + "\n"))
			return
		}
		_, _ = w.Write([]byte(`{"service":"frontend","calls":3,"errs":1,"avg_ms":12.5}` + "\n"))
	}))
	defer srv.Close()

	r := NewResourceRepository(NewClickHouseRepo(srv.URL, nil))
	scope := ResourceScope{TenantID: "t1", Minutes: 60}
	if _, err := r.ActiveServices(context.Background(), scope, false); err != nil {
		t.Fatalf("ActiveServices: %v", err)
	}
	if _, err := r.ServiceMetrics(context.Background(), scope); err != nil {
		t.Fatalf("ServiceMetrics: %v", err)
	}
	if len(queries) != 2 {
		t.Fatalf("expected two queries, got %d", len(queries))
	}
	for _, q := range queries {
		if !strings.Contains(q, "start_time >= now() - INTERVAL 60 MINUTE") {
			t.Errorf("resource query ignored scope minutes: %s", q)
		}
	}
}
