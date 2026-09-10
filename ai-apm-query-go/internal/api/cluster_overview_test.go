package api

import (
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

func TestAggregateClusterOverviewUsesExplicitCarrierKinds(t *testing.T) {
	now := time.Date(2026, 9, 10, 10, 32, 0, 0, time.UTC)
	got := aggregateClusterOverview(store.Cluster{ClusterID: "cluster-a", Name: "上海集群", Version: "v1.31.0", Status: "active", LifecycleStatus: "ready", UpdatedAt: now.Add(-30 * time.Second)}, nil, now)
	if got.Status != "healthy" || got.Coverage.Covered != 1 {
		t.Fatalf("cluster facts = %#v, want healthy and covered", got)
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
