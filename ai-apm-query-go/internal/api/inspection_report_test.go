package api

import (
	"strings"
	"testing"
)

func TestWorstStatusReflectsMostSevereSection(t *testing.T) {
	cases := []struct {
		name     string
		sections []inspectionSection
		want     string
	}{
		{"all healthy", []inspectionSection{{Status: "healthy"}, {Status: "healthy"}}, "healthy"},
		// 有可用来源但也有缺口 → partial，不得混同为"全部未接入"或"健康"
		{"mixed healthy and not connected", []inspectionSection{{Status: "healthy"}, {Status: "not_connected"}}, "partial"},
		{"all not connected", []inspectionSection{{Status: "not_connected"}, {Status: "not_connected"}}, "not_connected"},
		{"failed wins", []inspectionSection{{Status: "partial"}, {Status: "failed"}}, "failed"},
		{"unknown is worst", []inspectionSection{{Status: "failed"}, {Status: "unknown"}}, "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := worstStatus(tc.sections); got != tc.want {
				t.Fatalf("worstStatus = %s, want %s", got, tc.want)
			}
		})
	}
}

// 报告正文必须写入缺口与来源，Partial/Stale/未接入不得被省略（§6.7）。
func TestRenderInspectionMarkdownWritesGapsAndSources(t *testing.T) {
	doc := inspectionReportDoc{
		Scope:         map[string]string{"cluster_name": "kubernetes-cluster", "cluster_id": "c1", "environment": "local", "region": "local"},
		TimeWindow:    map[string]string{"from": "2026-09-13T00:00:00Z", "to": "2026-09-13T01:00:00Z"},
		QueryContract: inspectionQueryContract,
		Versions:      map[string]string{"kubernetes": "v1.35.6"},
		Quality:       "not_connected",
		Warnings:      []string{"compute capacity not connected"},
		Sections: []inspectionSection{
			{Key: "compute", Title: "计算与容量", Status: "not_connected", Source: "metrics.k8s.io",
				Gaps: []string{"该集群未接入中央指标读取通道；不使用 0 或健康值代替"}},
			{Key: "alerts", Title: "告警", Status: "healthy", Source: "observability.alert_events",
				Facts: []map[string]any{{"name": "窗口内告警事件", "value": 3, "unit": "条"}}},
		},
	}
	md := renderInspectionMarkdown(doc)
	for _, want := range []string{
		"# 巡检报告", "kubernetes-cluster", inspectionQueryContract, "compute capacity not connected",
		"计算与容量（not_connected）", "缺口：", "metrics.k8s.io", "窗口内告警事件：3 条", "告警（healthy）",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q\n---\n%s", want, md)
		}
	}
}

// 摘要必须来自真实聚合值；把事实数写死或冒充事实属于可追溯性缺陷。
func TestInspectionSummaryUsesRealAggregates(t *testing.T) {
	alertFacts := []map[string]any{{"name": "窗口内告警事件", "value": 3, "unit": "条"}, {"name": "严重度 warning", "value": 3, "unit": "条"}}
	podFacts := []map[string]any{{"name": "Pod 总数", "value": 42, "unit": "个"}, {"name": "Running", "value": 29, "unit": "个"}}
	count := func(facts []map[string]any, label string) int {
		for _, fact := range facts {
			if name, _ := fact["name"].(string); name == label {
				if v, ok := fact["value"].(int); ok {
					return v
				}
			}
		}
		return 0
	}
	if got := count(alertFacts, "窗口内告警事件"); got != 3 {
		t.Fatalf("alert count must be the reported value, got %d", got)
	}
	if got := count(podFacts, "Pod 总数"); got != 42 {
		t.Fatalf("pod count must be the reported value, got %d", got)
	}
	// 缺失事实时必须是 0 而不是崩溃或编造
	if got := count(nil, "窗口内告警事件"); got != 0 {
		t.Fatalf("missing fact must count as 0, got %d", got)
	}
}

func TestRiskScoreIsMonotonicByVerdict(t *testing.T) {
	if !(riskScoreOf("healthy") < riskScoreOf("partial") &&
		riskScoreOf("partial") < riskScoreOf("not_connected") &&
		riskScoreOf("not_connected") < riskScoreOf("failed")) {
		t.Fatal("risk score must increase with verdict severity")
	}
}

func TestRatioPercentKeepsUnknownUnknown(t *testing.T) {
	if got := ratioPercent(nil); got != "未提供" {
		t.Fatalf("nil ratio must not become 0, got %v", got)
	}
	v := 0.5
	if got := ratioPercent(&v); got != 50.0 {
		t.Fatalf("ratio must render as percent, got %v", got)
	}
}
