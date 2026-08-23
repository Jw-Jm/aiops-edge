package query

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockTopologyCH 返回一个按查询内容分发 JSONEachRow 的 mock ClickHouse。
// 由于 TopologyRepository 用 QueryJSON（GET + query 参数），mock 从 URL query 读 SQL。
func mockTopologyCH(t *testing.T, dispatch map[string]string) *TopologyRepository {
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
	return NewTopologyRepository(NewClickHouseRepo(srv.URL, nil))
}

const topoSvcStats = "" +
	`{"service_name":"frontend","calls":100,"errors":2,"lat_sum":150000000000}` + "\n" +
	`{"service_name":"backend","calls":80,"errors":5,"lat_sum":120000000000}` + "\n"

const topoDistinctSvcs = "" +
	`{"s":"frontend"}` + "\n" +
	`{"s":"backend"}` + "\n" +
	`{"s":"db"}` + "\n"

func TestTopologyRepoServiceREDStats(t *testing.T) {
	r := mockTopologyCH(t, map[string]string{"GROUP BY service_name": topoSvcStats})
	stats, err := r.ServiceREDStats(context.Background(), TopologyScope{
		TenantID: "3f3c3b3a-0000-4000-8000-000000000001",
		ClusterID: "3f3c3b3a-0000-4000-8000-000000000002",
	})
	if err != nil {
		t.Fatalf("ServiceREDStats: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("expected 2 stats, got %d", len(stats))
	}
	if stats[0].Service != "frontend" || stats[0].Calls != 100 || stats[0].Errors != 2 || stats[0].LatSumNs != 150000000000 {
		t.Fatalf("stats[0] = %+v", stats[0])
	}
	if stats[1].Service != "backend" || stats[1].Errors != 5 {
		t.Fatalf("stats[1] = %+v", stats[1])
	}
}

func TestTopologyRepoDistinctTopologyServices(t *testing.T) {
	r := mockTopologyCH(t, map[string]string{"service_topology": topoDistinctSvcs})
	svcs, err := r.DistinctTopologyServices(context.Background(), TopologyScope{TenantID: "t1"})
	if err != nil {
		t.Fatalf("DistinctTopologyServices: %v", err)
	}
	if len(svcs) != 3 {
		t.Fatalf("expected 3 services, got %d: %v", len(svcs), svcs)
	}
	want := map[string]bool{"frontend": true, "backend": true, "db": true}
	for _, s := range svcs {
		if !want[s] {
			t.Fatalf("unexpected service %q", s)
		}
	}
}

func TestTopologyRepoEdgeCount(t *testing.T) {
	r := mockTopologyCH(t, map[string]string{"source_service": `{"cnt":7}` + "\n"})
	n, err := r.EdgeCount(context.Background(), TopologyScope{TenantID: "t1"})
	if err != nil {
		t.Fatalf("EdgeCount: %v", err)
	}
	if n != 7 {
		t.Fatalf("EdgeCount = %d, want 7", n)
	}
}

func TestTopologyRepoP95Latency(t *testing.T) {
	r := mockTopologyCH(t, map[string]string{"p95_ms": `{"p95_ms":123.4}` + "\n"})
	v, err := r.P95Latency(context.Background(), TopologyScope{TenantID: "t1"})
	if err != nil {
		t.Fatalf("P95Latency: %v", err)
	}
	if v != 123.4 {
		t.Fatalf("P95Latency = %v, want 123.4", v)
	}
}

func TestTopologyRepoHourlyTrend(t *testing.T) {
	rows := "" +
		`{"t":"2026-08-20 10:00:00","calls":10,"errors":1}` + "\n" +
		`{"t":"2026-08-20 11:00:00","calls":20,"errors":2}` + "\n"
	r := mockTopologyCH(t, map[string]string{"toStartOfHour": rows})
	trend, err := r.HourlyTrend(context.Background(), TopologyScope{TenantID: "t1"})
	if err != nil {
		t.Fatalf("HourlyTrend: %v", err)
	}
	if len(trend) != 2 {
		t.Fatalf("expected 2 trend points, got %d", len(trend))
	}
	if trend[0].T != "2026-08-20 10:00:00" || trend[0].Calls != 10 || trend[0].Errors != 1 {
		t.Fatalf("trend[0] = %+v", trend[0])
	}
}

func TestTopologyRepoTopErrors(t *testing.T) {
	rows := "" +
		`{"s":"frontend","errors":5}` + "\n" +
		`{"s":"backend","errors":3}` + "\n"
	r := mockTopologyCH(t, map[string]string{"is_error=1": rows})
	items, err := r.TopErrors(context.Background(), TopologyScope{TenantID: "t1"}, 10)
	if err != nil {
		t.Fatalf("TopErrors: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Service != "frontend" || items[0].Errors != 5 {
		t.Fatalf("items[0] = %+v", items[0])
	}
}

func TestTopologyRepoGlobalEdges(t *testing.T) {
	rows := "" +
		`{"source_service":"frontend","target_service":"backend","calls":10,"errs":1,"avg_ns":1000000}` + "\n"
	r := mockTopologyCH(t, map[string]string{"service_topology": rows})
	edges, err := r.GlobalEdges(context.Background(), TopologyScope{TenantID: "t1"}, 1440)
	if err != nil {
		t.Fatalf("GlobalEdges: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	e := edges[0]
	if e.Source != "frontend" || e.Target != "backend" || e.Calls != 10 || e.Errors != 1 || e.AvgNs != 1000000 {
		t.Fatalf("edge = %+v", e)
	}
}

func TestTopologyRepoGlobalNodes(t *testing.T) {
	rows := "" +
		`{"service":"frontend","calls":100,"errs":2,"avg_ns":1000000}` + "\n"
	r := mockTopologyCH(t, map[string]string{"GROUP BY service_name": rows})
	nodes, err := r.GlobalNodes(context.Background(), TopologyScope{TenantID: "t1"}, 1440)
	if err != nil {
		t.Fatalf("GlobalNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Service != "frontend" || nodes[0].Calls != 100 || nodes[0].Errors != 2 || nodes[0].AvgNs != 1000000 {
		t.Fatalf("node = %+v", nodes[0])
	}
}

func TestTopologyRepoGlobalServiceNS(t *testing.T) {
	rows := "" +
		`{"service":"frontend","ns":"app","calls":100}` + "\n"
	r := mockTopologyCH(t, map[string]string{"k8s_namespace": rows})
	nsMap, err := r.GlobalServiceNS(context.Background(), TopologyScope{TenantID: "t1"}, 1440)
	if err != nil {
		t.Fatalf("GlobalServiceNS: %v", err)
	}
	if nsMap["frontend"] != "app" {
		t.Fatalf("GlobalServiceNS = %v, want frontend=app", nsMap)
	}
}

func TestTopologyRepoNodeDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("query")
		switch {
		case strings.Contains(q, "GROUP BY trace_id"):
			_, _ = w.Write([]byte(`{"trace_id":"tr-1","start":"2026-08-20 10:00:00","end":"2026-08-20 10:00:05","spans":3,"max_ms":99.0,"errors":1}` + "\n"))
		case strings.Contains(q, "operation_name"):
			_, _ = w.Write([]byte(`{"span_id":"sp-1","trace_id":"tr-1","start_time":"2026-08-20 10:00:00","service_name":"frontend","operation_name":"GET /x","ms":10.0,"is_error":0,"http_url":"http://x"}` + "\n"))
		case strings.Contains(q, "toStartOfMinute(start_time)"):
			_, _ = w.Write([]byte(`{"t":"2026-08-20 10:00:00","calls":10,"errors":1,"avg_ms":12.0}` + "\n"))
		case strings.Contains(q, "as calls, countIf(is_error=1) as errors, avg(duration_ns)/1000000 as avg_ms"):
			_, _ = w.Write([]byte(`{"calls":100,"errors":2,"avg_ms":15.5,"max_ms":200.0}` + "\n"))
		default:
			_, _ = w.Write([]byte(""))
		}
	}))
	defer srv.Close()

	r := NewTopologyRepository(NewClickHouseRepo(srv.URL, nil))
	d, err := r.NodeDetail(context.Background(), TopologyScope{TenantID: "t1"}, "frontend", 15)
	if err != nil {
		t.Fatalf("NodeDetail: %v", err)
	}
	if d.Metrics.Calls != 100 || d.Metrics.Errors != 2 || d.Metrics.AvgMS != 15.5 || d.Metrics.MaxMS != 200.0 {
		t.Fatalf("metrics = %+v", d.Metrics)
	}
	if len(d.Trend) != 1 || d.Trend[0].Calls != 10 {
		t.Fatalf("trend = %+v", d.Trend)
	}
	if len(d.Traces) != 1 || d.Traces[0].TraceID != "tr-1" || d.Traces[0].Spans != 3 {
		t.Fatalf("traces = %+v", d.Traces)
	}
	if len(d.Spans) != 1 || d.Spans[0].SpanID != "sp-1" || d.Spans[0].MS != 10.0 {
		t.Fatalf("spans = %+v", d.Spans)
	}
}

func TestTopologyRepoSQLOwnershipScope(t *testing.T) {
	// 验证 repository 生成的 SQL 含 tenant/cluster/service scope（SQL ownership 在 repository）。
	var gotQ string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQ = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(""))
	}))
	defer srv.Close()
	r := NewTopologyRepository(NewClickHouseRepo(srv.URL, nil))
	r.ServiceREDStats(context.Background(), TopologyScope{
		TenantID:  "3f3c3b3a-0000-4000-8000-000000000001",
		ClusterID: "3f3c3b3a-0000-4000-8000-000000000002",
		Services:  []string{"frontend", "backend"},
	})
	for _, want := range []string{
		"tenant_id='3f3c3b3a-0000-4000-8000-000000000001'",
		"cluster_id='3f3c3b3a-0000-4000-8000-000000000002'",
		"service_name IN ('frontend','backend')",
	} {
		if !strings.Contains(gotQ, want) {
			t.Errorf("repo SQL missing %q; got: %s", want, gotQ)
		}
	}
}

func TestTopologyRepoNoData(t *testing.T) {
	r := mockTopologyCH(t, map[string]string{"GROUP BY service_name": ""})
	_, err := r.ServiceREDStats(context.Background(), TopologyScope{TenantID: "t1"})
	if err == nil {
		t.Fatal("expected no_data error for empty response")
	}
	var qe *QueryError
	if !errors.As(err, &qe) || qe.Code != NoDataCode {
		t.Fatalf("expected no_data, got %v", err)
	}
}
