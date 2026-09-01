package tracesink

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-ingest-go/internal/model"
)

func testTopologyEdge() *model.TopologyEdge {
	return &model.TopologyEdge{
		TenantID: "7ed01afc-cc79-4ecd-8767-a2befa6168ad", ClusterID: "91771a6e-9c2d-11f1-8271-bea176fe9f9f",
		SourceService: "frontend", TargetService: "backend", TimeBucket: time.Date(2026, 9, 1, 4, 0, 0, 0, time.UTC),
		CallCount: 3, ErrorCount: 1, AvgDurationNs: 1200000, Date: "2026-09-01",
	}
}

func TestClickHouseTopologyEdgeSinkAddEdgesWritesScopedBatch(t *testing.T) {
	var gotAuth, gotQuery string
	var gotRows []map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if ok {
			gotAuth = user + ":" + pass
		}
		gotQuery = r.URL.Query().Get("query")
		if r.Method == http.MethodPost {
			for _, line := range strings.Split(strings.TrimSpace(readBody(t, r)), "\n") {
				var row map[string]interface{}
				if err := json.Unmarshal([]byte(line), &row); err != nil {
					t.Fatalf("decode row: %v", err)
				}
				gotRows = append(gotRows, row)
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink := NewClickHouseTopologyEdgeSinkAuth(srv.URL, "ingest", "secret", 5*time.Second)
	if err := sink.Probe(); err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if err := sink.AddEdges([]*model.TopologyEdge{testTopologyEdge(), testTopologyEdge()}); err != nil {
		t.Fatalf("AddEdges() error = %v", err)
	}
	if gotAuth != "ingest:secret" {
		t.Fatalf("basic auth = %q, want ingest:secret", gotAuth)
	}
	if gotQuery != "INSERT INTO observability.service_topology FORMAT JSONEachRow" {
		t.Fatalf("query = %q", gotQuery)
	}
	if len(gotRows) != 2 || gotRows[0]["tenant_id"] != testTopologyEdge().TenantID || gotRows[0]["cluster_id"] != testTopologyEdge().ClusterID {
		t.Fatalf("rows missing immutable scope: %#v", gotRows)
	}
	if !sink.Healthy() {
		t.Fatal("sink should be healthy after successful batch")
	}
}

func TestClickHouseTopologyEdgeSinkFailClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	sink := NewClickHouseTopologyEdgeSink(srv.URL, 5*time.Second)
	if err := sink.AddEdges([]*model.TopologyEdge{testTopologyEdge()}); err == nil {
		t.Fatal("AddEdges() must return sink failure")
	}
	if sink.Healthy() {
		t.Fatal("sink must be unhealthy after failed write")
	}
	if sink.LastError() == nil {
		t.Fatal("failed write must be retained for readiness diagnostics")
	}
}

func readBody(t *testing.T, r *http.Request) string {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(data)
}
