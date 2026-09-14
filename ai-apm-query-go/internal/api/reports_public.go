package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ReportsPublicRouter keeps report list and download links on the same
// canonical scope boundary. Legacy orchestrator history routes are not used.
func (h *Handler) ReportsPublicRouter(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), "/download") && r.Method == http.MethodGet {
		h.downloadReportPublic(w, r)
		return
	}
	h.ReportsPublic(w, r)
}

// ReportsPublic exposes the canonical, tenant-and-cluster scoped report read
// model used by the browser report workspace. Legacy orchestrator report
// history is not used here.
func (h *Handler) ReportsPublic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method_not_allowed"})
		return
	}
	auth, ok := requestAuthorizationContext(r)
	if !ok || auth.UserID == "" || auth.TenantID == "" {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
		return
	}
	clusterID := strings.TrimSpace(r.URL.Query().Get("cluster_id"))
	if clusterID == "" {
		clusterID = auth.ActiveClusterID
	}
	if clusterID == "" {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{"error": "INVALID_SCOPE"})
		return
	}
	if auth.ActiveClusterID != "" && clusterID != auth.ActiveClusterID {
		respondJSON(w, http.StatusConflict, map[string]interface{}{"error": "CONTEXT_SCOPE_MISMATCH"})
		return
	}
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 200 {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
			return
		}
		limit = parsed
	}
	db := store.GetDB()
	if db == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "persistence_unavailable"})
		return
	}
	rows, err := db.Query(`SELECT id, COALESCE(task_id,''), COALESCE(service_name,''), COALESCE(report_type,''), COALESCE(verdict,''), risk_score, COALESCE(summary,''), COALESCE(content,''), COALESCE(file_key,''), created_at, tenant_id, cluster_id
		FROM reports WHERE tenant_id = ? AND cluster_id = ? ORDER BY created_at DESC LIMIT ?`, auth.TenantID, clusterID, limit)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "report_read_failed"})
		return
	}
	defer rows.Close()
	reports := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id int64
		var taskID, serviceName, reportType, verdict, summary, content, fileKey, tenantValue, clusterValue sql.NullString
		var riskScore sql.NullFloat64
		var createdAt interface{}
		if err := rows.Scan(&id, &taskID, &serviceName, &reportType, &verdict, &riskScore, &summary, &content, &fileKey, &createdAt, &tenantValue, &clusterValue); err != nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "report_read_failed"})
			return
		}
		item := map[string]interface{}{
			"id": id, "task_id": taskID.String, "service_name": serviceName.String,
			"report_type": reportType.String, "verdict": verdict.String, "summary": summary.String,
			"content": content.String, "file_key": fileKey.String, "created_at": createdAt,
			"tenant_id": tenantValue.String, "cluster_id": clusterValue.String,
		}
		if riskScore.Valid {
			item["risk_score"] = riskScore.Float64
		}
		reports = append(reports, item)
	}
	if err := rows.Err(); err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "report_read_failed"})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"reports": reports, "count": len(reports)})
}

func (h *Handler) downloadReportPublic(w http.ResponseWriter, r *http.Request) {
	auth, ok := requestAuthorizationContext(r)
	if !ok || auth.UserID == "" || auth.TenantID == "" {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
		return
	}
	clusterID := strings.TrimSpace(r.URL.Query().Get("cluster_id"))
	if clusterID == "" {
		clusterID = auth.ActiveClusterID
	}
	if clusterID == "" {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{"error": "INVALID_SCOPE"})
		return
	}
	if auth.ActiveClusterID != "" && clusterID != auth.ActiveClusterID {
		respondJSON(w, http.StatusConflict, map[string]interface{}{"error": "CONTEXT_SCOPE_MISMATCH"})
		return
	}
	path := strings.TrimPrefix(strings.TrimRight(r.URL.Path, "/"), "/api/v1/ops/reports/")
	path = strings.TrimSuffix(path, "/download")
	reportID := strings.TrimSpace(path)
	if reportID == "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	db := store.GetDB()
	if db == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "persistence_unavailable"})
		return
	}
	var taskID, content, fileKey string
	var createdAt interface{}
	// 可空列必须显式回填：插入方未写 file_key 时值为 NULL，
	// 直接 Scan 到 string 会报错并表现为 503（真实环境验证发现的缺陷）。
	err := db.QueryRow(`SELECT COALESCE(task_id,''), COALESCE(content,''), COALESCE(file_key,''), created_at FROM reports
		WHERE tenant_id = ? AND cluster_id = ? AND (task_id = ? OR CAST(id AS CHAR) = ?) LIMIT 1`,
		auth.TenantID, clusterID, reportID, reportID).Scan(&taskID, &content, &fileKey, &createdAt)
	if err == sql.ErrNoRows {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": contract.ErrorCodeResourceNotFound})
		return
	}
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "report_read_failed"})
		return
	}
	if strings.TrimSpace(content) == "" {
		content = "报告正文尚未生成。\n"
	}
	name := taskID
	if name == "" {
		name = fileKey
	}
	if name == "" {
		name = reportID
	}
	name = strings.NewReplacer("/", "_", "\\", "_", "\"", "_", "\n", "_").Replace(name)
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.md"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(content))
}
