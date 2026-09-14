package api

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/query"
)

// 全链路监控（设计规范 §6.4 / §7.3）：
// 只覆盖云平台基础设施与控制路径，禁止业务示例（下单、商品查询）。
//
// 数据来源固定为真实事实表 observability.k8s_events（event-collector 唯一写入者），
// 不使用任何前端常量、默认路径或合成指标。窗口内没有事实的分类不生成路径行，
// 而是写入 gaps，避免用 0 或绿色伪装成健康。

type pathClass struct {
	ID       string
	Name     string
	Category string
	// match 命中判定
	match func(reason, message, kind, component string) bool
}

func containsAny(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if needle != "" && strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

// pathClasses 的顺序即匹配优先级：先具体后宽泛，保证同一事件分类结果确定可复现。
// k8s-control 使用最宽泛的关键词，必须放在最后。
var pathClasses = []pathClass{
	{
		ID: "control-plane", Name: "控制面调用路径", Category: "控制面调用",
		match: func(reason, message, kind, component string) bool {
			blob := reason + " " + message + " " + component + " " + kind
			return containsAny(blob, "apiserver", "kube-apiserver", "etcd", "Scheduler", "controller-manager")
		},
	},
	{
		ID: "storage-dependency", Name: "存储依赖路径", Category: "存储依赖路径",
		match: func(reason, message, kind, component string) bool {
			blob := reason + " " + message + " " + kind
			return containsAny(blob, "PVC", "PersistentVolume", "CSI", "storageclass", "StorageClass")
		},
	},
	{
		ID: "image-pull", Name: "镜像拉取路径", Category: "镜像拉取",
		match: func(reason, message, kind, component string) bool {
			blob := reason + " " + message + " " + component
			return containsAny(blob, "ErrImagePull", "ImagePullBackOff", "FailedToPullImage", "PullImage")
		},
	},
	{
		ID: "volume-mount", Name: "卷挂载路径", Category: "卷挂载",
		match: func(reason, message, kind, component string) bool {
			blob := reason + " " + message
			return containsAny(blob, "FailedMount", "FailedAttachVolume", "VolumeFailed", "FailedBinding",
				"ProvisioningFailed", "VolumeResizeFailed", "MountVolume")
		},
	},
	{
		ID: "pod-network", Name: "Pod 网络路径", Category: "Pod 网络",
		match: func(reason, message, kind, component string) bool {
			blob := reason + " " + message + " " + component
			return containsAny(blob, "FailedCreatePodSandBox", "FailedCreatePodSandbox", "NetworkNotReady",
				"SandboxChanged", "CNI", "networkPlugin")
		},
	},
	{
		ID: "dns", Name: "DNS 解析路径", Category: "DNS",
		match: func(reason, message, kind, component string) bool {
			blob := reason + " " + message + " " + component + " " + kind
			return containsAny(blob, "DNS", "dns", "CoreDNS", "coredns", "nameserver", "no such host", "NXDOMAIN")
		},
	},
	{
		ID: "load-balancer", Name: "负载均衡与入口路径", Category: "负载均衡",
		match: func(reason, message, kind, component string) bool {
			blob := reason + " " + message + " " + kind
			return containsAny(blob, "Ingress", "LoadBalancer", "Backend", "Endpoints", "endpoints")
		},
	},
	{
		ID: "vm-boot", Name: "虚拟机启动路径", Category: "虚拟机启动",
		match: func(reason, message, kind, component string) bool {
			blob := reason + " " + message + " " + kind
			return containsAny(blob, "VirtualMachine", "VMI", "Migration", "virt-launcher", "KubeVirt")
		},
	},
	{
		ID: "network-dependency", Name: "网络依赖路径", Category: "网络依赖路径",
		match: func(reason, message, kind, component string) bool {
			blob := reason + " " + message + " " + kind + " " + component
			return containsAny(blob, "NetworkPolicy", "network-attachment", "NAD", "Multus", "丢包")
		},
	},
	{
		ID: "compute-dependency", Name: "计算依赖路径", Category: "计算依赖路径",
		match: func(reason, message, kind, component string) bool {
			blob := reason + " " + message
			return containsAny(blob, "DiskPressure", "MemoryPressure", "PIDPressure", "NodeHasInsufficient",
				"Insufficient cpu", "Insufficient memory", "OOMKilled")
		},
	},
	{
		ID: "k8s-control", Name: "Kubernetes 控制路径", Category: "Kubernetes 控制路径",
		match: func(reason, message, kind, component string) bool {
			blob := reason + " " + message + " " + kind + " " + component
			return containsAny(blob, "Kubelet", "kubelet", "NotReady", "Evicted", "Eviction",
				"NodeNotReady", "kube-proxy")
		},
	},
}

type observabilityPathRow struct {
	PathID            string  `json:"path_id"`
	Name              string  `json:"name"`
	Category          string  `json:"category"`
	Status            string  `json:"status"`
	Severity          int     `json:"severity"`
	AffectedResources int     `json:"affected_resources"`
	Deviation         float64 `json:"deviation"`
	DurationSeconds   int     `json:"duration_seconds"`
	Quality           string  `json:"quality"`
	SourceTimestamp   string  `json:"source_timestamp,omitempty"`
}

type observabilitySourceState struct {
	Source        string `json:"source"`
	State         string `json:"state"`
	LastSuccessAt string `json:"last_success_at,omitempty"`
	Detail        string `json:"detail,omitempty"`
}

type observabilityPathCatalogResponse struct {
	Paths         []observabilityPathRow     `json:"paths"`
	Count         int                        `json:"count"`
	GeneratedAt   string                     `json:"generated_at"`
	TimeWindow    map[string]string          `json:"time_window"`
	Quality       string                     `json:"quality"`
	QualityReason string                     `json:"quality_reason,omitempty"`
	Partial       bool                       `json:"partial"`
	Stale         bool                       `json:"stale"`
	Sources       []observabilitySourceState `json:"sources"`
	Gaps          []string                   `json:"gaps"`
}

func severityFromCount(count, affected int) int {
	switch {
	case affected >= 8 || count >= 40:
		return 4
	case affected >= 4 || count >= 15:
		return 3
	case affected >= 2 || count >= 5:
		return 2
	default:
		return 1
	}
}

// classifyPathID 返回事件命中的路径分类；未命中返回空串（不猜测）。
func classifyPathID(event query.KubernetesEvent) string {
	reason := event.Reason
	message := event.Message
	kind := event.Kind
	component := event.SourceComponent
	for _, class := range pathClasses {
		if class.match(reason, message, kind, component) {
			return class.ID
		}
	}
	return ""
}

// buildPathRows 从真实事件聚合路径行；只输出窗口内有事实的分类。
func buildPathRows(events []query.KubernetesEvent) ([]observabilityPathRow, map[string][]query.KubernetesEvent, []string) {
	grouped := map[string][]query.KubernetesEvent{}
	total := 0
	for _, event := range events {
		id := classifyPathID(event)
		if id == "" {
			continue
		}
		grouped[id] = append(grouped[id], event)
		total++
	}

	rows := make([]observabilityPathRow, 0, len(grouped))
	missing := make([]string, 0)
	for _, class := range pathClasses {
		items, ok := grouped[class.ID]
		if !ok || len(items) == 0 {
			missing = append(missing, class.Category+"：该窗口内没有相关真实事件，无法判断是否存在异常")
			continue
		}
		objects := map[string]struct{}{}
		var earliest, latest time.Time
		for _, item := range items {
			key := item.InvolvedObject
			if key == "" {
				key = item.Kind + "/" + item.Name
			}
			objects[key] = struct{}{}
			if ts, ok := parseEventTime(item.Timestamp); ok {
				if earliest.IsZero() || ts.Before(earliest) {
					earliest = ts
				}
				if latest.IsZero() || ts.After(latest) {
					latest = ts
				}
			}
		}
		duration := 0
		if !earliest.IsZero() && !latest.IsZero() {
			duration = int(latest.Sub(earliest).Seconds())
		}
		deviation := 0.0
		if total > 0 {
			deviation = float64(len(items)) / float64(total)
		}
		status := "degraded"
		if len(objects) == 0 {
			status = "unknown"
		}
		rows = append(rows, observabilityPathRow{
			PathID:            class.ID,
			Name:              class.Name,
			Category:          class.Category,
			Status:            status,
			Severity:          severityFromCount(len(items), len(objects)),
			AffectedResources: len(objects),
			Deviation:         round4(deviation),
			DurationSeconds:   duration,
			Quality:           "healthy",
			SourceTimestamp:   latest.UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].PathID < rows[j].PathID })
	return rows, grouped, missing
}

func round4(v float64) float64 {
	return float64(int(v*10000+0.5)) / 10000
}

// ObservabilityPaths handles GET /api/v1/observability/paths.
func (h *Handler) ObservabilityPaths(w http.ResponseWriter, r *http.Request) {
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
	clusterID := strings.TrimSpace(r.URL.Query().Get("cluster_id"))
	if auth.TenantID == "" || clusterID == "" {
		respondJSON(w, http.StatusConflict, map[string]string{"error": "SCOPE_SELECTION_REQUIRED"})
		return
	}
	start, end := observabilityWindow(r)
	now := time.Now().UTC()

	if h.eventRepo == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error":   "OBSERVABILITY_PATH_UNAVAILABLE",
			"message": "事件事实来源未配置，无法生成路径目录",
		})
		return
	}
	events, err := h.eventRepo.List(r.Context(), auth.TenantID, clusterID, nil, &start, &end, 500, 0)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error":   "OBSERVABILITY_PATH_UNAVAILABLE",
			"message": err.Error(),
		})
		return
	}

	rows, _, missing := buildPathRows(events)
	quality := "healthy"
	partial := false
	reason := ""
	if len(events) == 0 {
		quality = "unknown"
		reason = "该窗口内没有任何 Kubernetes 事件事实，路径目录无法判定状态"
	} else if len(rows) == 0 {
		quality = "partial"
		partial = true
		reason = "窗口内有事件但没有任何事件映射到云平台路径分类，需要补充分类规则或来源"
	}

	respondJSON(w, http.StatusOK, observabilityPathCatalogResponse{
		Paths:         rows,
		Count:         len(rows),
		GeneratedAt:   now.Format(time.RFC3339),
		TimeWindow:    map[string]string{"from": start.Format(time.RFC3339), "to": end.Format(time.RFC3339)},
		Quality:       quality,
		QualityReason: reason,
		Partial:       partial,
		Stale:         false,
		Sources: []observabilitySourceState{{
			Source:        "observability.k8s_events",
			State:         quality,
			LastSuccessAt: now.Format(time.RFC3339),
			Detail:        "event-collector 为唯一写入者；query-api 只读",
		}},
		Gaps: missing,
	})
}

type observabilityPathNode struct {
	NodeUID    string   `json:"node_uid"`
	Name       string   `json:"name"`
	NodeType   string   `json:"node_type"`
	Status     string   `json:"status"`
	LatencyMS  *float64 `json:"latency_ms"`
	ErrorRate  *float64 `json:"error_rate"`
	Saturation *float64 `json:"saturation"`
	Quality    string   `json:"quality"`
}

type observabilityPathEdge struct {
	EdgeUID  string `json:"edge_uid"`
	SourceID string `json:"source_uid"`
	TargetID string `json:"target_uid"`
	Relation string `json:"relation"`
	Status   string `json:"status"`
}

type observabilityPathSegment struct {
	Segment       string   `json:"segment"`
	LatencyMS     *float64 `json:"latency_ms"`
	ErrorRate     *float64 `json:"error_rate"`
	Saturation    *float64 `json:"saturation"`
	BaselineDelta *float64 `json:"baseline_delta"`
	SampleSize    int      `json:"sample_size"`
	Quality       string   `json:"quality"`
}

type observabilityTrendPoint struct {
	TS         string   `json:"ts"`
	LatencyMS  *float64 `json:"latency_ms"`
	ErrorRate  *float64 `json:"error_rate"`
	Throughput *float64 `json:"throughput"`
	Saturation *float64 `json:"saturation"`
}

type observabilityTrend struct {
	Metric      string                    `json:"metric"`
	Unit        string                    `json:"unit"`
	Aggregation string                    `json:"aggregation"`
	StepSeconds int                       `json:"step_seconds"`
	Points      []observabilityTrendPoint `json:"points"`
}

type observabilityEvidence struct {
	EvidenceID  string `json:"evidence_id"`
	Type        string `json:"type"`
	Summary     string `json:"summary"`
	OccurredAt  string `json:"occurred_at"`
	ResourceUID string `json:"resource_uid,omitempty"`
	Quality     string `json:"quality"`
}

type observabilityPathDetailResponse struct {
	PathID      string                     `json:"path_id"`
	Name        string                     `json:"name"`
	Category    string                     `json:"category"`
	Nodes       []observabilityPathNode    `json:"nodes"`
	Edges       []observabilityPathEdge    `json:"edges"`
	Segments    []observabilityPathSegment `json:"segments"`
	Trend       observabilityTrend         `json:"trend"`
	Events      []observabilityEvidence    `json:"events"`
	Quality     string                     `json:"quality"`
	Partial     bool                       `json:"partial"`
	Stale       bool                       `json:"stale"`
	GeneratedAt string                     `json:"generated_at"`
	Gaps        []string                   `json:"gaps"`
}

// ObservabilityPathDetail handles GET /api/v1/observability/paths/{pathId}.
// 只允许白名单内的 pathId；未知 pathId 返回 404，不静默回落到默认路径。
func (h *Handler) ObservabilityPathDetail(w http.ResponseWriter, r *http.Request) {
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
	pathID := strings.TrimPrefix(r.URL.Path, "/api/v1/observability/paths/")
	pathID = strings.Trim(pathID, "/")
	clusterID := strings.TrimSpace(r.URL.Query().Get("cluster_id"))
	if auth.TenantID == "" || clusterID == "" {
		respondJSON(w, http.StatusConflict, map[string]string{"error": "SCOPE_SELECTION_REQUIRED"})
		return
	}
	var class *pathClass
	for i := range pathClasses {
		if pathClasses[i].ID == pathID {
			class = &pathClasses[i]
			break
		}
	}
	if class == nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "PATH_NOT_FOUND"})
		return
	}
	if h.eventRepo == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "OBSERVABILITY_PATH_UNAVAILABLE"})
		return
	}
	start, end := observabilityWindow(r)
	events, err := h.eventRepo.List(r.Context(), auth.TenantID, clusterID, nil, &start, &end, 500, 0)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "OBSERVABILITY_PATH_UNAVAILABLE"})
		return
	}

	matched := make([]query.KubernetesEvent, 0, len(events))
	for _, event := range events {
		if classifyPathID(event) == class.ID {
			matched = append(matched, event)
		}
	}

	nodes := make([]observabilityPathNode, 0)
	nodeSeen := map[string]struct{}{}
	segments := make([]observabilityPathSegment, 0)
	evidence := make([]observabilityEvidence, 0, len(matched))
	step := int(end.Sub(start).Seconds()) / 12
	if step <= 0 {
		step = 300
	}
	buckets := map[int64]float64{}
	for _, event := range matched {
		key := event.InvolvedObject
		if key == "" {
			key = event.Kind + "/" + event.Name
		}
		if _, exists := nodeSeen[key]; !exists {
			nodeSeen[key] = struct{}{}
			nodes = append(nodes, observabilityPathNode{
				NodeUID:  key,
				Name:     key,
				NodeType: event.Kind,
				Status:   event.Type,
				Quality:  "healthy",
			})
			sample := 1
			segments = append(segments, observabilityPathSegment{
				Segment:    key,
				SampleSize: sample,
				Quality:    "healthy",
			})
		}
		if ts, ok := parseEventTime(event.Timestamp); ok {
			bucket := ts.Unix() / int64(step) * int64(step)
			buckets[bucket]++
		}
		evidence = append(evidence, observabilityEvidence{
			EvidenceID:  event.EventID,
			Type:        "k8s_event",
			Summary:     event.Reason + ": " + firstLine(event.Message),
			OccurredAt:  event.Timestamp,
			ResourceUID: event.InvolvedObject,
			Quality:     "healthy",
		})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeUID < nodes[j].NodeUID })

	points := make([]observabilityTrendPoint, 0, len(buckets))
	keys := make([]int64, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		count := buckets[k]
		points = append(points, observabilityTrendPoint{
			TS:         time.Unix(k, 0).UTC().Format(time.RFC3339),
			Throughput: &count,
		})
	}

	gaps := make([]string, 0)
	if len(matched) == 0 {
		gaps = append(gaps,
			"该窗口内没有命中此路径分类的真实事件，无法给出延迟与饱和度",
			"秒级延迟、错误率与饱和度依赖指标来源，当前未接入，因此不显示数值而不是显示 0",
		)
	} else {
		gaps = append(gaps,
			"延迟、错误率与饱和度需要指标来源；当前仅提供事件计数趋势，缺失部分显式标注",
		)
	}
	quality := "healthy"
	if len(matched) == 0 {
		quality = "unknown"
	}

	respondJSON(w, http.StatusOK, observabilityPathDetailResponse{
		PathID:   class.ID,
		Name:     class.Name,
		Category: class.Category,
		Nodes:    nodes,
		Edges:    []observabilityPathEdge{},
		Segments: segments,
		Trend: observabilityTrend{
			Metric:      "k8s_warning_event_count",
			Unit:        "events",
			Aggregation: "count",
			StepSeconds: step,
			Points:      points,
		},
		Events:      evidence,
		Quality:     quality,
		Partial:     len(gaps) > 1,
		Stale:       false,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Gaps:        gaps,
	})
}

// eventTimeLayouts 覆盖真实来源格式。observability.k8s_events 的 ts 为
// ClickHouse DateTime64，toString() 产出 "2006-01-02 15:04:05.999999999"
// （无时区，语义为 UTC），而不是 RFC3339。此前只按 RFC3339 解析导致
// duration_seconds 恒为 0 且趋势无点——真实环境验证发现的缺陷。
var eventTimeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04:05",
}

// parseEventTime 解析事件时间；缺时区时按 UTC 解释（与存储语义一致）。
func parseEventTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range eventTimeLayouts {
		if ts, err := time.Parse(layout, value); err == nil {
			return ts.UTC(), true
		}
	}
	return time.Time{}, false
}

func firstLine(value string) string {
	if idx := strings.IndexAny(value, "\r\n"); idx >= 0 {
		return value[:idx]
	}
	if len(value) > 240 {
		return value[:240]
	}
	return value
}

// observabilityWindow 解析绝对时间窗；缺省为最近 60 分钟（服务端计算，不依赖前端）。
func observabilityWindow(r *http.Request) (time.Time, time.Time) {
	q := r.URL.Query()
	end := time.Now().UTC()
	if raw := strings.TrimSpace(q.Get("to")); raw != "" {
		if ts, err := time.Parse(time.RFC3339, raw); err == nil {
			end = ts.UTC()
		}
	}
	start := end.Add(-60 * time.Minute)
	if raw := strings.TrimSpace(q.Get("from")); raw != "" {
		if ts, err := time.Parse(time.RFC3339, raw); err == nil {
			start = ts.UTC()
		}
	}
	if start.After(end) {
		start = end.Add(-60 * time.Minute)
	}
	return start, end
}
