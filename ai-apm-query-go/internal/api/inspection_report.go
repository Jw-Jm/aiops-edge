package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// 巡检报告生成（设计规范 §6.7）。
//
// 要求：页面、导出和 API 使用同一聚合与单位；每份报告绑定 scope、时间窗、
// query contract 与各版本；Partial/Stale 必须写入正文。
// 这里只聚合服务端已经可以验证的真实事实，缺失来源显式写入正文而不是填 0。

type inspectionSection struct {
	Key         string            `json:"key"`
	Title       string            `json:"title"`
	Status      string            `json:"status"`
	Facts       []map[string]any  `json:"facts"`
	Gaps        []string          `json:"gaps"`
	Source      string            `json:"source"`
	QueryWindow map[string]string `json:"query_window"`
}

type inspectionReportDoc struct {
	Scope         map[string]string   `json:"scope"`
	TimeWindow    map[string]string   `json:"time_window"`
	QueryContract string              `json:"query_contract"`
	Versions      map[string]string   `json:"versions"`
	Sections      []inspectionSection `json:"sections"`
	Quality       string              `json:"quality"`
	Warnings      []string            `json:"warnings"`
}

type inspectionReportRequest struct {
	Template string `json:"template"`
	From     string `json:"from"`
	To       string `json:"to"`
}

const inspectionQueryContract = "inspection-report-v1"

// InspectReportGenerate handles POST /api/v1/ops/reports/inspection.
func (h *Handler) InspectReportGenerate(w http.ResponseWriter, r *http.Request) {
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
	// 客户端提交的 cluster 只是请求参数：必须与权威活动集群一致才继续。
	if auth.ActiveClusterID != "" && clusterID != auth.ActiveClusterID {
		respondJSON(w, http.StatusConflict, map[string]interface{}{"error": "CONTEXT_SCOPE_MISMATCH"})
		return
	}
	clusters, err := (&store.ClusterDAO{}).ListForTenant(auth.TenantID)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "CLUSTER_UNAVAILABLE"})
		return
	}
	var target *store.Cluster
	for i := range clusters {
		if clusters[i].ClusterID == clusterID {
			target = &clusters[i]
			break
		}
	}
	if target == nil {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": "CLUSTER_NOT_FOUND"})
		return
	}

	var req inspectionReportRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	to := time.Now().UTC()
	from := to.Add(-24 * time.Hour)
	if strings.TrimSpace(req.To) != "" {
		if ts, perr := parseEventTime(req.To); perr {
			to = ts
		}
	}
	if strings.TrimSpace(req.From) != "" {
		if ts, perr := parseEventTime(req.From); perr {
			from = ts
		}
	}
	if from.After(to) {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed, "detail": "from 必须早于 to"})
		return
	}
	template := strings.TrimSpace(req.Template)
	if template == "" {
		template = "standard"
	}
	if template != "standard" && template != "capacity" && template != "kubernetes" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed, "detail": "未知巡检模板"})
		return
	}

	window := map[string]string{"from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339)}
	doc := inspectionReportDoc{
		Scope: map[string]string{
			"tenant_id": auth.TenantID, "cluster_id": clusterID, "cluster_name": target.Name,
			"environment": target.Environment, "region": target.Region,
		},
		TimeWindow:    window,
		QueryContract: inspectionQueryContract,
		Versions: map[string]string{
			"platform_version": os.Getenv("AIOPS_VERSION"),
			"kubernetes":       target.Version,
			"cluster_slug":     target.Slug,
		},
		Warnings: []string{},
	}

	// —— 告警 ——
	alertSection := inspectionSection{Key: "alerts", Title: "告警", Status: "healthy", Source: "observability.alert_events", QueryWindow: window}
	if h.alertRepo == nil {
		alertSection.Status = "not_connected"
		alertSection.Gaps = append(alertSection.Gaps, "告警来源未配置，未能核对窗口内告警")
		doc.Warnings = append(doc.Warnings, "alerts source not connected")
	} else {
		events, aerr := h.alertRepo.ListEventsScoped(r.Context(), auth.TenantID, clusterID, &from, &to, "", 200, 0)
		if aerr != nil {
			alertSection.Status = "failed"
			alertSection.Gaps = append(alertSection.Gaps, "告警读取失败："+aerr.Error())
			doc.Warnings = append(doc.Warnings, "alert read failed")
		} else {
			bySeverity := map[string]int{}
			unresolved := 0
			for _, e := range events {
				sev := strings.ToLower(strings.TrimSpace(e.Severity))
				if sev == "" {
					sev = "unknown"
				}
				bySeverity[sev]++
				if strings.ToLower(strings.TrimSpace(e.Status)) != "resolved" {
					unresolved++
				}
			}
			alertSection.Facts = append(alertSection.Facts,
				map[string]any{"name": "窗口内告警事件", "value": len(events), "unit": "条"},
				map[string]any{"name": "未解决告警", "value": unresolved, "unit": "条"},
			)
			for sev, count := range bySeverity {
				alertSection.Facts = append(alertSection.Facts, map[string]any{"name": "严重度 " + sev, "value": count, "unit": "条"})
			}
			if unresolved > 0 {
				alertSection.Status = "partial"
			}
		}
	}
	doc.Sections = append(doc.Sections, alertSection)

	// —— 计算与容量（仅本地集群有真实指标来源） ——
	computeSection := inspectionSection{Key: "compute", Title: "计算与容量", Status: "healthy", Source: "metrics.k8s.io + core/v1 nodes.allocatable", QueryWindow: window}
	localClusterID := strings.TrimSpace(os.Getenv("AIOPS_SYSTEM_CLUSTER_ID"))
	if localClusterID == "" || clusterID != localClusterID {
		computeSection.Status = "not_connected"
		computeSection.Gaps = append(computeSection.Gaps, "该集群未接入中央指标读取通道；不使用 0 或健康值代替")
		doc.Warnings = append(doc.Warnings, "compute capacity not connected")
	} else if nodeData, nerr := k8sAPIFn("/api/v1/nodes"); nerr != nil {
		computeSection.Status = "failed"
		computeSection.Gaps = append(computeSection.Gaps, "节点读取失败："+nerr.Error())
	} else if samples, _ := parseNodeAllocatable(nodeData); len(samples) == 0 {
		computeSection.Status = "failed"
		computeSection.Gaps = append(computeSection.Gaps, "节点列表为空，无法计算集群口径")
	} else {
		if metricsData, merr := k8sAPIFn("/apis/metrics.k8s.io/v1beta1/nodes"); merr == nil {
			if matched := applyNodeUsage(samples, metricsData); matched == 0 {
				computeSection.Status = "partial"
				computeSection.Gaps = append(computeSection.Gaps, "metrics-server 未返回任何节点用量")
			} else if matched < len(samples) {
				computeSection.Status = "partial"
				computeSection.Gaps = append(computeSection.Gaps, "部分节点缺少用量样本")
			}
		} else {
			computeSection.Status = "partial"
			computeSection.Gaps = append(computeSection.Gaps, "metrics-server 不可用，容量分母可用而用量缺失")
		}
		fact := aggregateClusterCapacity(*target, samples, to, computeSection.Status, "")
		computeSection.Facts = append(computeSection.Facts,
			map[string]any{"name": "CPU 使用率", "value": ratioPercent(fact.CPU.UsageRatio), "unit": "%", "aggregation": fact.CPU.Aggregation, "used": fact.CPU.Used, "allocatable": fact.CPU.Allocatable, "unit_raw": fact.CPU.Unit},
			map[string]any{"name": "内存使用率", "value": ratioPercent(fact.Memory.UsageRatio), "unit": "%", "aggregation": fact.Memory.Aggregation, "used_bytes": fact.Memory.Used, "allocatable_bytes": fact.Memory.Allocatable, "unit_raw": fact.Memory.Unit},
			map[string]any{"name": "节点就绪", "value": fmt.Sprintf("%d/%d", fact.Nodes.Ready, fact.Nodes.Total), "unit": "节点"},
			map[string]any{"name": "P95 节点 CPU 利用率", "value": floatOrText(fact.P95CPUUtilization), "unit": "%"},
			map[string]any{"name": "最大节点 CPU 利用率", "value": floatOrText(fact.MaxCPUUtilization), "unit": "%"},
			map[string]any{"name": "热点节点数", "value": fact.HotNodeCount, "unit": "个", "threshold_pct": fact.HotNodeThresholdPct},
		)
	}
	doc.Sections = append(doc.Sections, computeSection)

	// —— Kubernetes ——
	k8sSection := inspectionSection{Key: "kubernetes", Title: "Kubernetes", Status: "healthy", Source: "core/v1 pods", QueryWindow: window}
	if localClusterID == "" || clusterID != localClusterID {
		k8sSection.Status = "not_connected"
		k8sSection.Gaps = append(k8sSection.Gaps, "该集群未接入运行时读取通道")
		doc.Warnings = append(doc.Warnings, "kubernetes runtime not connected")
	} else if podData, perr := k8sAPIFn("/api/v1/pods"); perr != nil {
		k8sSection.Status = "failed"
		k8sSection.Gaps = append(k8sSection.Gaps, "Pod 读取失败："+perr.Error())
	} else if pods, restarts, parsed := parsePodRuntime(podData); !parsed {
		k8sSection.Status = "failed"
		k8sSection.Gaps = append(k8sSection.Gaps, "Pod 列表解析失败")
	} else {
		k8sSection.Facts = append(k8sSection.Facts,
			map[string]any{"name": "Pod 总数", "value": pods.Total, "unit": "个"},
			map[string]any{"name": "Running", "value": pods.Running, "unit": "个"},
			map[string]any{"name": "Pending", "value": pods.Pending, "unit": "个"},
			map[string]any{"name": "容器就绪", "value": fmt.Sprintf("%d/%d", pods.Ready, pods.Ready+pods.NotReady), "unit": "个"},
			map[string]any{"name": "容器重启总数", "value": restarts.TotalRestarts, "unit": "次"},
			map[string]any{"name": "有重启的 Pod", "value": restarts.TotalPodsWithRestarts, "unit": "个"},
			map[string]any{"name": "单 Pod 最大重启", "value": restarts.MaxPerPod, "unit": "次", "source": restarts.Source},
		)
		if pods.Pending > 0 || pods.NotReady > 0 || pods.Failed > 0 {
			k8sSection.Status = "partial"
		}
	}
	doc.Sections = append(doc.Sections, k8sSection)

	// —— 网络与存储：未接入来源，显式写入正文 ——
	doc.Sections = append(doc.Sections,
		inspectionSection{Key: "network", Title: "网络", Status: "not_connected", Source: "未接入", QueryWindow: window,
			Gaps: []string{"未接入网络吞吐/错误/丢包/时延指标来源；报告中不出现 0 或健康结论"}},
		inspectionSection{Key: "storage", Title: "存储", Status: "not_connected", Source: "未接入", QueryWindow: window,
			Gaps: []string{"未接入存储使用率/容量/IO 时延指标来源；报告中不出现 0 或健康结论"}},
	)

	// —— 数据质量 ——
	qualitySection := inspectionSection{Key: "data_quality", Title: "数据质量", Status: "healthy", Source: "query-api 聚合元数据", QueryWindow: window}
	for _, section := range doc.Sections {
		if section.Status == "not_connected" || section.Status == "failed" || section.Status == "partial" {
			qualitySection.Facts = append(qualitySection.Facts, map[string]any{"name": section.Title + " 状态", "value": section.Status, "unit": "状态"})
		}
	}
	if len(qualitySection.Facts) == 0 {
		qualitySection.Facts = append(qualitySection.Facts, map[string]any{"name": "覆盖状态", "value": "全部来源可用", "unit": "状态"})
	} else {
		qualitySection.Status = "partial"
	}
	doc.Sections = append(doc.Sections, qualitySection)

	doc.Quality = worstStatus(doc.Sections)
	// 摘要必须来自真实聚合值，不得写死 0 或把事实数当成事实本身。
	alertCount := 0
	for _, fact := range alertSection.Facts {
		if name, _ := fact["name"].(string); name == "窗口内告警事件" {
			if v, ok := fact["value"].(int); ok {
				alertCount = v
			}
		}
	}
	podCount := 0
	for _, fact := range k8sSection.Facts {
		if name, _ := fact["name"].(string); name == "Pod 总数" {
			if v, ok := fact["value"].(int); ok {
				podCount = v
			}
		}
	}
	summary := fmt.Sprintf("集群 %s 巡检（%s 至 %s）：整体 %s；告警 %d 条，Pod %d 个",
		target.Name, from.Format("2006-01-02 15:04"), to.Format("2006-01-02 15:04"), doc.Quality, alertCount, podCount)
	content := renderInspectionMarkdown(doc)

	reportID, err := insertInspectionReport(r.Context(), auth.TenantID, clusterID, template, doc.Quality, summary, content)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "report_write_failed", "detail": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"id":           reportID,
		"report_id":    reportID,
		"verdict":      doc.Quality,
		"summary":      summary,
		"content":      content,
		"document":     doc,
		"generated_at": to.Format(time.RFC3339),
	})
}

func ratioPercent(ratio *float64) any {
	if ratio == nil {
		return "未提供"
	}
	return round2(*ratio * 100)
}

func floatOrText(v *float64) any {
	if v == nil {
		return "未提供"
	}
	return *v
}

var statusRank = map[string]int{"healthy": 0, "partial": 1, "not_connected": 2, "stale": 2, "failed": 3, "unknown": 4}

// worstStatus 区分"部分来源未接入"与"全部来源未接入"：
// 有可用来源但也有缺口时必须报 partial，不得用 not_connected 或 healthy 混同。
func worstStatus(sections []inspectionSection) string {
	if len(sections) == 0 {
		return "unknown"
	}
	usable := 0
	worst := "healthy"
	for _, s := range sections {
		if s.Status == "healthy" || s.Status == "partial" {
			usable++
		}
		if statusRank[s.Status] > statusRank[worst] {
			worst = s.Status
		}
	}
	switch {
	case worst == "failed" || worst == "unknown":
		return worst
	case usable == 0:
		return "not_connected"
	case usable < len(sections):
		return "partial"
	default:
		return worst
	}
}

func renderInspectionMarkdown(doc inspectionReportDoc) string {
	var b strings.Builder
	b.WriteString("# 巡检报告\n\n")
	b.WriteString(fmt.Sprintf("- 集群：%s（%s / %s / %s）\n", doc.Scope["cluster_name"], doc.Scope["cluster_id"], doc.Scope["environment"], doc.Scope["region"]))
	b.WriteString(fmt.Sprintf("- 时间窗：%s → %s\n", doc.TimeWindow["from"], doc.TimeWindow["to"]))
	b.WriteString(fmt.Sprintf("- 查询合同：%s\n", doc.QueryContract))
	b.WriteString(fmt.Sprintf("- 整体状态：%s\n", doc.Quality))
	for key, value := range doc.Versions {
		if value != "" {
			b.WriteString(fmt.Sprintf("- %s：%s\n", key, value))
		}
	}
	b.WriteString("\n")
	if len(doc.Warnings) > 0 {
		b.WriteString("## 数据质量提示\n\n")
		for _, warning := range doc.Warnings {
			b.WriteString(fmt.Sprintf("- %s\n", warning))
		}
		b.WriteString("\n")
	}
	for _, section := range doc.Sections {
		b.WriteString(fmt.Sprintf("## %s（%s）\n\n", section.Title, section.Status))
		b.WriteString(fmt.Sprintf("来源：%s\n\n", section.Source))
		for _, fact := range section.Facts {
			b.WriteString(fmt.Sprintf("- %v：%v %v\n", fact["name"], fact["value"], fact["unit"]))
		}
		for _, gap := range section.Gaps {
			b.WriteString(fmt.Sprintf("- 缺口：%s\n", gap))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func insertInspectionReport(ctx context.Context, tenantID, clusterID, template, verdict, summary, content string) (int64, error) {
	db := store.GetDB()
	if db == nil {
		return 0, fmt.Errorf("persistence unavailable")
	}
	taskID := fmt.Sprintf("inspection-%s-%d", clusterID[:8], time.Now().UTC().Unix())
	fileKey := fmt.Sprintf("reports/%s/inspection-%s.md", tenantID, taskID)
	res, err := db.ExecContext(ctx,
		`INSERT INTO reports (task_id, service_name, report_type, verdict, risk_score, summary, content, file_key, tenant_id, cluster_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		taskID, "cluster", "inspection:"+template, verdict, riskScoreOf(verdict), summary, content, fileKey, tenantID, clusterID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func riskScoreOf(verdict string) float64 {
	switch verdict {
	case "failed":
		return 1.0
	case "not_connected", "stale":
		return 0.6
	case "partial":
		return 0.4
	default:
		return 0.1
	}
}

var _ = sql.ErrNoRows
