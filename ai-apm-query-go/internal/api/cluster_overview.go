package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

type resourceKindSummary struct {
	Kind           string `json:"kind"`
	Total          int    `json:"total"`
	Abnormal       int    `json:"abnormal"`
	UnknownOrStale int    `json:"unknown_or_stale"`
}

type foundationFact struct {
	Kind                  string `json:"kind"`
	Status                string `json:"status"`
	Reason                string `json:"reason"`
	AffectedResourceCount int    `json:"affected_resource_count"`
}

type kubeVirtSummary struct {
	VM              int `json:"vm"`
	VMI             int `json:"vmi"`
	NotReady        int `json:"not_ready"`
	Migrating       int `json:"migrating"`
	FailedMigration int `json:"failed_migration"`
	StorageAffected int `json:"storage_affected"`
	NetworkAffected int `json:"network_affected"`
}

type clusterOverviewResponse struct {
	ClusterID     string                `json:"cluster_id"`
	Name          string                `json:"name"`
	Status        string                `json:"status"`
	StatusReasons []string              `json:"status_reasons"`
	Version       string                `json:"version,omitempty"`
	LastSyncAt    time.Time             `json:"last_sync_at"`
	Coverage      platformCoverage      `json:"coverage"`
	Issues        []platformIssue       `json:"issues"`
	ResourceKinds []resourceKindSummary `json:"resource_kinds"`
	KubeVirt      kubeVirtSummary       `json:"kubevirt"`
	Foundation    []foundationFact      `json:"foundation"`
	Meta          ResourceReadMeta      `json:"meta"`
}

var canonicalClusterResourceKinds = []string{
	"deployment", "statefulset", "daemonset", "job", "cronjob", "pod", "k8s_service", "ingress",
}

func defaultResourceKindSummaries() []resourceKindSummary {
	items := make([]resourceKindSummary, 0, len(canonicalClusterResourceKinds))
	for _, kind := range canonicalClusterResourceKinds {
		items = append(items, resourceKindSummary{Kind: kind})
	}
	return items
}

func foundationStatusReason(cluster store.Cluster, state string, now time.Time) (string, string, int) {
	if state == "unknown" {
		return "unknown", "集群状态证据缺失", 0
	}
	if cluster.UpdatedAt.IsZero() {
		return "unknown", "暂无最新同步时间", 0
	}
	if now.Sub(cluster.UpdatedAt) > 5*time.Minute {
		return "degraded", "集群数据陈旧", 1
	}
	if state == "critical" {
		return "critical", "集群连接或控制面状态异常", 0
	}
	return "healthy", "API Server 与集群注册状态可读", 0
}

func aggregateClusterOverview(cluster store.Cluster, issues []platformIssueInput, now time.Time) clusterOverviewResponse {
	state := healthStateForCluster(cluster)
	coverage := platformCoverage{Expected: 1}
	if clusterCovered(cluster, now) {
		coverage.Covered = 1
		coverage.Ratio = 1
	}
	status, foundationReason, affected := foundationStatusReason(cluster, state, now)
	if status != state && state != "unknown" {
		state = status
	}
	result := clusterOverviewResponse{
		ClusterID:     cluster.ClusterID,
		Name:          cluster.Name,
		Status:        state,
		StatusReasons: []string{foundationReason},
		Version:       cluster.Version,
		LastSyncAt:    cluster.UpdatedAt,
		Coverage:      coverage,
		Issues:        make([]platformIssue, 0),
		ResourceKinds: defaultResourceKindSummaries(),
		Foundation: []foundationFact{
			{Kind: "control_plane", Status: status, Reason: foundationReason, AffectedResourceCount: affected},
			{Kind: "nodes_hosts", Status: "unknown", Reason: "暂无节点与物理机事实", AffectedResourceCount: 0},
			{Kind: "network", Status: "unknown", Reason: "暂无网络面证据", AffectedResourceCount: 0},
			{Kind: "storage", Status: "unknown", Reason: "暂无存储面证据", AffectedResourceCount: 0},
			{Kind: "kubevirt", Status: "unknown", Reason: "暂无 KubeVirt 运行面证据", AffectedResourceCount: 0},
		},
		Meta: ResourceReadMeta{GeneratedAt: now, Partial: coverage.Covered < coverage.Expected, Stale: !clusterCovered(cluster, now)},
	}
	if result.Meta.Partial {
		result.Meta.WarningCodes = append(result.Meta.WarningCodes, "CLUSTER_DATA_PARTIAL")
	}
	if result.Meta.Stale {
		result.Meta.WarningCodes = append(result.Meta.WarningCodes, "CLUSTER_DATA_STALE")
	}
	seen := map[string]struct{}{}
	for _, input := range issues {
		if !activePlatformIssue(input) || input.ClusterID != cluster.ClusterID {
			continue
		}
		key := issueKey(input)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result.Issues = append(result.Issues, platformIssue{ClusterID: input.ClusterID, ResourceUID: input.ResourceUID, RuleID: input.RuleID, Severity: input.Severity, Status: input.Status, Title: input.Title, ObservedAt: input.ObservedAt})
	}
	return result
}

func clusterIDFromOverviewPath(path string) string {
	const prefix = "/api/v1/clusters/"
	value := strings.TrimPrefix(path, prefix)
	value = strings.TrimSuffix(value, "/overview")
	return strings.Trim(value, "/")
}

func (h *Handler) ClusterOverview(w http.ResponseWriter, r *http.Request) {
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
	clusterID := clusterIDFromOverviewPath(r.URL.Path)
	if auth.TenantID == "" || clusterID == "" {
		respondJSON(w, http.StatusConflict, map[string]string{"error": "SCOPE_SELECTION_REQUIRED"})
		return
	}
	clusters, err := (&store.ClusterDAO{}).ListForTenant(auth.TenantID)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "CLUSTER_OVERVIEW_UNAVAILABLE"})
		return
	}
	var target *store.Cluster
	for i := range clusters {
		if clusters[i].ClusterID == clusterID && (clusters[i].LifecycleStatus == "" || clusters[i].LifecycleStatus == "active" || clusters[i].LifecycleStatus == "ready") {
			target = &clusters[i]
			break
		}
	}
	if target == nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "CLUSTER_NOT_FOUND"})
		return
	}
	respondJSON(w, http.StatusOK, aggregateClusterOverview(*target, h.platformIssueInputs(r, auth.TenantID, []store.Cluster{*target}), time.Now().UTC()))
}
