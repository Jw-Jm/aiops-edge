package query

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTraceRepoFindTraces(t *testing.T) {
	srv := chSrv(t, "trace-1\t2026-08-20 10:00:00\t2026-08-20 10:00:05\t3\t2\t150.5\n"+
		"trace-2\t2026-08-20 09:00:00\t2026-08-20 09:00:01\t1\t1\t50.0\n")
	defer srv.Close()

	repo := &TraceRepository{ch: NewClickHouseRepo(srv.URL, nil)}
	q := TraceQuery{
		TenantID:  "3f3c3b3a-0000-4000-8000-000000000001",
		ClusterID: "3f3c3b3a-0000-4000-8000-000000000002",
		Limit:     20,
	}
	traces, err := repo.FindTraces(context.Background(), q)
	if err != nil {
		t.Fatalf("FindTraces: %v", err)
	}
	if len(traces) != 2 {
		t.Fatalf("expected 2 traces, got %d", len(traces))
	}
	if traces[0].TraceID != "trace-1" || traces[0].Spans != 3 || traces[0].Services != 2 {
		t.Fatalf("trace0 = %+v", traces[0])
	}
	if traces[1].MaxMS != 50.0 {
		t.Fatalf("trace1 = %+v", traces[1])
	}
}

func TestTraceRepoFindTracesParsesServiceNamesForEvidenceAttribution(t *testing.T) {
	srv := chSrv(t, "trace-1\t2026-08-20 10:00:00\t2026-08-20 10:00:05\t3\t2\t150.5\tai-orchestrator,victoria-metrics\n")
	defer srv.Close()

	repo := &TraceRepository{ch: NewClickHouseRepo(srv.URL, nil)}
	traces, err := repo.FindTraces(context.Background(), TraceQuery{
		TenantID: "tenant-1", ClusterID: "cluster-1", Limit: 20,
	})
	if err != nil {
		t.Fatalf("FindTraces: %v", err)
	}
	if len(traces) != 1 || len(traces[0].ServiceNames) != 2 || traces[0].ServiceNames[0] != "ai-orchestrator" || traces[0].ServiceNames[1] != "victoria-metrics" {
		t.Fatalf("service names = %+v", traces)
	}
}

func TestTraceRepoTraceRuleValue(t *testing.T) {
	// TraceRuleValue 用 QueryJSON（GET + JSONEachRow），按关键字分发。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("query")
		switch {
		case strings.Contains(q, "quantile(0.99)"):
			_, _ = w.Write([]byte(`{"x":88.8}` + "\n"))
		case strings.Contains(q, "countIf(is_error=1)"):
			_, _ = w.Write([]byte(`{"x":3.3}` + "\n"))
		default:
			_, _ = w.Write([]byte(""))
		}
	}))
	defer srv.Close()

	repo := &TraceRepository{ch: NewClickHouseRepo(srv.URL, nil)}
	ctx := context.Background()
	if v, err := repo.TraceRuleValue(ctx, "checkout", "trace_latency"); err != nil || v != 88.8 {
		t.Fatalf("trace_latency = %v, %v; want 88.8", v, err)
	}
	if v, err := repo.TraceRuleValue(ctx, "checkout", "trace_error_rate"); err != nil || v != 3.3 {
		t.Fatalf("trace_error_rate = %v, %v; want 3.3", v, err)
	}
}

func TestTraceRepoTraceService(t *testing.T) {
	srv := chSrv(t, "checkout\n")
	defer srv.Close()
	repo := &TraceRepository{ch: NewClickHouseRepo(srv.URL, nil)}
	svc, err := repo.TraceService(context.Background(), "t1", "", "trace-1")
	if err != nil {
		t.Fatalf("TraceService: %v", err)
	}
	if svc != "checkout" {
		t.Fatalf("TraceService = %q, want checkout", svc)
	}
}

func TestTraceRepoTraceServiceEmpty(t *testing.T) {
	srv := chSrv(t, "")
	defer srv.Close()
	repo := &TraceRepository{ch: NewClickHouseRepo(srv.URL, nil)}
	svc, err := repo.TraceService(context.Background(), "t1", "", "trace-missing")
	if err == nil {
		t.Fatal("expected no_data for missing trace")
	}
	if svc != "" {
		t.Fatalf("TraceService should be empty on error, got %q", svc)
	}
}

func TestTraceRepoFindSpans(t *testing.T) {
	srv := chSrv(t, "sp1\t\tcheckout\tcreateOrder\tserver\t2026-08-20 10:00:00\t150.5\t0\n"+
		"sp2\tsp1\tpayments\tcharge\tclient\t2026-08-20 10:00:01\t200.0\t1\n")
	defer srv.Close()

	repo := &TraceRepository{ch: NewClickHouseRepo(srv.URL, nil)}
	spans, err := repo.FindSpans(context.Background(), "3f3c3b3a-0000-4000-8000-000000000001", "", "trace-1")
	if err != nil {
		t.Fatalf("FindSpans: %v", err)
	}
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}
	if spans[0].SpanID != "sp1" || spans[0].ServiceName != "checkout" || spans[0].IsError != false {
		t.Fatalf("span0 = %+v", spans[0])
	}
	if spans[1].ParentSpanID != "sp1" || spans[1].IsError != true || spans[1].MS != 200.0 {
		t.Fatalf("span1 = %+v", spans[1])
	}
}

func TestTraceRepoFindSpansSQLOwnership(t *testing.T) {
	var gotQ string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		gotQ = string(buf)
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(""))
	}))
	defer srv.Close()

	repo := &TraceRepository{ch: NewClickHouseRepo(srv.URL, nil)}
	repo.FindSpans(context.Background(), "3f3c3b3a-0000-4000-8000-000000000001", "3f3c3b3a-0000-4000-8000-000000000002", "trace-1")
	for _, want := range []string{
		"tenant_id='3f3c3b3a-0000-4000-8000-000000000001'",
		"cluster_id='3f3c3b3a-0000-4000-8000-000000000002'",
		"trace_id='trace-1'",
		"ORDER BY start_time",
	} {
		if !strings.Contains(gotQ, want) {
			t.Errorf("repo SQL missing %q; got: %s", want, gotQ)
		}
	}
}

func TestTraceRepoEscapesTraceDetailIdentifiers(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			queries = append(queries, string(body))
		} else {
			queries = append(queries, r.URL.Query().Get("query"))
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(""))
	}))
	defer srv.Close()

	repo := &TraceRepository{ch: NewClickHouseRepo(srv.URL, nil)}
	tenant := "tenant' OR 1=1 --"
	cluster := "cluster' OR 1=1 --"
	traceID := "trace' OR 1=1 --"
	_, _ = repo.FindSpans(context.Background(), tenant, cluster, traceID)
	_, _ = repo.TraceService(context.Background(), tenant, cluster, traceID)
	if len(queries) != 2 {
		t.Fatalf("expected two trace detail queries, got %d", len(queries))
	}
	for _, q := range queries {
		for _, want := range []string{
			"tenant_id=" + sqlStr(tenant),
			"cluster_id=" + sqlStr(cluster),
			"trace_id=" + sqlStr(traceID),
		} {
			if !strings.Contains(q, want) {
				t.Fatalf("query missing escaped condition %q: %s", want, q)
			}
		}
	}
}

func TestTraceRepoFindTracesSQLOwnership(t *testing.T) {
	var gotQ string
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		gotQ = string(buf)
		queries = append(queries, gotQ)
		w.Header().Set("Content-Type", "text/plain")
		if strings.Contains(gotQ, "trace_summary_index") {
			w.Write([]byte("trace-1\n"))
			return
		}
		w.Write([]byte("trace-1\t2026-08-20 10:00:00\t2026-08-20 10:00:05\t3\t2\t150.5\n"))
	}))
	defer srv.Close()

	repo := &TraceRepository{ch: NewClickHouseRepo(srv.URL, nil)}
	q := TraceQuery{
		TenantID:  "3f3c3b3a-0000-4000-8000-000000000001",
		ClusterID: "3f3c3b3a-0000-4000-8000-000000000002",
		Service:   "checkout",
		Keyword:   "error",
		Hours:     24,
		Limit:     20,
		Offset:    0,
	}
	repo.FindTraces(context.Background(), q)
	for _, want := range []string{
		"tenant_id='3f3c3b3a-0000-4000-8000-000000000001'",
		"cluster_id='3f3c3b3a-0000-4000-8000-000000000002'",
		"has(arrayDistinct(arrayFlatten(groupUniqArrayArray(service_names))), 'checkout')",
		"FROM observability.trace_summary_state FINAL",
		"finalizeAggregation(start_state)",
		"sum(span_count)",
		"arrayDistinct(arrayFlatten(groupUniqArrayArray(service_names)))",
		"GROUP BY trace_id",
		"ORDER BY start DESC",
		"LIMIT 20 OFFSET 0",
	} {
		if !strings.Contains(gotQ, want) {
			t.Errorf("repo SQL missing %q; got: %s", want, gotQ)
		}
	}
	indexSeen := false
	for _, sql := range queries {
		if strings.Contains(sql, "FROM observability.trace_summary_index") && strings.Contains(sql, "optimize_read_in_order=1") {
			indexSeen = true
			break
		}
	}
	if !indexSeen {
		t.Fatalf("trace list must first read the ordered candidate index, got: %v", queries)
	}
	if strings.Contains(gotQ, "FROM observability.trace_spans") {
		t.Fatalf("trace list must not aggregate raw spans, got: %s", gotQ)
	}
}

func TestTraceRepoFindTracesMergesCandidateSummaryRows(t *testing.T) {
	var summarySQL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		sql := string(buf)
		w.Header().Set("Content-Type", "text/plain")
		if strings.Contains(sql, "trace_summary_index") {
			_, _ = w.Write([]byte("trace-cross-midnight\n"))
			return
		}
		summarySQL = sql
		_, _ = w.Write([]byte("trace-cross-midnight\t2026-08-20 23:59:59\t2026-08-21 00:00:01\t3\t2\t150.5\n"))
	}))
	defer srv.Close()

	repo := &TraceRepository{ch: NewClickHouseRepo(srv.URL, nil)}
	traces, err := repo.FindTraces(context.Background(), TraceQuery{
		TenantID: "tenant-1",
		Hours:    24,
		Limit:    20,
	})
	if err != nil {
		t.Fatalf("FindTraces: %v", err)
	}
	if len(traces) != 1 || traces[0].TraceID != "trace-cross-midnight" {
		t.Fatalf("unexpected traces: %+v", traces)
	}
	for _, want := range []string{
		"min(trace_start) AS start",
		"max(trace_end) AS end",
		"sum(span_count) AS spans",
		"length(arrayDistinct(arrayFlatten(groupUniqArrayArray(service_names)))) AS services",
		"GROUP BY trace_id",
	} {
		if !strings.Contains(summarySQL, want) {
			t.Fatalf("summary query missing %q; got: %s", want, summarySQL)
		}
	}
}

func TestTraceRepoFindTracesUnfilteredUsesOneBoundedIndexRead(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		sql := string(buf)
		queries = append(queries, sql)
		w.Header().Set("Content-Type", "text/plain")
		if strings.Contains(sql, "trace_summary_index") {
			_, _ = w.Write([]byte("trace-1\ntrace-2\n"))
			return
		}
		_, _ = w.Write([]byte("trace-1\t2026-08-20 10:00:00\t2026-08-20 10:00:05\t3\t2\t150.5\n"))
	}))
	defer srv.Close()

	repo := &TraceRepository{ch: NewClickHouseRepo(srv.URL, nil)}
	traces, err := repo.FindTraces(context.Background(), TraceQuery{
		TenantID: "tenant-1",
		Hours:    24,
		Limit:    20,
	})
	if err != nil {
		t.Fatalf("FindTraces: %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("expected one summary row, got %d", len(traces))
	}
	if len(queries) != 3 {
		t.Fatalf("expected two date-partition index reads plus one summary read, got %d queries: %v", len(queries), queries)
	}
	if !strings.Contains(queries[0], "date = today() - INTERVAL 0 DAY") ||
		!strings.Contains(queries[0], "latest_start >= now() - INTERVAL 24 HOUR") ||
		!strings.Contains(queries[0], "ORDER BY latest_start_key ASC, cluster_id ASC, trace_id ASC, service_name ASC") ||
		!strings.Contains(queries[0], "LIMIT 2000") {
		t.Fatalf("candidate read is not bounded and time ordered: %s", queries[0])
	}
}
