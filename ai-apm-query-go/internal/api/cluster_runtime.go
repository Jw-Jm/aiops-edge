package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// 集群页运行时事实（设计规范 §6.3）。
//
// 只返回真实来源可提供的事实：
//   - 节点就绪（core/v1 nodes）
//   - Pod 就绪 / 阶段分布 / 调度异常（core/v1 pods）
//   - 容器重启次数（pod.status.containerStatuses[].restartCount）
//
// 网络吞吐/错误/丢包/时延与存储使用率/IO 时延当前没有接入的指标来源，
// 必须显式返回 not_connected 并说明影响，禁止用 0 或健康值填充（D-09）。

type podPhaseFact struct {
	Total     int `json:"total"`
	Running   int `json:"running"`
	Pending   int `json:"pending"`
	Failed    int `json:"failed"`
	Succeeded int `json:"succeeded"`
	Unknown   int `json:"unknown"`
	NotReady  int `json:"not_ready"`
	Ready     int `json:"ready"`
}

type restartTopEntry struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Restarts  int    `json:"restarts"`
}

type restartFact struct {
	TotalPodsWithRestarts int               `json:"total_pods_with_restarts"`
	TotalRestarts         int               `json:"total_restarts"`
	MaxPerPod             int               `json:"max_per_pod"`
	Top                   []restartTopEntry `json:"top"`
	Source                string            `json:"source"`
}

type signalGapFact struct {
	Quality string `json:"quality"`
	Reason  string `json:"reason"`
}

type clusterRuntimeResponse struct {
	ClusterID   string        `json:"cluster_id"`
	Name        string        `json:"name"`
	Pods        podPhaseFact  `json:"pods"`
	Restarts    restartFact   `json:"restarts"`
	Network     signalGapFact `json:"network"`
	Storage     signalGapFact `json:"storage"`
	Quality     string        `json:"quality"`
	QualityNote string        `json:"quality_note,omitempty"`
	GeneratedAt string        `json:"generated_at"`
	Source      string        `json:"source"`
}

// parsePodRuntime 从 core/v1 pods 计算阶段分布、就绪与重启统计。
func parsePodRuntime(data []byte) (podPhaseFact, restartFact, bool) {
	var r struct {
		Items []struct {
			Metadata struct{ Name, Namespace string }
			Status   struct {
				Phase             string
				ContainerStatuses []struct {
					Name         string
					Ready        bool
					RestartCount int
				}
			}
		}
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return podPhaseFact{}, restartFact{}, false
	}
	pods := podPhaseFact{Total: len(r.Items)}
	restarts := restartFact{Source: "core/v1 pods.status.containerStatuses[].restartCount", Top: []restartTopEntry{}}
	for _, item := range r.Items {
		switch item.Status.Phase {
		case "Running":
			pods.Running++
		case "Pending":
			pods.Pending++
		case "Failed":
			pods.Failed++
		case "Succeeded":
			pods.Succeeded++
		default:
			pods.Unknown++
		}
		// 终态 Pod（Succeeded/Failed）既不参与就绪分母，也不参与重启统计：
		// 已完成 Pod 的容器 ready=false 与历史 restartCount 不是当前健康事实，
		// 计入会把"已正常结束"误报为"未就绪"，并虚增重启总量。
		if item.Status.Phase != "Running" && item.Status.Phase != "Pending" {
			continue
		}
		ready, counted := true, false
		podRestarts := 0
		for _, cs := range item.Status.ContainerStatuses {
			counted = true
			if !cs.Ready {
				ready = false
			}
			podRestarts += cs.RestartCount
		}
		if !counted {
			// 没有容器状态的 Pod（例如 Pending 调度中）不计入就绪分母，
			// 否则会把"尚未调度"误算成"不就绪"。
			continue
		}
		if ready {
			pods.Ready++
		} else {
			pods.NotReady++
		}
		if podRestarts > 0 {
			restarts.TotalPodsWithRestarts++
			restarts.TotalRestarts += podRestarts
			if podRestarts > restarts.MaxPerPod {
				restarts.MaxPerPod = podRestarts
			}
			restarts.Top = append(restarts.Top, restartTopEntry{
				Namespace: item.Metadata.Namespace,
				Name:      item.Metadata.Name,
				Restarts:  podRestarts,
			})
		}
	}
	sortRestartTop(restarts.Top)
	if len(restarts.Top) > 10 {
		restarts.Top = restarts.Top[:10]
	}
	return pods, restarts, true
}

func sortRestartTop(entries []restartTopEntry) {
	// 稳定降序：重启次数优先，其次 namespace/name，保证同一数据多次渲染一致。
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0; j-- {
			a, b := entries[j-1], entries[j]
			if a.Restarts < b.Restarts ||
				(a.Restarts == b.Restarts && (a.Namespace > b.Namespace ||
					(a.Namespace == b.Namespace && a.Name > b.Name))) {
				entries[j-1], entries[j] = b, a
			} else {
				break
			}
		}
	}
}

// ClusterRuntime handles GET /api/v1/clusters/{clusterId}/runtime.
func (h *Handler) ClusterRuntime(w http.ResponseWriter, r *http.Request) {
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
	clusterID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/clusters/"), "/runtime")
	clusterID = strings.Trim(clusterID, "/")
	if auth.TenantID == "" || clusterID == "" {
		respondJSON(w, http.StatusConflict, map[string]string{"error": "SCOPE_SELECTION_REQUIRED"})
		return
	}
	clusters, err := (&store.ClusterDAO{}).ListForTenant(auth.TenantID)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "CLUSTER_RUNTIME_UNAVAILABLE"})
		return
	}
	var target *store.Cluster
	for i := range clusters {
		if clusters[i].ClusterID == clusterID {
			target = &clusters[i]
			break
		}
	}
	if target == nil {
		// 未授权集群不泄露存在性，与平台总览保持同样的拒绝语义。
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "CLUSTER_NOT_FOUND"})
		return
	}
	localClusterID := strings.TrimSpace(os.Getenv("AIOPS_SYSTEM_CLUSTER_ID"))
	now := time.Now().UTC()

	networkGap := signalGapFact{
		Quality: "not_connected",
		Reason:  "未接入网络吞吐/错误/丢包/时延指标来源；不显示为 0 或健康",
	}
	storageGap := signalGapFact{
		Quality: "not_connected",
		Reason:  "未接入存储使用率/容量/IO 时延指标来源；不显示为 0 或健康",
	}

	if localClusterID == "" || clusterID != localClusterID {
		respondJSON(w, http.StatusOK, clusterRuntimeResponse{
			ClusterID:   clusterID,
			Name:        target.Name,
			Network:     networkGap,
			Storage:     storageGap,
			Quality:     "not_connected",
			QualityNote: "该集群未接入中央运行时读取通道，无法给出 Pod 就绪与重启事实",
			GeneratedAt: now.Format(time.RFC3339),
			Source:      "core/v1 nodes + core/v1 pods (in-cluster)",
		})
		return
	}

	podData, podErr := k8sAPIFn("/api/v1/pods")
	if podErr != nil {
		respondJSON(w, http.StatusOK, clusterRuntimeResponse{
			ClusterID:   clusterID,
			Name:        target.Name,
			Network:     networkGap,
			Storage:     storageGap,
			Quality:     "failed",
			QualityNote: "读取 Pod 列表失败：" + podErr.Error(),
			GeneratedAt: now.Format(time.RFC3339),
			Source:      "core/v1 nodes + core/v1 pods (in-cluster)",
		})
		return
	}
	pods, restarts, parsed := parsePodRuntime(podData)
	if !parsed {
		respondJSON(w, http.StatusOK, clusterRuntimeResponse{
			ClusterID:   clusterID,
			Name:        target.Name,
			Network:     networkGap,
			Storage:     storageGap,
			Quality:     "failed",
			QualityNote: "Pod 列表解析失败，未产生任何就绪或重启结论",
			GeneratedAt: now.Format(time.RFC3339),
			Source:      "core/v1 nodes + core/v1 pods (in-cluster)",
		})
		return
	}

	// 质量说明必须列出实际触发原因，便于用户判断而不是笼统"异常"。
	quality := "healthy"
	reasons := make([]string, 0, 3)
	if pods.Pending > 0 {
		reasons = append(reasons, "存在 Pending Pod（调度未完成）")
	}
	if pods.NotReady > 0 {
		reasons = append(reasons, "存在容器未就绪的 Running Pod")
	}
	if pods.Failed > 0 {
		reasons = append(reasons, "存在 Failed Pod（可能为 Job 正常退避，需结合任务判断）")
	}
	note := ""
	if len(reasons) > 0 {
		quality = "partial"
		note = strings.Join(reasons, "；") + "。集群整体状态不能按健康呈现。"
	}
	respondJSON(w, http.StatusOK, clusterRuntimeResponse{
		ClusterID:   clusterID,
		Name:        target.Name,
		Pods:        pods,
		Restarts:    restarts,
		Network:     networkGap,
		Storage:     storageGap,
		Quality:     quality,
		QualityNote: note,
		GeneratedAt: now.Format(time.RFC3339),
		Source:      "core/v1 nodes + core/v1 pods (in-cluster)",
	})
}
