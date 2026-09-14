package api

import (
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// 总览/集群的集群口径容量事实（设计规范 §6.2 / §6.3）。
//
// 硬约束：
//   - CPU 口径 = 已使用核数 ÷ 可分配核数（allocatable），不是某一张卡或某一个节点。
//   - 内存口径 = 已使用容量 ÷ 可分配容量（allocatable）。
//   - 必须同时给出 P95 节点利用率与最大值、热点节点数，避免集群平均值掩盖单节点热点。
//   - 拿不到 metrics-server 的集群不得显示 0 或健康，必须显式 not_connected/failed。

type capacityResourceFact struct {
	Used            float64  `json:"used"`
	Allocatable     float64  `json:"allocatable"`
	UsageRatio      *float64 `json:"usage_ratio"`
	Unit            string   `json:"unit"`
	Aggregation     string   `json:"aggregation"`
	Source          string   `json:"source"`
	SourceTimestamp string   `json:"source_timestamp"`
}

type capacityNodeFact struct {
	Total    int `json:"total"`
	Ready    int `json:"ready"`
	NotReady int `json:"not_ready"`
	Unknown  int `json:"unknown"`
}

type capacityClusterFact struct {
	ClusterID           string               `json:"cluster_id"`
	Name                string               `json:"name"`
	CPU                 capacityResourceFact `json:"cpu"`
	Memory              capacityResourceFact `json:"memory"`
	Nodes               capacityNodeFact     `json:"nodes"`
	P95CPUUtilization   *float64             `json:"p95_cpu_utilization"`
	P95MemUtilization   *float64             `json:"p95_mem_utilization"`
	MaxCPUUtilization   *float64             `json:"max_cpu_utilization"`
	MaxMemUtilization   *float64             `json:"max_mem_utilization"`
	HotNodeCount        int                  `json:"hot_node_count"`
	HotNodeThresholdPct float64              `json:"hot_node_threshold_pct"`
	Quality             string               `json:"quality"`
	QualityReason       string               `json:"quality_reason,omitempty"`
	RegistrationStatus  string               `json:"registration_status"`
}

type platformCapacityResponse struct {
	GeneratedAt string                `json:"generated_at"`
	Clusters    []capacityClusterFact `json:"clusters"`
	Count       int                   `json:"count"`
	Meta        ResourceReadMeta      `json:"meta"`
}

const hotNodeThresholdPct = 80.0

// parseQuantity 对内存返回 Ki 基数的数值（"Ki" 系数 1，"Mi" 系数 1024）。
// 对外统一换算为 bytes，单位不可隐含。
const memoryKiBToBytes = 1024.0

type nodeCapacitySample struct {
	name       string
	ready      bool
	knownReady bool
	allocCPU   float64
	allocMem   float64
	usedCPU    float64
	usedMem    float64
	hasUsage   bool
}

// parseNodeAllocatable 只读 Allocatable（规范口径），缺失时回落到 Capacity 并标记。
func parseNodeAllocatable(data []byte) ([]nodeCapacitySample, bool) {
	var r struct {
		Items []struct {
			Metadata struct{ Name string }
			Status   struct {
				Conditions  []struct{ Type, Status string }
				Capacity    map[string]string
				Allocatable map[string]string
			}
		}
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, false
	}
	usedFallback := false
	out := make([]nodeCapacitySample, 0, len(r.Items))
	for _, it := range r.Items {
		ready, known := false, false
		for _, c := range it.Status.Conditions {
			if c.Type == "Ready" {
				known = true
				ready = c.Status == "True"
			}
		}
		cpu := it.Status.Allocatable["cpu"]
		mem := it.Status.Allocatable["memory"]
		if cpu == "" {
			cpu = it.Status.Capacity["cpu"]
			usedFallback = true
		}
		if mem == "" {
			mem = it.Status.Capacity["memory"]
			usedFallback = true
		}
		out = append(out, nodeCapacitySample{
			name:       it.Metadata.Name,
			ready:      ready,
			knownReady: known,
			allocCPU:   parseQuantity(cpu),
			allocMem:   parseQuantity(mem),
		})
	}
	return out, usedFallback
}

// applyNodeUsage 用 metrics.k8s.io 的真实用量填充样本；返回成功匹配的节点数。
func applyNodeUsage(samples []nodeCapacitySample, data []byte) int {
	var r struct {
		Items []struct {
			Metadata struct{ Name string }
			Usage    map[string]string
		}
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return 0
	}
	byName := map[string]*nodeCapacitySample{}
	for i := range samples {
		byName[samples[i].name] = &samples[i]
	}
	matched := 0
	for _, it := range r.Items {
		sample, ok := byName[it.Metadata.Name]
		if !ok {
			continue
		}
		sample.usedCPU = parseQuantity(it.Usage["cpu"])
		sample.usedMem = parseQuantity(it.Usage["memory"])
		sample.hasUsage = true
		matched++
	}
	return matched
}

// percentile 返回升序样本的 P 分位（最接近秩法），空集返回 nil。
func percentile(values []float64, p float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	idx := int(float64(len(sorted))*p+0.999999) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	v := round2(sorted[idx])
	return &v
}

func maxOrNil(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	best := values[0]
	for _, v := range values {
		if v > best {
			best = v
		}
	}
	v := round2(best)
	return &v
}

func ratioOrNil(used, allocatable float64) *float64 {
	if allocatable <= 0 {
		return nil
	}
	v := round4(used / allocatable)
	return &v
}

// aggregateClusterCapacity 把节点样本聚合为集群口径事实。
func aggregateClusterCapacity(cluster store.Cluster, samples []nodeCapacitySample, now time.Time, quality, reason string) capacityClusterFact {
	fact := capacityClusterFact{
		ClusterID:           cluster.ClusterID,
		Name:                cluster.Name,
		HotNodeThresholdPct: hotNodeThresholdPct,
		Quality:             quality,
		QualityReason:       reason,
		RegistrationStatus:  cluster.LifecycleStatus,
	}
	var cpuUtil, memUtil []float64
	usageKnown := false
	for _, s := range samples {
		fact.Nodes.Total++
		switch {
		case !s.knownReady:
			fact.Nodes.Unknown++
		case s.ready:
			fact.Nodes.Ready++
		default:
			fact.Nodes.NotReady++
		}
		fact.CPU.Allocatable += s.allocCPU
		// parseQuantity 的内存口径统一为 KiB（Ki 基数）；对外必须显式换算为 bytes，
		// 否则前端按 GiB 展示会出现 1024 倍误差——真实环境验证发现的单位缺陷。
		fact.Memory.Allocatable += s.allocMem * memoryKiBToBytes
		if s.hasUsage {
			usageKnown = true
			fact.CPU.Used += s.usedCPU
			fact.Memory.Used += s.usedMem * memoryKiBToBytes
			if s.allocCPU > 0 {
				cpuUtil = append(cpuUtil, s.usedCPU/s.allocCPU*100)
			}
			if s.allocMem > 0 {
				memUtil = append(memUtil, s.usedMem/s.allocMem*100)
			}
			if s.allocCPU > 0 && s.usedCPU/s.allocCPU*100 >= hotNodeThresholdPct {
				fact.HotNodeCount++
			} else if s.allocMem > 0 && s.usedMem/s.allocMem*100 >= hotNodeThresholdPct {
				fact.HotNodeCount++
			}
		}
	}
	fact.CPU.Used = round4(fact.CPU.Used)
	fact.CPU.Allocatable = round4(fact.CPU.Allocatable)
	fact.Memory.Used = round4(fact.Memory.Used)
	fact.Memory.Allocatable = round4(fact.Memory.Allocatable)
	ts := now.UTC().Format(time.RFC3339)
	fact.CPU.Unit = "cores"
	fact.CPU.Aggregation = "sum(node usage) / sum(node allocatable)"
	fact.CPU.Source = "metrics.k8s.io/v1beta1 + core/v1 nodes.allocatable"
	fact.CPU.SourceTimestamp = ts
	fact.Memory.Unit = "bytes"
	fact.Memory.Aggregation = "sum(node usage) / sum(node allocatable)"
	fact.Memory.Source = "metrics.k8s.io/v1beta1 + core/v1 nodes.allocatable"
	fact.Memory.SourceTimestamp = ts

	if usageKnown {
		fact.CPU.UsageRatio = ratioOrNil(fact.CPU.Used, fact.CPU.Allocatable)
		fact.Memory.UsageRatio = ratioOrNil(fact.Memory.Used, fact.Memory.Allocatable)
		fact.P95CPUUtilization = percentile(cpuUtil, 0.95)
		fact.P95MemUtilization = percentile(memUtil, 0.95)
		fact.MaxCPUUtilization = maxOrNil(cpuUtil)
		fact.MaxMemUtilization = maxOrNil(memUtil)
	}
	return fact
}

// PlatformCapacity handles GET /api/v1/platform/capacity.
// 只对 query-api 所在集群（AIOPS_SYSTEM_CLUSTER_ID）读取真实 metrics；
// 其余纳管集群显式返回 not_connected，不得用 0 或健康值填充（D-09）。
func (h *Handler) PlatformCapacity(w http.ResponseWriter, r *http.Request) {
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
	if auth.TenantID == "" {
		respondJSON(w, http.StatusConflict, map[string]string{"error": "SCOPE_SELECTION_REQUIRED"})
		return
	}
	clusters, err := (&store.ClusterDAO{}).ListForTenant(auth.TenantID)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "PLATFORM_CAPACITY_UNAVAILABLE"})
		return
	}
	localClusterID := strings.TrimSpace(os.Getenv("AIOPS_SYSTEM_CLUSTER_ID"))
	now := time.Now().UTC()

	facts := make([]capacityClusterFact, 0, len(clusters))
	partial := false
	warnings := make([]string, 0)
	// 本地集群的节点与用量只读取一次
	var localSamples []nodeCapacitySample
	localLoaded := false
	localErr := ""
	localQuality := "healthy"
	localReason := ""

	for _, cluster := range clusters {
		if cluster.ClusterID == "" || (cluster.LifecycleStatus != "" && cluster.LifecycleStatus != "active" && cluster.LifecycleStatus != "ready") {
			continue
		}
		if localClusterID == "" || cluster.ClusterID != localClusterID {
			facts = append(facts, capacityClusterFact{
				ClusterID:           cluster.ClusterID,
				Name:                cluster.Name,
				Quality:             "not_connected",
				QualityReason:       "该集群未接入中央指标读取通道，无法给出集群口径 CPU/内存；不显示 0 或健康",
				RegistrationStatus:  cluster.LifecycleStatus,
				HotNodeThresholdPct: hotNodeThresholdPct,
			})
			partial = true
			warnings = append(warnings, "cluster "+cluster.ClusterID+" capacity not connected")
			continue
		}
		if !localLoaded {
			localLoaded = true
			nodeData, nerr := k8sAPIFn("/api/v1/nodes")
			if nerr != nil {
				localQuality = "failed"
				localReason = "读取节点可分配容量失败：" + nerr.Error()
			} else if samples, fellBack := parseNodeAllocatable(nodeData); len(samples) == 0 {
				localQuality = "failed"
				localReason = "节点列表为空，无法计算集群口径"
			} else {
				if fellBack {
					warnings = append(warnings, "some nodes missing allocatable, fell back to capacity")
				}
				localSamples = samples
				metricsData, merr := k8sAPIFn("/apis/metrics.k8s.io/v1beta1/nodes")
				if merr != nil {
					localQuality = "partial"
					localReason = "metrics-server 不可用，容量分母可用但用量缺失：" + merr.Error()
				} else if matched := applyNodeUsage(localSamples, metricsData); matched == 0 {
					localQuality = "partial"
					localReason = "metrics-server 未返回任何节点用量"
				} else if matched < len(localSamples) {
					localQuality = "partial"
					localReason = "部分节点缺少用量样本"
				}
			}
			if localQuality != "healthy" {
				partial = true
				warnings = append(warnings, "local cluster capacity quality="+localQuality)
			}
			localErr = localReason
			_ = localErr
		}
		fact := aggregateClusterCapacity(cluster, localSamples, now, localQuality, localReason)
		facts = append(facts, fact)
	}

	meta := ResourceReadMeta{
		GeneratedAt:  now,
		Partial:      partial,
		Stale:        false,
		WarningCodes: warnings,
	}
	respondJSON(w, http.StatusOK, platformCapacityResponse{
		GeneratedAt: now.Format(time.RFC3339),
		Clusters:    facts,
		Count:       len(facts),
		Meta:        meta,
	})
}
