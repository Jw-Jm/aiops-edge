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

func mockVLogs(t *testing.T, dispatch map[string]string) *VLogsReader {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		for needle, out := range dispatch {
			if strings.Contains(r.URL.String(), needle) {
				_, _ = w.Write([]byte(out))
				return
			}
		}
		_, _ = w.Write([]byte(""))
	}))
	t.Cleanup(srv.Close)
	return NewVLogsReader(srv.URL, &http.Client{Timeout: 5 * time.Second})
}

func TestVLogsSearchSuccess(t *testing.T) {
	// VictoriaLogs 返回 JSON Lines（每行一个 JSON 日志）。
	out := "" +
		`{"_time":"2026-08-20T10:00:00Z","service_name":"checkout","level":"info","_msg":"hello"}` + "\n" +
		`{"_time":"2026-08-20T10:00:01Z","service_name":"checkout","level":"error","_msg":"boom"}` + "\n"
	r := mockVLogs(t, map[string]string{"service_name": out})
	recs, err := r.Search(context.Background(), LogQuery{
		TenantID: "t1", ClusterID: "c1", Service: "checkout", Minutes: 60,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs))
	}
	if recs[0].ServiceName != "checkout" || recs[0].Body != "hello" {
		t.Fatalf("recs[0] = %+v", recs[0])
	}
	if recs[1].Severity != "error" {
		t.Fatalf("recs[1].Severity = %q, want error", recs[1].Severity)
	}
}

func TestVLogsSearchNormalizesUnstructuredSeverity(t *testing.T) {
	out := "" +
		`{"_time":"2026-08-20T10:00:00Z","service_name":"checkout","_msg":"request failed with Failure"}` + "\n" +
		`{"_time":"2026-08-20T10:00:01Z","service_name":"checkout","_msg":"mysql: [Warning] insecure password"}` + "\n" +
		`{"_time":"2026-08-20T10:00:02Z","service_name":"checkout","_msg":"worker started"}` + "\n" +
		`{"_time":"2026-08-20T10:00:03Z","service_name":"checkout","_msg":"Post-timeout activity"}` + "\n"
	r := mockVLogs(t, map[string]string{"service_name": out})
	recs, err := r.Search(context.Background(), LogQuery{TenantID: "t1", Service: "checkout", Minutes: 60})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	want := []string{"error", "warning", "info", ""}
	for i, got := range recs {
		if got.Severity != want[i] {
			t.Fatalf("recs[%d].Severity = %q, want %q", i, got.Severity, want[i])
		}
	}
}

func TestVLogsSearchAppliesResponseLimit(t *testing.T) {
	var gotLimit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLimit = r.URL.Query().Get("limit")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"_time":"2026-08-20T10:00:00Z","service_name":"checkout","level":"info","_msg":"hello"}` + "\n"))
	}))
	defer srv.Close()
	r := NewVLogsReader(srv.URL, &http.Client{Timeout: 5 * time.Second})
	_, err := r.Search(context.Background(), LogQuery{TenantID: "t1", Limit: 7, Minutes: 60})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotLimit != "7" {
		t.Fatalf("VictoriaLogs limit = %q, want 7", gotLimit)
	}
}

func TestVLogsEmptyIsNoData(t *testing.T) {
	r := mockVLogs(t, map[string]string{})
	_, err := r.Search(context.Background(), LogQuery{TenantID: "t1", ClusterID: "c1", Minutes: 60})
	var qe *QueryError
	if err == nil || !errors.As(err, &qe) || qe.Code != NoDataCode {
		t.Fatalf("expected no_data, got %v", err)
	}
}

func TestVLogsUnavailableNoFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	r := NewVLogsReader(srv.URL, &http.Client{Timeout: 5 * time.Second})
	_, err := r.Search(context.Background(), LogQuery{TenantID: "t1", ClusterID: "c1", Minutes: 60})
	var qe *QueryError
	if err == nil || !errors.As(err, &qe) || qe.Code != UnavailableCode {
		t.Fatalf("expected unavailable, got %v", err)
	}
}

func TestVLogsScopeLabelsInjected(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query") // 自动 URL-decode
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(""))
	}))
	defer srv.Close()
	r := NewVLogsReader(srv.URL, &http.Client{Timeout: 5 * time.Second})
	r.Search(context.Background(), LogQuery{
		TenantID:  "3f3c3b3a-0000-4000-8000-000000000001",
		ClusterID: "3f3c3b3a-0000-4000-8000-000000000002",
		Service:   "checkout", Minutes: 60,
	})
	for _, want := range []string{
		`tenant_id:"3f3c3b3a-0000-4000-8000-000000000001"`,
		`cluster_id:"3f3c3b3a-0000-4000-8000-000000000002"`,
		`service_name:"checkout"`,
	} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("VLogs query missing %q; got: %s", want, gotQuery)
		}
	}
}

func TestVLogsQueryHonorsLevelAndHealthFilter(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"_time":"2026-08-20T10:00:00Z","service_name":"checkout","level":"error","_msg":"boom"}` + "\n" +
			`{"_time":"2026-08-20T10:00:01Z","service_name":"checkout","_msg":"unstructured log"}` + "\n"))
	}))
	defer srv.Close()

	r := NewVLogsReader(srv.URL, &http.Client{Timeout: 5 * time.Second})
	rows, err := r.Search(context.Background(), LogQuery{
		TenantID: "tenant-a", ClusterID: "cluster-a", Service: "checkout", Minutes: 60,
		Level: "error", ExcludeHealth: true,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(rows) != 1 || rows[0].Severity != "error" {
		t.Fatalf("rows = %+v, want one error log", rows)
	}
	for _, want := range []string{
		`level:equals_common_case("error")`,
		`severity:equals_common_case("error")`,
		`NOT *health*`,
		`NOT *ready*`,
		`NOT *livez*`,
		`NOT *v1/query*`,
		`NOT *metrics*`,
	} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("LogsQL missing %q; got: %s", want, gotQuery)
		}
	}
}

func TestVLogsStatsReadersUseRealVictoriaLogsAggregates(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		queries = append(queries, q)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(q, "stats by (service_name)"):
			_, _ = w.Write([]byte(`{"service_name":"checkout","logs":"9"}` + "\n"))
		case strings.Contains(q, "stats by (_time:5m)"):
			_, _ = w.Write([]byte(`{"_time":"2026-08-20T10:00:00Z","logs":"5"}` + "\n"))
		default:
			_, _ = w.Write([]byte(`{"total":"10","errors":"2","warnings":"1","debugs":"3","infos":"4"}` + "\n"))
		}
	}))
	defer srv.Close()
	r := NewVLogsReader(srv.URL, &http.Client{Timeout: 5 * time.Second})
	q := LogQuery{TenantID: "tenant-a", ClusterID: "cluster-a", Minutes: 60}
	services, err := r.AggregateServices(context.Background(), q, 10)
	if err != nil || len(services) != 1 || services[0].Service != "checkout" || services[0].Count != 9 {
		t.Fatalf("services = %+v, err=%v", services, err)
	}
	trend, err := r.AggregateTrend(context.Background(), q, 5)
	if err != nil || len(trend) != 1 || trend[0].Count != 5 {
		t.Fatalf("trend = %+v, err=%v", trend, err)
	}
	levels, err := r.AggregateLevels(context.Background(), q)
	if err != nil || len(levels) != 4 || levels[0].Level != "error" || levels[0].Count != 2 {
		t.Fatalf("levels = %+v, err=%v", levels, err)
	}
	for _, want := range []string{
		`tenant_id:"tenant-a"`,
		`cluster_id:"cluster-a"`,
		`stats by (service_name)`,
		`stats by (_time:5m)`,
		`count() if (error OR err`,
	} {
		found := false
		for _, query := range queries {
			if strings.Contains(query, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("stats query missing %q; got %v", want, queries)
		}
	}
}
