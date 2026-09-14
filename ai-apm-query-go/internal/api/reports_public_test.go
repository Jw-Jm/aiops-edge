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

func TestListReportsPublicScopesByTenantAndCluster(t *testing.T) {
	h := &Handler{}
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	// 可空列用 COALESCE 显式回填，避免 NULL 直接 Scan 到 string 报 503。
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, COALESCE(task_id,''), COALESCE(service_name,''), COALESCE(report_type,''), COALESCE(verdict,''), risk_score, COALESCE(summary,''), COALESCE(content,''), COALESCE(file_key,''), created_at, tenant_id, cluster_id")).
		WithArgs("tenant-a", "cluster-a", 100).
		WillReturnRows(sqlmock.NewRows([]string{"id", "task_id", "service_name", "report_type", "verdict", "risk_score", "summary", "content", "file_key", "created_at", "tenant_id", "cluster_id"}).
			AddRow(7, "task-7", "checkout", "diagnostic", "degraded", 0.8, "Pod 未就绪", "# report", "", time.Now(), "tenant-a", "cluster-a"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ops/reports?cluster_id=cluster-a&limit=100", nil)
	req = withAuthorizationContext(req, AuthorizationContext{UserID: "user-a", TenantID: "tenant-a", ActiveClusterID: "cluster-a"})
	rec := httptest.NewRecorder()
	h.ReportsPublic(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Reports []map[string]any `json:"reports"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Reports) != 1 || body.Reports[0]["task_id"] != "task-7" || body.Reports[0]["cluster_id"] != "cluster-a" {
		t.Fatalf("unexpected reports response: %s", rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadReportPublicScopesByTenantAndCluster(t *testing.T) {
	h := &Handler{}
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	// 查询文本必须包含 COALESCE（真实 MySQL 中 NULL 会转成空串）。
	// 注意：sqlmock 不执行 SQL 表达式，驱动层返回的列值仍是测试给定值，
	// 因此这里用空串而不是 nil —— NULL 场景由真实环境验证覆盖（§7C.3 D12）。
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COALESCE(task_id,''), COALESCE(content,''), COALESCE(file_key,''), created_at FROM reports")).
		WithArgs("tenant-a", "cluster-a", "task-7", "task-7").
		WillReturnRows(sqlmock.NewRows([]string{"task_id", "content", "file_key", "created_at"}).AddRow("task-7", "# report\n", "", time.Now()))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ops/reports/task-7/download", nil)
	req = withAuthorizationContext(req, AuthorizationContext{UserID: "user-a", TenantID: "tenant-a", ActiveClusterID: "cluster-a"})
	rec := httptest.NewRecorder()
	h.ReportsPublicRouter(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/markdown; charset=utf-8" || rec.Body.String() != "# report\n" {
		t.Fatalf("unexpected download response: %d %q", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
