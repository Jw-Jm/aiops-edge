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
	// 新健康语义：active/ready 只是接入状态，不能产生 healthy 结论；
	// 陈旧证据（cluster-b 已 7 分钟未更新）也必须是 unknown 而不是 degraded。
	if got.ClusterStates["healthy"] != 0 || got.ClusterStates["degraded"] != 0 || got.ClusterStates["unknown"] != 2 {
		t.Fatalf("cluster states = %#v, want unknown=2 and never healthy/degraded", got.ClusterStates)
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
	// a 只有接入状态 → unknown（有新鲜覆盖）；b 证据陈旧 → unknown 且未覆盖。
	if got.UnknownOrStaleClusters != 2 || got.ClusterStates["critical"] != 0 || got.ClusterStates["unknown"] != 2 {
		t.Fatalf("quality/status = unknown_or_stale=%d states=%#v", got.UnknownOrStaleClusters, got.ClusterStates)
	}
	if !got.Meta.Partial || !got.Meta.Stale {
		t.Fatalf("meta = %#v, want partial+stale", got.Meta)
	}
}

// TestAggregatePlatformOverviewNeverTurnsStaleOrRegistrationIntoHealthy 锁定现场事实矛盾：
// 同一陈旧集群不能在平台显示健康；注册 active/ready 也不能单独产生健康。
func TestAggregatePlatformOverviewNeverTurnsStaleOrRegistrationIntoHealthy(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	clusters := []store.Cluster{
		{ClusterID: "regression-kind-cluster", TenantID: "t", Name: "kind-02", Status: "active", LifecycleStatus: "ready", UpdatedAt: now.Add(-6 * time.Minute)},
		{ClusterID: "fresh-registration", TenantID: "t", Name: "orbstack", Status: "active", LifecycleStatus: "ready", UpdatedAt: now.Add(-30 * time.Second)},
	}
	got := aggregatePlatformOverview(clusters, "", nil, now)
	if got.ClusterStates["healthy"] != 0 {
		t.Fatalf("stale/registration-only clusters must never be healthy: %#v", got.ClusterStates)
	}
	if got.UnknownOrStaleClusters != 2 {
		t.Fatalf("unknown_or_stale = %d, want 2", got.UnknownOrStaleClusters)
	}
	if got.Coverage.Covered != 1 || got.Coverage.Expected != 2 {
		t.Fatalf("stale cluster must not count as covered: %#v", got.Coverage)
	}
	if got.FreshestAt.IsZero() || got.OldestValidAt.IsZero() {
		t.Fatalf("data freshness must come from real evidence: %#v", got)
	}
}

// TestPlatformCapabilitiesNeverHardcodeOneOfOne 验证平台能力摘要不再硬编码 1/1。
func TestPlatformCapabilitiesNeverHardcodeOneOfOne(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	clusters := []store.Cluster{{ClusterID: "a", TenantID: "t", Status: "healthy", LifecycleStatus: "ready", UpdatedAt: now.Add(-10 * time.Second)}}
	withRows := aggregatePlatformOverviewWithCapabilities(clusters, "", nil, []systemComponentResultView{
		{Name: "query-api", Status: "ok"},
		{Name: "ingest", Status: "down"},
	}, now)
	if withRows.CapabilitySummary.Total != 2 || withRows.CapabilitySummary.Healthy != 1 {
		t.Fatalf("capability summary must come from probe rows: %#v", withRows.CapabilitySummary)
	}
	withoutRows := aggregatePlatformOverview(clusters, "", nil, now)
	if withoutRows.CapabilitySummary.Total != 0 || withoutRows.CapabilitySummary.Healthy != 0 {
		t.Fatalf("without probe rows the summary must be empty, not 1/1: %#v", withoutRows.CapabilitySummary)
	}
}
