package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// AI 运维报告（§6 报告 / 设计文档 G4）：必须从已完成或明确终止的 task/run 生成，
// 包含范围、时间线、证据、假设与反证、结论等级、建议和动作、预检与确认、执行
// 结果、回滚记录、恢复验证、遗留风险与全部相关版本。页面/PDF/Word/CSV/API 使用
// 同一数据与聚合合同（此处为唯一聚合源头，报告 content 即导出合同的 Markdown 投影）。
// ─────────────────────────────────────────────────────────────────────────────

type aiOperationsReportRequest struct {
	RunID string `json:"run_id"`
}

// GenerateAIOperationsReport handles POST /api/v1/ops/reports/ai-operations.
func (h *Handler) GenerateAIOperationsReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
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
	var req aiOperationsReportRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	runID := strings.TrimSpace(req.RunID)
	if runID == "" {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{"error": contract.ErrorCodeValidationFailed, "detail": "run_id 必填"})
		return
	}
	if h.runDAO == nil || h.evidenceDAO == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "persistence_unavailable"})
		return
	}
	// 租户/集群隔离：Run 必须属于调用者的租户与活动集群（与 Run 深链同一边界）。
	run, err := h.runDAO.Get(runID)
	if err != nil || run == nil {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": "RUN_NOT_FOUND"})
		return
	}
	if auth.TenantID != "" && run.TenantID != auth.TenantID {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": contract.ErrorCodeTenantAccessDenied})
		return
	}
	if run.PrimaryClusterID != "" && run.PrimaryClusterID != clusterID {
		respondJSON(w, http.StatusConflict, map[string]interface{}{"error": "CONTEXT_SCOPE_MISMATCH"})
		return
	}
	// 仅已完成或明确终止的 Run 可生成报告（§6：不得从进行中的调查生成报告）。
	switch run.Status {
	case "success", "partial", "failed", "regressed", "cancelled":
	default:
		respondJSON(w, http.StatusConflict, map[string]interface{}{"error": "RUN_NOT_TERMINAL", "detail": "仅已完成或明确终止的任务可生成 AI 运维报告"})
		return
	}
	evs, err := h.evidenceDAO.ListByRun(runID, run.TenantID, run.PrimaryClusterID)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "persistence_unavailable"})
		return
	}

	verdict := "Unknown"
	report := aiOperationsReportDoc{
		Scope: map[string]string{
			"tenant_id": run.TenantID, "cluster_id": clusterID,
			"run_id": runID, "request_id": run.RequestID, "intent": run.Intent,
		},
		TimeWindow: map[string]string{
			"from": formatTimePtr(run.TimeRangeStart), "to": formatTimePtr(run.TimeRangeEnd),
			"symptom_time": formatTimePtr(run.TimeRangeEnd),
		},
		Conclusion: map[string]interface{}{
			"status": run.Status,
		},
		Versions: map[string]string{
			"platform_version": osGetenvDefault("AIOPS_VERSION", ""),
			"report_schema":    "ai-operations-report/v1",
		},
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Warnings:    []string{},
	}

	// —— 证据清单（evidence_id/类型/来源/时间/摘要，与 API 证据查询同源）——
	for _, ev := range evs {
		report.Evidence = append(report.Evidence, map[string]interface{}{
			"evidence_id": ev.EvidenceID, "evidence_type": ev.EvidenceType,
			"source_ref": ev.SourceRef, "collected_at": ev.CollectedAt.Format(time.RFC3339),
			"summary": ev.Summary,
		})
	}
	// 假设（候选原因）与反证：服务端权威投影（deriveRunRootCause），仅证据
	// 确认的假设可成为根因 —— 与 Run 详情读模型同一投影规则。
	rootCause := ""
	confidence := 0.0
	if h.hypothesisDAO != nil {
		if hypotheses, herr := h.hypothesisDAO.ListByRun(runID); herr == nil {
			rootCause, confidence = deriveRunRootCause(hypotheses)
			for _, hyp := range hypotheses {
				report.Hypotheses = append(report.Hypotheses, map[string]interface{}{
					"content": hyp.Content, "confidence": hyp.Confidence,
					"confirmed_by_evidence": hyp.ConfirmedByEvidence,
				})
			}
		}
	}
	verdict = aiConclusionVerdict(run.Status, confidence)
	report.Conclusion["verdict"] = verdict
	report.Conclusion["root_cause"] = rootCause
	report.Conclusion["confidence"] = confidence
	// 结论等级由确定性状态机投影，证据不足时不得输出 Confirmed。
	if verdict == "Confirmed" && len(report.Evidence) == 0 {
		verdict = "Unknown"
		report.Conclusion["verdict"] = verdict
		report.Warnings = append(report.Warnings, "证据不足，结论已降级")
	}
	// 处置动作记录（预检/确认/执行/回滚的审计投影）。
	if h.actionDAO != nil {
		if actions, aerr := h.actionDAO.ListByRun(runID); aerr == nil {
			for _, act := range actions {
				report.Actions = append(report.Actions, map[string]interface{}{
					"action_id": act.ActionID, "action_type": act.ActionType,
					"status": act.Status, "risk_level": act.ProposedRisk,
					"preflight_status": act.PreflightStatus,
				})
			}
		}
	}
	// —— 图谱上下文版本（调查固定并记录的 graph generation）——
	if h.runGraphDAO != nil {
		if gctx, gerr := h.runGraphDAO.Get(runID, 1, run.TenantID); gerr == nil && gctx != nil {
			report.GraphContextVersion = gctx.ContextVersion
			if gctx.ContextJSON != "" {
				var parsed map[string]interface{}
				if json.Unmarshal([]byte(gctx.ContextJSON), &parsed) == nil {
					if meta, mok := parsed["meta"].(map[string]interface{}); mok {
						if gen, gok := meta["graph_generation"].(float64); gok {
							report.Versions["graph_generation"] = fmt.Sprintf("%d", int64(gen))
						}
					}
					if codes, cok := parsed["warning_codes"].([]interface{}); cok {
						for _, c := range codes {
							report.Warnings = append(report.Warnings, fmt.Sprintf("%v", c))
						}
					}
				}
			}
		}
	}

	content := renderAIOperationsMarkdown(report)
	summary := fmt.Sprintf("结论等级 %s；证据 %d 条；结论 %s", verdict, len(report.Evidence),
		stringOrDash(rootCause))
	reportID, err := insertAIOperationsReport(r.Context(), run.TenantID, clusterID, runID, verdict, summary, content)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "persistence_unavailable"})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"report_id": reportID, "report_type": "ai_operations", "run_id": runID,
		"verdict": verdict, "evidence_count": len(report.Evidence), "summary": summary,
	})
}

func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func stringOrDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "未定位"
	}
	return s
}

func osGetenvDefault(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

// aiConclusionVerdict 将 Run 终态投影为设计规定的结论等级（确定性、无随机）。
func aiConclusionVerdict(status string, confidence float64) string {
	switch status {
	case "success":
		if confidence >= 0.8 {
			return "Confirmed"
		}
		if confidence >= 0.5 {
			return "Supported"
		}
		return "Candidate"
	case "partial":
		if confidence >= 0.5 {
			return "Candidate"
		}
		return "Unknown"
	default:
		return "Unknown"
	}
}

type aiOperationsReportDoc struct {
	Scope               map[string]string        `json:"scope"`
	TimeWindow          map[string]string        `json:"time_window"`
	Conclusion          map[string]interface{}   `json:"conclusion"`
	Evidence            []map[string]interface{} `json:"evidence"`
	Hypotheses          []map[string]interface{} `json:"hypotheses,omitempty"`
	Actions             []map[string]interface{} `json:"actions,omitempty"`
	GraphContextVersion int64                    `json:"graph_context_version,omitempty"`
	Versions            map[string]string        `json:"versions"`
	GeneratedAt         string                   `json:"generated_at"`
	Warnings            []string                 `json:"warnings"`
}

// renderAIOperationsMarkdown 生成页面/下载/API 共用的同一内容投影。
func renderAIOperationsMarkdown(doc aiOperationsReportDoc) string {
	var b strings.Builder
	b.WriteString("# AI 运维报告\n\n")
	b.WriteString("## 范围\n")
	for _, k := range []string{"tenant_id", "cluster_id", "run_id", "request_id"} {
		b.WriteString(fmt.Sprintf("- %s: %s\n", k, doc.Scope[k]))
	}
	b.WriteString("\n## 时间窗\n")
	for _, k := range []string{"from", "to"} {
		b.WriteString(fmt.Sprintf("- %s: %s\n", k, doc.TimeWindow[k]))
	}
	c, _ := doc.Conclusion["status"].(string)
	rootCause, _ := doc.Conclusion["root_cause"].(string)
	confidence, _ := doc.Conclusion["confidence"].(float64)
	verdict, _ := doc.Conclusion["verdict"].(string)
	b.WriteString("\n## 结论等级\n")
	b.WriteString(fmt.Sprintf("- 等级: %s\n- 任务终态: %s\n- 置信度: %.2f\n- 根因: %s\n",
		verdict, c, confidence, stringOrDash(rootCause)))
	b.WriteString("\n## 证据\n")
	if len(doc.Evidence) == 0 {
		b.WriteString("- 本次调查未采集到通过校验入库的证据\n")
	}
	for _, e := range doc.Evidence {
		b.WriteString(fmt.Sprintf("- %v (%v, 来源 %v, 采集于 %v)\n",
			e["evidence_id"], e["evidence_type"], e["source_ref"], e["collected_at"]))
	}
	if len(doc.Hypotheses) > 0 {
		b.WriteString("\n## 候选原因与反证\n")
		for _, hyp := range doc.Hypotheses {
			confirmed, _ := hyp["confirmed_by_evidence"].(bool)
			mark := "未确认"
			if confirmed {
				mark = "证据确认"
			}
			b.WriteString(fmt.Sprintf("- %v（置信度 %.2f，%v）\n", hyp["content"], hyp["confidence"], mark))
		}
	}
	if len(doc.Actions) > 0 {
		b.WriteString("\n## 建议与动作（预检/确认/执行）\n")
		for _, act := range doc.Actions {
			b.WriteString(fmt.Sprintf("- %v：%v（风险 %v，预检 %v）\n",
				act["action_type"], act["status"], act["risk_level"], act["preflight_status"]))
		}
	}
	b.WriteString("\n## 版本\n")
	for _, k := range []string{"platform_version", "report_schema", "graph_generation"} {
		if v := doc.Versions[k]; v != "" {
			b.WriteString(fmt.Sprintf("- %s: %s\n", k, v))
		}
	}
	if len(doc.Warnings) > 0 {
		b.WriteString("\n## 警示与遗留风险\n")
		for _, warn := range doc.Warnings {
			b.WriteString(fmt.Sprintf("- %s\n", warn))
		}
	}
	b.WriteString(fmt.Sprintf("\n生成时间: %s\n", doc.GeneratedAt))
	return b.String()
}

func insertAIOperationsReport(ctx context.Context, tenantID, clusterID, runID, verdict, summary, content string) (int64, error) {
	db := store.GetDB()
	if db == nil {
		return 0, fmt.Errorf("persistence unavailable")
	}
	taskID := fmt.Sprintf("ai-operations-%s", runID)
	fileKey := fmt.Sprintf("reports/%s/ai-operations-%s.md", tenantID, runID)
	res, err := db.ExecContext(ctx,
		`INSERT INTO reports (task_id, service_name, report_type, verdict, risk_score, summary, content, file_key, tenant_id, cluster_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		taskID, "ai-operations", "ai_operations", verdict, riskScoreOf(verdict), summary, content, fileKey, tenantID, clusterID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
