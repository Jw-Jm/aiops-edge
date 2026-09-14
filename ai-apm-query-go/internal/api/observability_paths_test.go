package api

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/query"
)

func TestClassifyPathIDMapsCloudPlatformInfrastructurePaths(t *testing.T) {
	cases := []struct {
		name    string
		event   query.KubernetesEvent
		want    string
		comment string
	}{
		{"image pull", query.KubernetesEvent{Reason: "Failed", Message: "Failed to pull image \"repo/app:1\": ErrImagePull"}, "image-pull", ""},
		{"volume mount", query.KubernetesEvent{Reason: "FailedMount", Message: "MountVolume.SetUp failed for volume \"data\""}, "volume-mount", ""},
		{"pod network", query.KubernetesEvent{Reason: "FailedCreatePodSandBox", Message: "failed to create pod sandbox: CNI plugin failed"}, "pod-network", ""},
		{"dns", query.KubernetesEvent{Reason: "Sync", Message: "no such host for CoreDNS lookup"}, "dns", ""},
		{"vm boot", query.KubernetesEvent{Kind: "VirtualMachine", Reason: "Failed", Message: "virt-launcher failed to start"}, "vm-boot", ""},
		{"k8s control", query.KubernetesEvent{Reason: "NodeNotReady", Message: "kubelet stopped posting status"}, "k8s-control", ""},
		{"compute dependency", query.KubernetesEvent{Reason: "Evicted", Message: "Insufficient memory"}, "compute-dependency", "具体分类（计算依赖）优先于宽泛分类（k8s-control），保证确定性"},
		{"control plane", query.KubernetesEvent{Reason: "Unhealthy", Message: "etcd health check failed"}, "control-plane", ""},
		{"storage", query.KubernetesEvent{Kind: "PersistentVolumeClaim", Reason: "ProvisioningFailed", Message: "waiting for a volume to be created"}, "storage-dependency", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyPathID(tc.event)
			if got != tc.want {
				t.Fatalf("classifyPathID(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestClassifyPathIDNeverGuessesUnrelatedEvents(t *testing.T) {
	// 与云平台路径无关的事件必须返回空串，不能强行归入某个路径。
	unrelated := query.KubernetesEvent{Kind: "ConfigMap", Reason: "Created", Message: "configmap created by controller"}
	if got := classifyPathID(unrelated); got != "" {
		t.Fatalf("expected no classification for unrelated event, got %q", got)
	}
}

func TestBuildPathRowsAggregatesRealFactsAndListsGaps(t *testing.T) {
	events := []query.KubernetesEvent{
		{Reason: "Failed", Message: "ErrImagePull", Kind: "Pod", Name: "a", InvolvedObject: "Pod/a", Timestamp: "2026-09-13T06:00:00Z", EventID: "e1"},
		{Reason: "Failed", Message: "ErrImagePull", Kind: "Pod", Name: "b", InvolvedObject: "Pod/b", Timestamp: "2026-09-13T06:05:00Z", EventID: "e2"},
		{Reason: "FailedMount", Message: "MountVolume.SetUp failed for volume", Kind: "Pod", Name: "a", InvolvedObject: "Pod/a", Timestamp: "2026-09-13T06:06:00Z", EventID: "e3"},
		{Kind: "ConfigMap", Reason: "Created", Message: "irrelevant", Timestamp: "2026-09-13T06:07:00Z", EventID: "e4"},
	}
	rows, grouped, gaps := buildPathRows(events)

	if len(rows) != 2 {
		t.Fatalf("expected 2 path rows with real facts, got %d", len(rows))
	}
	// 与路径无关的事件不得生成路径行
	for _, row := range rows {
		if row.PathID == "dns" {
			t.Fatal("unrelated events must not fabricate a DNS path")
		}
	}
	imageRow := rows[0]
	if imageRow.PathID != "image-pull" {
		t.Fatalf("expected rows sorted by path_id, got first %q", imageRow.PathID)
	}
	if imageRow.AffectedResources != 2 {
		t.Fatalf("expected 2 affected resources, got %d", imageRow.AffectedResources)
	}
	if imageRow.DurationSeconds != 300 {
		t.Fatalf("expected 300s duration between first and last fact, got %d", imageRow.DurationSeconds)
	}
	if imageRow.Severity < 1 || imageRow.Severity > 4 {
		t.Fatalf("severity out of range: %d", imageRow.Severity)
	}
	if len(grouped["image-pull"]) != 2 {
		t.Fatalf("expected 2 grouped image-pull events, got %d", len(grouped["image-pull"]))
	}
	// 窗口内没有事实的分类必须写入 gaps，而不是生成 0 值或健康路径
	if len(gaps) == 0 {
		t.Fatal("expected gaps for categories without real facts")
	}
}

func TestBuildPathRowsProducesNothingForEmptyWindow(t *testing.T) {
	rows, _, gaps := buildPathRows(nil)
	if len(rows) != 0 {
		t.Fatalf("expected no path rows without facts, got %d", len(rows))
	}
	if len(gaps) != len(pathClasses) {
		t.Fatalf("expected a gap per path class, got %d", len(gaps))
	}
}

// 回归：真实来源（ClickHouse DateTime64 的 toString）不是 RFC3339。
// 只按 RFC3339 解析会让 duration_seconds 恒为 0 且趋势无点。
func TestParseEventTimeAcceptsRealSourceFormats(t *testing.T) {
	cases := map[string]string{
		"2026-09-13T06:00:00Z":           "2026-09-13T06:00:00Z",
		"2026-09-13T06:00:00.123456789Z": "2026-09-13T06:00:00Z",
		"2026-09-13 06:00:00.123456789":  "2026-09-13T06:00:00Z",
		"2026-09-13 06:00:00":            "2026-09-13T06:00:00Z",
	}
	for input, want := range cases {
		ts, ok := parseEventTime(input)
		if !ok {
			t.Fatalf("parseEventTime(%q) failed", input)
		}
		if ts.Format(time.RFC3339) != want {
			t.Fatalf("parseEventTime(%q) = %s, want %s", input, ts.Format(time.RFC3339), want)
		}
	}
	if _, ok := parseEventTime("not-a-timestamp"); ok {
		t.Fatal("invalid timestamp must not parse")
	}
}

func TestBuildPathRowsComputesDurationFromRealTimestampFormat(t *testing.T) {
	events := []query.KubernetesEvent{
		{Reason: "NodeNotReady", Message: "kubelet stopped", Kind: "Node", Name: "orbstack", InvolvedObject: "Node/orbstack", Timestamp: "2026-09-13 06:00:00.000000000", EventID: "e1"},
		{Reason: "NodeNotReady", Message: "kubelet stopped", Kind: "Node", Name: "orbstack", InvolvedObject: "Node/orbstack", Timestamp: "2026-09-13 06:10:00.000000000", EventID: "e2"},
	}
	rows, _, _ := buildPathRows(events)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].DurationSeconds != 600 {
		t.Fatalf("expected 600s duration from real timestamp format, got %d", rows[0].DurationSeconds)
	}
	if rows[0].SourceTimestamp != "2026-09-13T06:10:00Z" {
		t.Fatalf("unexpected source timestamp %q", rows[0].SourceTimestamp)
	}
}

func TestObservabilityWindowIsServerSideAbsolute(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/observability/paths", nil)
	start, end := observabilityWindow(req)
	if end.Sub(start) != time.Hour {
		t.Fatalf("default window must be 60 minutes, got %s", end.Sub(start))
	}

	req = httptest.NewRequest("GET", "/api/v1/observability/paths?from=2026-09-13T01:00:00Z&to=2026-09-13T02:00:00Z", nil)
	start, end = observabilityWindow(req)
	if start.Format(time.RFC3339) != "2026-09-13T01:00:00Z" || end.Format(time.RFC3339) != "2026-09-13T02:00:00Z" {
		t.Fatalf("explicit window not honored: %s → %s", start, end)
	}

	// 非法或倒置窗口必须回落为安全的 60 分钟窗口，不能放大范围
	req = httptest.NewRequest("GET", "/api/v1/observability/paths?from=2026-09-13T05:00:00Z&to=2026-09-13T02:00:00Z", nil)
	start, end = observabilityWindow(req)
	if end.Before(start) {
		t.Fatal("inverted window must be normalized")
	}
}

func TestPathClassesCoverRequiredCloudPlatformPaths(t *testing.T) {
	required := []string{"控制面调用", "虚拟机启动", "Pod 网络", "卷挂载", "镜像拉取", "负载均衡", "DNS", "Kubernetes 控制路径", "计算依赖路径", "存储依赖路径", "网络依赖路径"}
	have := map[string]bool{}
	for _, class := range pathClasses {
		have[class.Category] = true
	}
	for _, want := range required {
		if !have[want] {
			t.Fatalf("missing required path category %q (规范 §6.4)", want)
		}
	}
	// 禁止业务示例
	for _, class := range pathClasses {
		if class.Category == "下单" || class.Category == "商品查询" {
			t.Fatalf("business example leaked into path catalog: %s", class.Category)
		}
	}
}
