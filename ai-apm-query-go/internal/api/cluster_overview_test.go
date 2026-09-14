package api

import (
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

func TestAggregateClusterOverviewUsesExplicitCarrierKinds(t *testing.T) {
	now := time.Date(2026, 9, 10, 10, 32, 0, 0, time.UTC)
	got := aggregateClusterOverview(store.Cluster{ClusterID: "cluster-a", Name: "上海集群", Version: "v1.31.0", Status: "active", LifecycleStatus: "ready", UpdatedAt: now.Add(-30 * time.Second)}, nil, now)
	// active/ready 只是接入状态：必须有新鲜覆盖，但不能被当成健康结论。
	if got.Status != "unknown" || got.Coverage.Covered != 1 {
		t.Fatalf("cluster facts = %#v, want unknown health with covered evidence", got)
	}
	if got.StatusReason == "" || got.RegistrationStatus != "ready" {
		t.Fatalf("cluster must expose reason and registration status separately: %#v", got)
	}
	if len(got.ResourceKinds) != 8 || got.ResourceKinds[0].Kind != "deployment" || got.ResourceKinds[5].Kind != "pod" {
		t.Fatalf("resource kinds = %#v, want canonical carrier kinds", got.ResourceKinds)
	}
	for _, item := range got.ResourceKinds {
		if item.Kind == "workload" || item.Kind == "container" {
			t.Fatalf("legacy aggregate kind leaked: %#v", item)
		}
	}
}

func TestAggregateClusterOverviewMarksStaleEvidenceAndKeepsNADOutOfPrimaryResources(t *testing.T) {
	now := time.Date(2026, 9, 10, 10, 32, 0, 0, time.UTC)
	got := aggregateClusterOverview(store.Cluster{ClusterID: "cluster-a", Status: "degraded", LifecycleStatus: "ready", UpdatedAt: now.Add(-8 * time.Minute)}, []platformIssueInput{{ClusterID: "cluster-a", ResourceUID: "pod:api", RuleID: "pod-not-ready", Severity: "critical", Status: "firing", Title: "Pod 未就绪"}}, now)
	if !got.Meta.Partial || !got.Meta.Stale || got.Coverage.Covered != 0 {
		t.Fatalf("data quality = %#v coverage=%#v, want partial/stale and uncovered", got.Meta, got.Coverage)
	}
	if len(got.Issues) != 1 || got.Issues[0].ResourceUID != "pod:api" {
		t.Fatalf("issues = %#v, want pod evidence", got.Issues)
	}
	for _, item := range got.ResourceKinds {
		if item.Kind == "nad" || item.Kind == "disk" {
			t.Fatalf("NAD/disk must not be primary carrier rows: %#v", got.ResourceKinds)
		}
	}
}

// TestClusterOverviewSharesPlatformHealthProjection 锁定现场事实矛盾：
// 同一陈旧集群在平台列表显示健康、在集群详情显示降级。
func TestClusterOverviewSharesPlatformHealthProjection(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	cluster := store.Cluster{ClusterID: "stale-cluster", TenantID: "t", Name: "kind-02", Status: "healthy", LifecycleStatus: "ready", UpdatedAt: now.Add(-7 * time.Minute)}

	detail := aggregateClusterOverview(cluster, nil, now)
	overview := aggregatePlatformOverview([]store.Cluster{cluster}, "", nil, now)
	if detail.Status == "healthy" {
		t.Fatalf("stale cluster must not be healthy in detail: %#v", detail)
	}
	if overview.ClusterStates[detail.Status] != 1 {
		t.Fatalf("platform matrix (%#v) and cluster detail (%s) disagree", overview.ClusterStates, detail.Status)
	}
	if detail.ClusterID != cluster.ClusterID {
		t.Fatalf("detail cluster id = %q", detail.ClusterID)
	}
	// Regression: an active registration (no observation) must never be reported healthy.
	active := store.Cluster{ClusterID: "active-cluster", TenantID: "t", Status: "active", LifecycleStatus: "ready", UpdatedAt: now.Add(-10 * time.Second)}
	if got := aggregateClusterOverview(active, nil, now); got.Status != "unknown" {
		t.Fatalf("registration-only cluster must be unknown, got %#v", got)
	}
}
