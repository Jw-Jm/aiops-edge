package query

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// mockVM 返回一个模拟 VictoriaMetrics /api/v1/query_range 的 server。
func mockVM(t *testing.T, dispatch map[string]string) *VictoriaMetricsReader {
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
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[]}}`))
	}))
	t.Cleanup(srv.Close)
	return NewVictoriaMetricsReader(srv.URL, &http.Client{Timeout: 5 * time.Second})
}

func TestVMREDQuerySuccess(t *testing.T) {
	// VM query_range 返回 2 个样本。
	rows := `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"service_name":"checkout","tenant_id":"t1","cluster_id":"c1"},"values":[["1710000000","10"],["1710000060","12"]]}]}}`
	r := mockVM(t, map[string]string{"rate(": rows})
	pts, err := r.ServiceRED(context.Background(), VMQuery{
		TenantID: "t1", ClusterID: "c1", Service: "checkout", Minutes: 60,
	})
	if err != nil {
		t.Fatalf("ServiceRED: %v", err)
	}
	if len(pts) != 2 {
		t.Fatalf("expected 2 points, got %d", len(pts))
	}
}

func TestVMREDQueryCarriesErrorAndDurationSignals(t *testing.T) {
	rows := `{"status":"success","data":{"resultType":"matrix","result":[` +
		`{"metric":{"red_kind":"calls"},"values":[[1710000000,"10"]]},` +
		`{"metric":{"red_kind":"errors"},"values":[[1710000000,"2"]]},` +
		`{"metric":{"red_kind":"duration_sum"},"values":[[1710000000,"5"]]},` +
		`{"metric":{"red_kind":"duration_count"},"values":[[1710000000,"10"]]}` +
		`]}}`
	r := mockVM(t, map[string]string{"red_kind": rows})
	pts, err := r.ServiceRED(context.Background(), VMQuery{
		TenantID: "t1", ClusterID: "c1", Service: "checkout", Minutes: 60,
	})
	if err != nil {
		t.Fatalf("ServiceRED: %v", err)
	}
	if len(pts) != 1 {
		t.Fatalf("expected one merged RED point, got %d", len(pts))
	}
	if pts[0].CallCount != 10 || pts[0].ErrorCount != 2 || pts[0].AvgMS != 500 {
		t.Fatalf("point = %+v, want calls=10 errors=2 avg_ms=500", pts[0])
	}
}

func TestVMEmptyIsNoData(t *testing.T) {
	r := mockVM(t, map[string]string{}) // empty result
	_, err := r.ServiceRED(context.Background(), VMQuery{
		TenantID: "t1", ClusterID: "c1", Service: "checkout", Minutes: 60,
	})
	var qe *QueryError
	if err == nil || !errors.As(err, &qe) || qe.Code != NoDataCode {
		t.Fatalf("expected no_data, got %v", err)
	}
}

func TestVMUnavailableNoFallback(t *testing.T) {
	// VM 返回 500 → unavailable（绝不 fallback ClickHouse）。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("vm down"))
	}))
	defer srv.Close()
	r := NewVictoriaMetricsReader(srv.URL, &http.Client{Timeout: 5 * time.Second})
	_, err := r.ServiceRED(context.Background(), VMQuery{
		TenantID: "t1", ClusterID: "c1", Service: "checkout", Minutes: 60,
	})
	var qe *QueryError
	if err == nil || !errors.As(err, &qe) || qe.Code != UnavailableCode {
		t.Fatalf("expected unavailable, got %v", err)
	}
}

func TestVMSQLOwnershipLabelsInjected(t *testing.T) {
	var gotQ string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQ = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[]}}`))
	}))
	defer srv.Close()
	r := NewVictoriaMetricsReader(srv.URL, &http.Client{Timeout: 5 * time.Second})
	r.ServiceRED(context.Background(), VMQuery{
		TenantID:  "3f3c3b3a-0000-4000-8000-000000000001",
		ClusterID: "3f3c3b3a-0000-4000-8000-000000000002",
		Service:   "checkout", Minutes: 60,
	})
	for _, want := range []string{
		"tenant_id=\"3f3c3b3a-0000-4000-8000-000000000001\"",
		"cluster_id=\"3f3c3b3a-0000-4000-8000-000000000002\"",
		"service_name=\"checkout\"",
	} {
		if !strings.Contains(gotQ, want) {
			t.Errorf("VM PromQL missing label %q; got: %s", want, gotQ)
		}
	}
}
