package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestControlPlaneKnowledgeGraphSnapshot(t *testing.T) {
	c := newCPHandler(t)
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM topology_nodes")).
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, type, name, props_json, created_at, updated_at FROM topology_nodes ORDER BY id LIMIT ? OFFSET ?")).
		WithArgs(100000, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "type", "name", "props_json", "created_at", "updated_at"}).
			AddRow(7, "service", "orders", `{"cluster_id":"cluster-1","created_by":"auto"}`, time.Now(), time.Now()))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM topology_relations")).
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, src_id, dst_id, type, props_json, created_at FROM topology_relations ORDER BY id LIMIT ? OFFSET ?")).
		WithArgs(100000, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "src_id", "dst_id", "type", "props_json", "created_at"}))

	req := c.cpReq(t, http.MethodPost, "/internal/v1/control-plane/knowledge-graph",
		"control_plane.knowledge_graph.read", `{"operation":"snapshot","cluster_id":"cluster-1"}`, nil)
	rec := httptest.NewRecorder()
	c.h.InternalControlPlaneKnowledgeGraph(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body["nodes"].([]interface{})) != 1 {
		t.Fatalf("expected one node: %v", body)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations not met: %v", err)
	}
}
