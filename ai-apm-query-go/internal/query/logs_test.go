package query

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func chSrv(t *testing.T, respBody string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(respBody))
	}))
}

func TestLogRepoSearchRawLogsLegacy(t *testing.T) {
	srv := chSrv(t, "2026-08-20 10:00:00\tcheckout\tinfo\torder placed\ttrace-1\n"+
		"2026-08-20 10:01:00\tcheckout\terror\tpayment failed\ttrace-2\n")
	defer srv.Close()

	repo := &LogRepository{
		ch:     NewClickHouseRepo(srv.URL, nil),
		router: NewSourceRouter(ModeLegacy), // legacy 模式 → ClickHouse raw-log transition path
	}
	q := LogQuery{
		TenantID:  "3f3c3b3a-0000-4000-8000-000000000001",
		ClusterID: "3f3c3b3a-0000-4000-8000-000000000002",
		Service:   "checkout",
		Query:     "payment",
		Minutes:   60,
	}
	rows, err := repo.SearchRawLogs(context.Background(), q)
	if err != nil {
		t.Fatalf("SearchRawLogs: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(rows))
	}
	if rows[0].ServiceName != "checkout" || rows[0].Body != "order placed" {
		t.Fatalf("row0 = %+v", rows[0])
	}
	if rows[1].Severity != "error" || rows[1].TraceID != "trace-2" {
		t.Fatalf("row1 = %+v", rows[1])
	}
}

func TestLogRepoTraceLogs(t *testing.T) {
	srv := chSrv(t, "2026-08-20 10:00:00\tcheckout\tinfo\torder placed\ttrace-1\n")
	defer srv.Close()
	repo := &LogRepository{ch: NewClickHouseRepo(srv.URL, nil)}
	rows, err := repo.TraceLogs(context.Background(), "3f3c3b3a-0000-4000-8000-000000000001", "", "trace-1")
	if err != nil {
		t.Fatalf("TraceLogs: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 log, got %d", len(rows))
	}
	if rows[0].ServiceName != "checkout" || rows[0].TraceID != "trace-1" {
		t.Fatalf("row0 = %+v", rows[0])
	}
}

func TestLogRepoLogRuleValue(t *testing.T) {
	// LogRuleValue 用 QueryJSON（GET + JSONEachRow），按关键字分发。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("query")
		switch {
		case strings.Contains(q, "countIf(severity"):
			_, _ = w.Write([]byte(`{"x":5.5}` + "\n"))
		case strings.Contains(q, "body LIKE"):
			_, _ = w.Write([]byte(`{"x":7}` + "\n"))
		default:
			_, _ = w.Write([]byte(""))
		}
	}))
	defer srv.Close()

	repo := &LogRepository{ch: NewClickHouseRepo(srv.URL, nil)}
	ctx := context.Background()
	if v, err := repo.LogRuleValue(ctx, "checkout", "log_error_rate", ""); err != nil || v != 5.5 {
		t.Fatalf("log_error_rate = %v, %v; want 5.5", v, err)
	}
	if v, err := repo.LogRuleValue(ctx, "checkout", "log_keyword", "order"); err != nil || v != 7 {
		t.Fatalf("log_keyword = %v, %v; want 7", v, err)
	}
}

func TestLogRepoSearchRawLogsSQLOwnership(t *testing.T) {
	var gotQ string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		gotQ = string(buf)
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(""))
	}))
	defer srv.Close()

	repo := &LogRepository{
		ch:     NewClickHouseRepo(srv.URL, nil),
		router: NewSourceRouter(ModeLegacy),
	}
	q := LogQuery{
		TenantID:  "3f3c3b3a-0000-4000-8000-000000000001",
		ClusterID: "3f3c3b3a-0000-4000-8000-000000000002",
		Service:   "checkout",
		Query:     "payment",
		Minutes:   60,
	}
	repo.SearchRawLogs(context.Background(), q)
	for _, want := range []string{
		"tenant_id='3f3c3b3a-0000-4000-8000-000000000001'",
		"cluster_id='3f3c3b3a-0000-4000-8000-000000000002'",
		"FROM observability.log_records",
		"ORDER BY timestamp DESC",
	} {
		if !strings.Contains(gotQ, want) {
			t.Errorf("repo SQL missing %q; got: %s", want, gotQ)
		}
	}
}

func TestLogRepoAggregateTrend(t *testing.T) {
	srv := chSrv(t, "2026-08-20 10:00:00\t5\n2026-08-20 10:05:00\t12\n")
	defer srv.Close()

	repo := &LogRepository{ch: NewClickHouseRepo(srv.URL, nil)}
	q := LogQuery{TenantID: "3f3c3b3a-0000-4000-8000-000000000001", Minutes: 60}
	trend, err := repo.AggregateTrend(context.Background(), q, 5)
	if err != nil {
		t.Fatalf("AggregateTrend: %v", err)
	}
	if len(trend) != 2 {
		t.Fatalf("expected 2 buckets, got %d", len(trend))
	}
	if trend[1].Count != 12 {
		t.Fatalf("trend[1] = %+v", trend[1])
	}
}

// Raw Logs new mode 必须走 VLogs，禁止 fallback 到 ClickHouse。
func TestLogRepoSearchRawLogsNewModeNoFallback(t *testing.T) {
	// new mode + 无 VLogs reader：必须返回 unavailable，绝不 fallback ClickHouse。
	srv := chSrv(t, "2026-08-20 10:00:00\tcheckout\tinfo\tx\t1")
	defer srv.Close()
	repo := &LogRepository{
		ch:     NewClickHouseRepo(srv.URL, nil),
		router: NewSourceRouter(ModeNew),
		vlogs:  nil, // 未配置 VLogs reader
	}
	q := LogQuery{TenantID: "3f3c3b3a-0000-4000-8000-000000000001", Minutes: 60}
	_, err := repo.SearchRawLogs(context.Background(), q)
	if err == nil {
		t.Fatal("new mode without VLogs reader must error, not fallback")
	}
	var qe *QueryError
	if !asQueryError(err, &qe) || qe.Code != UnavailableCode {
		t.Fatalf("expected unavailable (no VLogs reader), got %v", err)
	}
}

// Raw Logs new mode + VLogs reader 存在但 VLogs 不可用 → unavailable；绝不 fallback ClickHouse。
func TestLogRepoSearchRawLogsNewModeVLogsDownNoFallback(t *testing.T) {
	// CH 返回有效数据，但 new mode 不应查询它（VLogs down → unavailable，不 fallback CH）。
	chServer := chSrv(t, "2026-08-20 10:00:00\tcheckout\tinfo\tx\t1")
	defer chServer.Close()

	vlogsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer vlogsSrv.Close()

	repo := &LogRepository{
		ch:     NewClickHouseRepo(chServer.URL, nil),
		router: NewSourceRouter(ModeNew),
		vlogs:  NewVLogsReader(vlogsSrv.URL, &http.Client{Timeout: 5 * time.Second}),
	}
	q := LogQuery{TenantID: "3f3c3b3a-0000-4000-8000-000000000001", Minutes: 60}
	_, err := repo.SearchRawLogs(context.Background(), q)
	var qe *QueryError
	if err == nil || !asQueryError(err, &qe) || qe.Code != UnavailableCode {
		t.Fatalf("expected unavailable (VLogs down, no CH fallback), got %v", err)
	}
}

func TestVLogsTenantClusterIsolation(t *testing.T) {
	// 同名资源（service=checkout）在 cluster-A/B 必须隔离：LogsQL 只注入本次 tenant+cluster。
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query().Get("query"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(""))
	}))
	defer srv.Close()
	r := NewVLogsReader(srv.URL, &http.Client{Timeout: 5 * time.Second})

	tenantA := "3f3c3b3a-0000-4000-8000-000000000001"
	clusterA := "3f3c3b3a-0000-4000-8000-000000000002"
	clusterB := "3f3c3b3a-0000-4000-8000-000000000003"

	r.Search(context.Background(), LogQuery{TenantID: tenantA, ClusterID: clusterA, Service: "checkout", Minutes: 60})
	r.Search(context.Background(), LogQuery{TenantID: tenantA, ClusterID: clusterB, Service: "checkout", Minutes: 60})

	if len(queries) != 2 {
		t.Fatalf("expected 2 VLogs queries, got %d", len(queries))
	}
	if !strings.Contains(queries[0], `cluster_id:"`+clusterA+`"`) || strings.Contains(queries[0], clusterB) {
		t.Fatalf("query[0] not isolated to cluster-A: %s", queries[0])
	}
	if !strings.Contains(queries[1], `cluster_id:"`+clusterB+`"`) || strings.Contains(queries[1], clusterA) {
		t.Fatalf("query[1] not isolated to cluster-B: %s", queries[1])
	}
}

// P6.4.4-4: new mode 下 legacy ClickHouse 被 poison，生产查询仍经 VictoriaLogs 成功
// → 证明 old reader 实际 inactive。
func TestLogsNewModeLegacyPoisonedStillServes(t *testing.T) {
	vlogsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"_time":"2026-08-20T10:00:00Z","service_name":"checkout","level":"info","_msg":"hello"}` + "\n"))
	}))
	defer vlogsSrv.Close()
	// ClickHouse（legacy）poisoned。
	repo := &LogRepository{
		ch:     NewClickHouseRepo("http://127.0.0.1:1", nil),
		router: NewSourceRouter(ModeNew),
		vlogs:  NewVLogsReader(vlogsSrv.URL, &http.Client{Timeout: 5 * time.Second}),
	}
	recs, err := repo.SearchRawLogs(context.Background(), LogQuery{
		TenantID: "3f3c3b3a-0000-4000-8000-000000000001", ClusterID: "3f3c3b3a-0000-4000-8000-000000000002",
		Service: "checkout", Minutes: 60,
	})
	if err != nil {
		t.Fatalf("new mode with poisoned legacy must still serve via VLogs: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 record via VLogs, got %d", len(recs))
	}
}

func asQueryError(err error, target **QueryError) bool {
	if e, ok := err.(*QueryError); ok {
		*target = e
		return true
	}
	return false
}
