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
	ClusterID          string                `json:"cluster_id"`
	Name               string                `json:"name"`
	// 环境与地域：集群页必须永久显示（设计规范 §6.3），为空时前端显示未提供。
	Environment        string                `json:"environment,omitempty"`
	Region             string                `json:"region,omitempty"`
	Status             string                `json:"status"`
	StatusReason       string                `json:"status_reason"`
	StatusReasons      []string              `json:"status_reasons"`
	RegistrationStatus string                `json:"registration_status"`
	Stale              bool                  `json:"stale"`
	Version            string                `json:"version,omitempty"`
	LastSyncAt         time.Time             `json:"last_sync_at"`
	Coverage           platformCoverage      `json:"coverage"`
	Issues             []platformIssue       `json:"issues"`
	ResourceKinds      []resourceKindSummary `json:"resource_kinds"`
	KubeVirt           kubeVirtSummary       `json:"kubevirt"`
	Foundation         []foundationFact      `json:"foundation"`
	Meta               ResourceReadMeta      `json:"meta"`
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

func aggregateClusterOverview(cluster store.Cluster, issues []platformIssueInput, now time.Time) clusterOverviewResponse {
	// 与平台总览 / 平台集群列表共用同一健康投影，保证同一集群在任何入口
	// 得到完全相同的 status 与 status_reason。
	state := projectClusterOperationalState(cluster, now)
	coverage := platformCoverage{Expected: 1}
	if state.Covered {
		coverage.Covered = 1
		coverage.Ratio = 1
	}
	result := clusterOverviewResponse{
		ClusterID:          cluster.ClusterID,
		Name:               cluster.Name,
		Environment:        cluster.Environment,
		Region:             cluster.Region,
		Status:             state.Health,
		StatusReason:       state.Reason,
		StatusReasons:      []string{state.Reason},
		RegistrationStatus: state.RegistrationStatus,
		Stale:              state.Stale,
		Version:            cluster.Version,
		LastSyncAt:         cluster.UpdatedAt,
		Coverage:           coverage,
		Issues:             make([]platformIssue, 0),
		ResourceKinds:      defaultResourceKindSummaries(),
		Foundation: []foundationFact{
			// 控制面事实与集群健康同源；未采集的维度保持 unknown，不假定健康。
			{Kind: "control_plane", Status: state.Health, Reason: state.Reason, AffectedResourceCount: 0},
			{Kind: "nodes_hosts", Status: "unknown", Reason: "暂无节点与物理机事实", AffectedResourceCount: 0},
			{Kind: "network", Status: "unknown", Reason: "暂无网络面证据", AffectedResourceCount: 0},
			{Kind: "storage", Status: "unknown", Reason: "暂无存储面证据", AffectedResourceCount: 0},
			{Kind: "kubevirt", Status: "unknown", Reason: "暂无 KubeVirt 运行面证据", AffectedResourceCount: 0},
		},
		Meta: ResourceReadMeta{GeneratedAt: now, Partial: !state.Covered, Stale: state.Stale},
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
