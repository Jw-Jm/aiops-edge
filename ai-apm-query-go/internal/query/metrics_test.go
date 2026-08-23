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

func TestMetricsRepoServiceRED(t *testing.T) {
	// mock CH 返回 3 行 RED 时序列（TabSeparated）。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		body := "2026-08-20 10:00:00\t10\t2\t150.5\n" +
			"2026-08-20 10:01:00\t20\t3\t200.0\n" +
			"2026-08-20 10:02:00\t30\t1\t99.5\n"
		w.Write([]byte(body))
	}))
	defer srv.Close()

	repo := &MetricsRepository{ch: NewClickHouseRepo(srv.URL, nil)}
	scope := Scope{TenantID: "3f3c3b3a-0000-4000-8000-000000000001", ClusterID: "3f3c3b3a-0000-4000-8000-000000000002"}
	points, err := repo.ServiceRED(context.Background(), scope, "checkout", 30)
	if err != nil {
		t.Fatalf("ServiceRED: %v", err)
	}
	if len(points) != 3 {
		t.Fatalf("expected 3 points, got %d", len(points))
	}
	if points[0].CallCount != 10 || points[0].ErrorCount != 2 || points[0].AvgMS != 150.5 {
		t.Fatalf("point0 = %+v", points[0])
	}
	if points[2].CallCount != 30 || points[2].AvgMS != 99.5 {
		t.Fatalf("point2 = %+v", points[2])
	}
}

func TestMetricsRepoNewModeRoutesToVMNoFallback(t *testing.T) {
	// new mode + VM reader：ServiceRED 走 VM，不查 ClickHouse。
	vmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/query_range" {
			t.Errorf("expected VM query_range, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[{"metric":{"service_name":"checkout"},"values":[["1710000000","10"]]}]}}`))
	}))
	defer vmSrv.Close()

	vm := NewVictoriaMetricsReader(vmSrv.URL, &http.Client{Timeout: 5 * time.Second})
	repo := &MetricsRepository{ch: NewClickHouseRepo("http://unreachable:9999", nil)}
	repo.WithVMRouter(vm, ModeNew)
	points, err := repo.ServiceRED(context.Background(), Scope{
		TenantID: "3f3c3b3a-0000-4000-8000-000000000001", ClusterID: "3f3c3b3a-0000-4000-8000-000000000002",
	}, "checkout", 60)
	if err != nil {
		t.Fatalf("ServiceRED new mode: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 point from VM, got %d", len(points))
	}
}

func TestMetricsRepoNewModeVMUnavailableNoFallback(t *testing.T) {
	// new mode + VM down → unavailable；绝不 fallback ClickHouse（CH 不可达也不会被调用）。
	vmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer vmSrv.Close()

	vm := NewVictoriaMetricsReader(vmSrv.URL, &http.Client{Timeout: 5 * time.Second})
	repo := &MetricsRepository{ch: NewClickHouseRepo("http://unreachable:9999", nil)}
	repo.WithVMRouter(vm, ModeNew)
	_, err := repo.ServiceRED(context.Background(), Scope{
		TenantID: "3f3c3b3a-0000-4000-8000-000000000001", ClusterID: "3f3c3b3a-0000-4000-8000-000000000002",
	}, "checkout", 60)
	var qe *QueryError
	if err == nil || !errors.As(err, &qe) || qe.Code != UnavailableCode {
		t.Fatalf("expected unavailable (no fallback), got %v", err)
	}
}

// P6.4.4-4: new mode 下 legacy ClickHouse 被 poison（不可达），生产查询仍经 VM 成功
// → 证明 old reader 实际 inactive（被 new backend 完全旁路）。
func TestMetricsNewModeLegacyPoisonedStillServes(t *testing.T) {
	vmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[{"metric":{"service_name":"checkout"},"values":[["1710000000","10"]]}]}}`))
	}))
	defer vmSrv.Close()
	vm := NewVictoriaMetricsReader(vmSrv.URL, &http.Client{Timeout: 5 * time.Second})
	// ClickHouse（legacy）poisoned：指向不可达地址。
	repo := &MetricsRepository{ch: NewClickHouseRepo("http://127.0.0.1:1", nil)}
	repo.WithVMRouter(vm, ModeNew)
	pts, err := repo.ServiceRED(context.Background(), Scope{
		TenantID: "3f3c3b3a-0000-4000-8000-000000000001", ClusterID: "3f3c3b3a-0000-4000-8000-000000000002",
	}, "checkout", 60)
	if err != nil {
		t.Fatalf("new mode with poisoned legacy must still serve via VM: %v", err)
	}
	if len(pts) != 1 {
		t.Fatalf("expected 1 point via VM, got %d", len(pts))
	}
}

func TestMetricsRepoNewModeNoVMReaderIsUnavailable(t *testing.T) {
	// new mode 但未注入 VM reader → unavailable，绝不 fallback ClickHouse。
	repo := &MetricsRepository{ch: NewClickHouseRepo("http://unreachable:9999", nil)}
	repo.WithVMRouter(nil, ModeNew)
	_, err := repo.ServiceRED(context.Background(), Scope{
		TenantID: "3f3c3b3a-0000-4000-8000-000000000001", ClusterID: "3f3c3b3a-0000-4000-8000-000000000002",
	}, "checkout", 60)
	var qe *QueryError
	if err == nil || !errors.As(err, &qe) || qe.Code != UnavailableCode {
		t.Fatalf("expected unavailable (no VM reader), got %v", err)
	}
}

func TestMetricsRepoVMTenantClusterIsolation(t *testing.T) {
	// 同名资源（service=checkout）在 cluster-A 与 cluster-B 必须隔离：PromQL 只注入本次查询的
	// tenant+cluster（P6.3.6 三字段隔离，Phase 19 两集群验收前置防线）。
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query().Get("query"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[]}}`))
	}))
	defer srv.Close()
	vm := NewVictoriaMetricsReader(srv.URL, &http.Client{Timeout: 5 * time.Second})

	tenantA := "3f3c3b3a-0000-4000-8000-000000000001"
	clusterA := "3f3c3b3a-0000-4000-8000-000000000002"
	clusterB := "3f3c3b3a-0000-4000-8000-000000000003"

	vm.ServiceRED(context.Background(), VMQuery{TenantID: tenantA, ClusterID: clusterA, Service: "checkout", Minutes: 60})
	vm.ServiceRED(context.Background(), VMQuery{TenantID: tenantA, ClusterID: clusterB, Service: "checkout", Minutes: 60})

	if len(queries) != 2 {
		t.Fatalf("expected 2 VM queries, got %d", len(queries))
	}
	if !strings.Contains(queries[0], `cluster_id="`+clusterA+`"`) || strings.Contains(queries[0], clusterB) {
		t.Fatalf("query[0] not isolated to cluster-A: %s", queries[0])
	}
	if !strings.Contains(queries[1], `cluster_id="`+clusterB+`"`) || strings.Contains(queries[1], clusterA) {
		t.Fatalf("query[1] not isolated to cluster-B: %s", queries[1])
	}
}

func TestMetricsRepoServiceREDDetailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		body := "2026-08-20 10:00:00\t10\t2\t150.5\t100.0\t200.0\t300.0\n"
		w.Write([]byte(body))
	}))
	defer srv.Close()

	repo := &MetricsRepository{ch: NewClickHouseRepo(srv.URL, nil)}
	scope := Scope{TenantID: "3f3c3b3a-0000-4000-8000-000000000001", ClusterID: "3f3c3b3a-0000-4000-8000-000000000002"}
	points, err := repo.ServiceREDDetailed(context.Background(), scope, "checkout", nil)
	if err != nil {
		t.Fatalf("ServiceREDDetailed: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(points))
	}
	p := points[0]
	if p.CallCount != 10 || p.P95MS != 200.0 || p.P99MS != 300.0 {
		t.Fatalf("point = %+v", p)
	}
}

func TestMetricsRepoServiceREDSQLOwnership(t *testing.T) {
	// 验证 repository 生成的 SQL 含 tenant/cluster scope 与 RED 聚合列（SQL ownership
	// 在 repository，而非 handler）。
	var gotQ string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		gotQ = string(buf)
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(""))
	}))
	defer srv.Close()

	repo := &MetricsRepository{ch: NewClickHouseRepo(srv.URL, nil)}
	scope := Scope{TenantID: "3f3c3b3a-0000-4000-8000-000000000001", ClusterID: "3f3c3b3a-0000-4000-8000-000000000002"}
	repo.ServiceRED(context.Background(), scope, "checkout", 30)

	for _, want := range []string{
		"tenant_id='3f3c3b3a-0000-4000-8000-000000000001'",
		"cluster_id='3f3c3b3a-0000-4000-8000-000000000002'",
		"service_name='checkout'",
		"countIf(is_error=1) as error_count",
	} {
		if !strings.Contains(gotQ, want) {
			t.Errorf("repo SQL missing %q; got: %s", want, gotQ)
		}
	}
}

func TestMetricsRepoServiceRuleValue(t *testing.T) {
	// ServiceRuleValue 用 QueryJSON（GET + JSONEachRow），按查询关键字分发返回。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("query")
		switch {
		case strings.Contains(q, "as errors, count() as total"):
			_, _ = w.Write([]byte(`{"errors":2,"total":100}` + "\n"))
		case strings.Contains(q, "as cnt"):
			_, _ = w.Write([]byte(`{"cnt":42}` + "\n"))
		case strings.Contains(q, "p95_ms"):
			_, _ = w.Write([]byte(`{"p95_ms":123.5}` + "\n"))
		case strings.Contains(q, "p99_ms"):
			_, _ = w.Write([]byte(`{"p99_ms":200.0}` + "\n"))
		default:
			_, _ = w.Write([]byte(""))
		}
	}))
	defer srv.Close()

	repo := &MetricsRepository{ch: NewClickHouseRepo(srv.URL, nil)}
	ctx := context.Background()

	if v, err := repo.ServiceRuleValue(ctx, "checkout", "error_rate", 5); err != nil || v != 2.0 {
		t.Fatalf("error_rate = %v, %v; want 2.0", v, err)
	}
	if v, err := repo.ServiceRuleValue(ctx, "checkout", "calls", 5); err != nil || v != 42 {
		t.Fatalf("calls = %v, %v; want 42", v, err)
	}
	if v, err := repo.ServiceRuleValue(ctx, "checkout", "latency_p95", 5); err != nil || v != 123.5 {
		t.Fatalf("latency_p95 = %v, %v; want 123.5", v, err)
	}
	if v, err := repo.ServiceRuleValue(ctx, "checkout", "latency_p99", 5); err != nil || v != 200.0 {
		t.Fatalf("latency_p99 = %v, %v; want 200.0", v, err)
	}
	if _, err := repo.ServiceRuleValue(ctx, "checkout", "nope", 5); err == nil {
		t.Fatal("expected error for unknown metric")
	}
}
