package api

import (
	"strings"
	"sync"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// clusterEvidenceFreshness 是集群观测证据的有效期。超过它只能判为 unknown/stale，
// 不能因为注册状态仍是 active/ready 就显示健康（信息架构 §4.1 / 验收 G3）。
const clusterEvidenceFreshness = 5 * time.Minute

// clusterOperationalState 是平台矩阵、平台集群列表与集群详情共用的唯一健康投影。
//
//	Health             观测健康：healthy | degraded | critical | unknown
//	RegistrationStatus 接入/注册状态（active/ready/running/ok），只描述生命周期，不产生健康结论
//	Reason             可下钻的状态原因
//	Covered            是否有新鲜的观测证据（unknown 也可能是"已被覆盖但无健康结论"）
//	Stale              证据是否已过期
type clusterOperationalState struct {
	Health             string
	RegistrationStatus string
	Reason             string
	Covered            bool
	Stale              bool
}

// projectClusterOperationalState 是唯一健康事实规则。平台、集群列表和集群详情
// 必须调用它，不得各自实现映射（否则会出现同一集群两处状态矛盾的现场缺陷）。
//
// 优先级：
//  1. 无 UpdatedAt → unknown（没有观测证据）
//  2. 证据超过 5 分钟 → unknown + stale（观测数据陈旧）
//  3. critical/down/error/offline/disconnected → critical
//  4. degraded/warning/unhealthy → degraded
//  5. healthy → healthy
//  6. active/ready/running/ok 等接入状态 → unknown（只有接入状态，缺少资源健康证据）
func projectClusterOperationalState(cluster store.Cluster, now time.Time) clusterOperationalState {
	registration := strings.ToLower(strings.TrimSpace(cluster.LifecycleStatus))
	if cluster.UpdatedAt.IsZero() {
		return clusterOperationalState{Health: "unknown", RegistrationStatus: registration, Reason: "没有观测证据"}
	}
	if now.Sub(cluster.UpdatedAt) > clusterEvidenceFreshness {
		return clusterOperationalState{Health: "unknown", RegistrationStatus: registration, Reason: "观测数据陈旧", Stale: true}
	}
	state := strings.ToLower(strings.TrimSpace(cluster.Status))
	switch state {
	case "critical", "down", "error", "offline", "disconnected":
		return clusterOperationalState{Health: "critical", RegistrationStatus: registration, Reason: "观测到严重异常", Covered: true}
	case "degraded", "warning", "unhealthy":
		return clusterOperationalState{Health: "degraded", RegistrationStatus: registration, Reason: "观测到降级信号", Covered: true}
	case "healthy":
		return clusterOperationalState{Health: "healthy", RegistrationStatus: registration, Reason: "健康证据有效", Covered: true}
	default:
		return clusterOperationalState{Health: "unknown", RegistrationStatus: registration, Reason: "只有接入状态，缺少资源健康证据", Covered: true}
	}
}

// systemComponentResultView 是组件探活的 typed 视图。系统管理页与平台能力摘要
// 消费同一份结果，避免平台总览伪造 1/1。
type systemComponentResultView struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	LatencyMS int64  `json:"latency_ms"`
	Detail    string `json:"detail"`
}

// platformComponentCatalog 是平台组件清单。可选但未配置的组件标记 configured=false，
// 返回 not_configured，不进入能力摘要分母。
func platformComponentCatalog() []systemComponent {
	return []systemComponent{
		{"query-api", "service", "http", "http://query-api.observability.svc.cluster.local:8080/health", true},
		{"ingest", "service", "http", "http://ingest.observability.svc.cluster.local:8080/health", true},
		{"ai-orchestrator", "service", "http", "http://ai-orchestrator.observability.svc.cluster.local:8080/health", true},
		{"clickhouse", "middleware", "tcp", "clickhouse.observability.svc.cluster.local:8123", true},
		{"mysql", "middleware", "tcp", "mysql.observability.svc.cluster.local:3306", true},
		{"victoria-metrics", "middleware", "http", "http://victoria-metrics.observability.svc.cluster.local:8428/health", true},
		{"victoria-logs", "middleware", "http", "http://victoria-logs.observability.svc.cluster.local:9428/health", true},
		{"minio", "middleware", "http", "http://minio.observability.svc.cluster.local:9000/minio/health/live", false},
		{"frontend", "service", "http", "http://frontend.observability.svc.cluster.local/health", true},
	}
}

// collectSystemComponentResults 并发探测平台组件并返回 typed 结果。
// probe 由调用方注入，便于测试与复用同一探测实现。
func collectSystemComponentResults(probe func(kind, addr string) bool) []systemComponentResultView {
	components := platformComponentCatalog()
	results := make([]systemComponentResultView, len(components))
	var wg sync.WaitGroup
	for i, component := range components {
		wg.Add(1)
		go func(i int, component systemComponent) {
			defer wg.Done()
			results[i] = systemComponentResult(component, probe)
		}(i, component)
	}
	wg.Wait()
	return results
}

// systemComponentResult 探测单个组件并投影为 typed 视图。
func systemComponentResult(component systemComponent, probe func(kind, addr string) bool) systemComponentResultView {
	if !component.configured {
		return systemComponentResultView{
			Name: component.name, Type: component.typ, Status: "not_configured",
			LatencyMS: 0, Detail: "optional component is not configured",
		}
	}
	start := time.Now()
	ok := probe(component.kind, component.addr)
	latency := time.Since(start).Milliseconds()
	status := "ok"
	detail := ""
	if !ok {
		status = "down"
		detail = component.addr
	} else if latency >= 2000 {
		status = "degraded"
	}
	return systemComponentResultView{Name: component.name, Type: component.typ, Status: status, LatencyMS: latency, Detail: detail}
}

// summarizePlatformCapabilities 汇总能力状态：not_configured 不进入分母，
// 非 ok 项的名称进入 issues。没有任何探测结果时返回 0/0，由调用方显示“未获得组件状态”，
// 不伪造成 1/1 或 0/0 正常。
func summarizePlatformCapabilities(rows []systemComponentResultView) platformCapabilitySummary {
	summary := platformCapabilitySummary{Issues: []string{}}
	for _, row := range rows {
		status := strings.ToLower(strings.TrimSpace(row.Status))
		if status == "" || status == "not_configured" {
			continue
		}
		summary.Total++
		if status == "ok" {
			summary.Healthy++
			continue
		}
		summary.Issues = append(summary.Issues, row.Name)
	}
	return summary
}

// platformCapabilityCacheTTL 限制平台总览触发组件探测的频率，避免每次读页面都做网络探测。
const platformCapabilityCacheTTL = 30 * time.Second

var (
	platformCapabilityMu   sync.Mutex
	platformCapabilityRows []systemComponentResultView
	platformCapabilityAt   time.Time
)

// platformCapabilitySnapshot 返回最近一次组件探测结果（30s TTL），
// 与系统管理页共用同一 collector。
func platformCapabilitySnapshot() []systemComponentResultView {
	platformCapabilityMu.Lock()
	defer platformCapabilityMu.Unlock()
	if !platformCapabilityAt.IsZero() && time.Since(platformCapabilityAt) < platformCapabilityCacheTTL {
		return platformCapabilityRows
	}
	rows := collectSystemComponentResults(probeComponent)
	platformCapabilityRows = rows
	platformCapabilityAt = time.Now()
	return rows
}
