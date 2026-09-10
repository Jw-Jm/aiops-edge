package api

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

func TestPlatformOverviewIgnoresActiveClusterAndUsesAuthorizedSet(t *testing.T) {
	now := time.Date(2026, 9, 10, 10, 32, 0, 0, time.UTC)
	clusters := []store.Cluster{
		{ClusterID: "cluster-a", TenantID: "tenant-1", Name: "上海集群", Status: "active", LifecycleStatus: "ready", UpdatedAt: now.Add(-2 * time.Minute)},
		{ClusterID: "cluster-b", TenantID: "tenant-1", Name: "北京集群", Status: "degraded", LifecycleStatus: "ready", UpdatedAt: now.Add(-7 * time.Minute)},
	}
	issues := []platformIssueInput{
		{TenantID: "tenant-1", ClusterID: "cluster-b", ResourceUID: "pod:order-api-1", RuleID: "pod-not-ready", Severity: "critical", Status: "firing", Title: "Pod 未就绪", ObservedAt: now.Add(-30 * time.Second)},
		{TenantID: "tenant-1", ClusterID: "cluster-b", ResourceUID: "pod:order-api-1", RuleID: "pod-not-ready", Severity: "critical", Status: "acknowledged", Title: "Pod 未就绪", ObservedAt: now.Add(-20 * time.Second)},
		{TenantID: "tenant-2", ClusterID: "cluster-x", ResourceUID: "pod:other", RuleID: "other", Severity: "critical", Status: "firing", Title: "不得泄漏", ObservedAt: now},
	}

	got := aggregatePlatformOverview(clusters, "cluster-a", issues, now)
	if got.ManagedClusters != 2 {
		t.Fatalf("managed clusters = %d, want all supplied authorized projection rows", got.ManagedClusters)
	}
	if got.AffectedClusters != 1 || got.ActiveCriticalIssues != 1 {
		t.Fatalf("critical aggregation = affected=%d issues=%d, want 1/1", got.AffectedClusters, got.ActiveCriticalIssues)
	}
	if got.HighestPriority == nil || got.HighestPriority.ClusterID != "cluster-b" {
		t.Fatalf("highest priority = %#v, want cluster-b", got.HighestPriority)
	}
	if got.ClusterStates["degraded"] != 1 || got.ClusterStates["healthy"] != 1 {
		t.Fatalf("cluster states = %#v, want healthy=1 degraded=1", got.ClusterStates)
	}
	if got.ActiveClusterID != "cluster-a" {
		t.Fatalf("active cluster should be context only, got %q", got.ActiveClusterID)
	}
	raw, _ := json.Marshal(got)
	if string(raw) == "" || bytes.Contains(raw, []byte(`"platform_status"`)) {
		t.Fatalf("platform response must not expose a synthetic platform_status: %s", raw)
	}
}

func TestAggregatePlatformOverviewReportsCoverageAndStaleness(t *testing.T) {
	now := time.Date(2026, 9, 10, 10, 32, 0, 0, time.UTC)
	clusters := []store.Cluster{
		{ClusterID: "a", TenantID: "t", Status: "active", LifecycleStatus: "ready", UpdatedAt: now.Add(-10 * time.Second)},
		{ClusterID: "b", TenantID: "t", Status: "down", LifecycleStatus: "ready", UpdatedAt: now.Add(-9 * time.Minute)},
	}
	got := aggregatePlatformOverview(clusters, "a", nil, now)
	if got.Coverage.Expected != 2 || got.Coverage.Covered != 1 || got.Coverage.Ratio != 0.5 {
		t.Fatalf("coverage = %#v, want 1/2/0.5", got.Coverage)
	}
	if got.UnknownOrStaleClusters != 1 || got.ClusterStates["critical"] != 1 {
		t.Fatalf("quality/status = unknown_or_stale=%d states=%#v", got.UnknownOrStaleClusters, got.ClusterStates)
	}
	if !got.Meta.Partial || !got.Meta.Stale {
		t.Fatalf("meta = %#v, want partial+stale", got.Meta)
	}
}
