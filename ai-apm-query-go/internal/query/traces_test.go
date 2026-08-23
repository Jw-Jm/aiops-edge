package query

import (
	"context"
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

func TestTraceRepoFindTracesSQLOwnership(t *testing.T) {
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
		"service_name='checkout'",
		"GROUP BY trace_id",
		"ORDER BY start DESC",
		"LIMIT 20 OFFSET 0",
	} {
		if !strings.Contains(gotQ, want) {
			t.Errorf("repo SQL missing %q; got: %s", want, gotQ)
		}
	}
}
