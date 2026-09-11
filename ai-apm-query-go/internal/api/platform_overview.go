package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

type platformCoverage struct {
	Covered  int     `json:"covered"`
	Expected int     `json:"expected"`
	Ratio    float64 `json:"ratio"`
}

type platformIssueInput struct {
	TenantID    string
	ClusterID   string
	ResourceUID string
	RuleID      string
	Severity    string
	Status      string
	Title       string
	ObservedAt  time.Time
}

type platformIssue struct {
	ClusterID   string    `json:"cluster_id"`
	ResourceUID string    `json:"resource_uid"`
	RuleID      string    `json:"rule_id"`
	Severity    string    `json:"severity"`
	Status      string    `json:"status"`
	Title       string    `json:"title"`
	ObservedAt  time.Time `json:"observed_at"`
}

type platformCapabilitySummary struct {
	Healthy int      `json:"healthy"`
	Total   int      `json:"total"`
	Issues  []string `json:"issues,omitempty"`
}

type platformOverviewResponse struct {
	ActiveClusterID        string                    `json:"active_cluster_id,omitempty"`
	ActiveCriticalIssues   int                       `json:"active_critical_issues"`
	ManagedClusters        int                       `json:"managed_clusters"`
	AffectedClusters       int                       `json:"affected_clusters"`
	UnknownOrStaleClusters int                       `json:"unknown_or_stale_clusters"`
	ClusterStates          map[string]int            `json:"cluster_states"`
	Coverage               platformCoverage          `json:"coverage"`
	FreshestAt             time.Time                 `json:"freshest_at"`
	OldestValidAt          time.Time                 `json:"oldest_valid_at"`
	HighestPriority        *platformIssue            `json:"highest_priority,omitempty"`
	CapabilitySummary      platformCapabilitySummary `json:"capability_summary"`
	Meta                   ResourceReadMeta          `json:"meta"`
}

func activePlatformIssue(input platformIssueInput) bool {
	return strings.EqualFold(strings.TrimSpace(input.Severity), "critical") &&
		!strings.EqualFold(strings.TrimSpace(input.Status), "resolved") &&
		strings.TrimSpace(input.ClusterID) != ""
}

func issueKey(input platformIssueInput) string {
	return strings.Join([]string{input.TenantID, input.ClusterID, input.ResourceUID, input.RuleID}, "|")
}

func aggregatePlatformOverview(clusters []store.Cluster, activeClusterID string, issues []platformIssueInput, now time.Time) platformOverviewResponse {
	return aggregatePlatformOverviewWithCapabilities(clusters, activeClusterID, issues, nil, now)
}

// aggregatePlatformOverviewWithCapabilities 在纯聚合之上注入组件探测结果，
// 使平台能力摘要与系统管理页同源；capabilityRows 为 nil 时返回 0/0，
// 由前端显示“未获得组件状态”，而不是硬编码 1/1。
func aggregatePlatformOverviewWithCapabilities(clusters []store.Cluster, activeClusterID string, issues []platformIssueInput, capabilityRows []systemComponentResultView, now time.Time) platformOverviewResponse {
	result := platformOverviewResponse{
		ActiveClusterID: activeClusterID,
		ManagedClusters: len(clusters),
		ClusterStates:   map[string]int{"healthy": 0, "degraded": 0, "critical": 0, "unknown": 0},
		FreshestAt:      time.Time{},
		OldestValidAt:   time.Time{},
		Meta:            ResourceReadMeta{GeneratedAt: now},
	}
	covered := 0
	affected := map[string]struct{}{}
	authorizedTenants := make(map[string]string, len(clusters))
	for _, cluster := range clusters {
		authorizedTenants[cluster.ClusterID] = cluster.TenantID
		state := projectClusterOperationalState(cluster, now)
		result.ClusterStates[state.Health]++
		if state.Covered {
			covered++
		}
		if state.Health == "unknown" || state.Stale {
			result.UnknownOrStaleClusters++
		}
		if cluster.UpdatedAt.IsZero() || state.Stale {
			continue
		}
		if result.FreshestAt.IsZero() || cluster.UpdatedAt.After(result.FreshestAt) {
			result.FreshestAt = cluster.UpdatedAt
		}
		if result.OldestValidAt.IsZero() || cluster.UpdatedAt.Before(result.OldestValidAt) {
			result.OldestValidAt = cluster.UpdatedAt
		}
	}
	result.Coverage = platformCoverage{Covered: covered, Expected: len(clusters)}
	if len(clusters) > 0 {
		result.Coverage.Ratio = float64(covered) / float64(len(clusters))
	}
	seen := map[string]struct{}{}
	for _, input := range issues {
		if !activePlatformIssue(input) {
			continue
		}
		clusterTenant, authorized := authorizedTenants[input.ClusterID]
		if !authorized || (input.TenantID != "" && clusterTenant != "" && input.TenantID != clusterTenant) {
			continue
		}
		key := issueKey(input)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result.ActiveCriticalIssues++
		affected[input.ClusterID] = struct{}{}
		candidate := &platformIssue{ClusterID: input.ClusterID, ResourceUID: input.ResourceUID, RuleID: input.RuleID, Severity: input.Severity, Status: input.Status, Title: input.Title, ObservedAt: input.ObservedAt}
		if result.HighestPriority == nil || candidate.ObservedAt.Before(result.HighestPriority.ObservedAt) || result.HighestPriority.ObservedAt.IsZero() {
			result.HighestPriority = candidate
		}
	}
	result.AffectedClusters = len(affected)
	result.Meta.Partial = result.Coverage.Covered < result.Coverage.Expected
	result.Meta.Stale = result.UnknownOrStaleClusters > 0
	if result.Meta.Partial {
		result.Meta.WarningCodes = append(result.Meta.WarningCodes, "CLUSTER_DATA_PARTIAL")
	}
	if result.Meta.Stale {
		result.Meta.WarningCodes = append(result.Meta.WarningCodes, "CLUSTER_DATA_STALE")
	}
	result.CapabilitySummary = summarizePlatformCapabilities(capabilityRows)
	return result
}

func (h *Handler) platformIssueInputs(r *http.Request, tenantID string, clusters []store.Cluster) []platformIssueInput {
	authorized := make(map[string]struct{}, len(clusters))
	for _, cluster := range clusters {
		authorized[cluster.ClusterID] = struct{}{}
	}
	alertEventsMu.RLock()
	defer alertEventsMu.RUnlock()
	inputs := make([]platformIssueInput, 0)
	for _, event := range alertEvents {
		if event.TenantID != "" && event.TenantID != tenantID {
			continue
		}
		if _, ok := authorized[event.Cluster]; !ok {
			continue
		}
		observedAt, _ := time.Parse(time.RFC3339Nano, event.LastTimestamp)
		inputs = append(inputs, platformIssueInput{TenantID: tenantID, ClusterID: event.Cluster, ResourceUID: event.Object, RuleID: event.RuleID, Severity: event.Severity, Status: event.Status, Title: event.Message, ObservedAt: observedAt})
	}
	return inputs
}

func (h *Handler) PlatformOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	auth, ok := requestAuthorizationContext(r)
	if !ok {
		var err error
		auth, err = RequestAuthorizationContext(r)
		if err != nil {
			respondAuthorizationError(w, err)
			return
		}
	}
	if auth.TenantID == "" {
		respondJSON(w, http.StatusConflict, map[string]string{"error": "SCOPE_SELECTION_REQUIRED"})
		return
	}
	clusters, err := (&store.ClusterDAO{}).ListForTenant(auth.TenantID)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "PLATFORM_OVERVIEW_UNAVAILABLE"})
		return
	}
	active := make([]store.Cluster, 0, len(clusters))
	for _, cluster := range clusters {
		if cluster.ClusterID == "" || (cluster.LifecycleStatus != "" && cluster.LifecycleStatus != "active" && cluster.LifecycleStatus != "ready") {
			continue
		}
		active = append(active, cluster)
	}
	respondJSON(w, http.StatusOK, aggregatePlatformOverviewWithCapabilities(active, auth.ActiveClusterID, h.platformIssueInputs(r, auth.TenantID, active), platformCapabilitySnapshot(), time.Now().UTC()))
}

func (h *Handler) PlatformClusters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	auth, ok := requestAuthorizationContext(r)
	if !ok {
		var err error
		auth, err = RequestAuthorizationContext(r)
		if err != nil {
			respondAuthorizationError(w, err)
			return
		}
	}
	if auth.TenantID == "" {
		respondJSON(w, http.StatusConflict, map[string]string{"error": "SCOPE_SELECTION_REQUIRED"})
		return
	}
	clusters, err := (&store.ClusterDAO{}).ListForTenant(auth.TenantID)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "PLATFORM_CLUSTERS_UNAVAILABLE"})
		return
	}
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	now := time.Now().UTC()
	states := make(map[string]clusterOperationalState, len(clusters))
	items := make([]store.Cluster, 0, len(clusters))
	for _, cluster := range clusters {
		state := projectClusterOperationalState(cluster, now)
		states[cluster.ClusterID] = state
		if statusFilter != "" && statusFilter != state.Health {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(cluster.Name), query) && !strings.Contains(strings.ToLower(cluster.ClusterID), query) {
			continue
		}
		items = append(items, cluster)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	total := len(items)
	if len(items) > limit {
		items = items[:limit]
	}
	result := make([]map[string]interface{}, 0, len(items))
	for _, cluster := range items {
		state := states[cluster.ClusterID]
		result = append(result, map[string]interface{}{
			"cluster_id": cluster.ClusterID,
			"name":       cluster.Name,
			// status 保持“观测健康”含义；注册状态单独输出，二者不得互相替代。
			"status":              state.Health,
			"registration_status": state.RegistrationStatus,
			"status_reason":       state.Reason,
			"stale":               state.Stale,
			"covered":             state.Covered,
			"updated_at":          cluster.UpdatedAt,
		})
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"clusters": result, "count": len(result), "total": total, "meta": resourceReadMeta(false, nil)})
}
