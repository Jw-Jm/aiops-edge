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
		_, _ = w.Write([]byte(`{"_time":"2026-08-20T10:00:00Z","service_name":"checkout","level":"error","_msg":"boom"}` + "\n"))
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
