package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// DashboardResources handles GET /api/v1/dashboard/resources?cluster_id=X
// 工作台集群资源卡：CPU/内存/磁盘当前使用率 + 阈值 + 预计触达时间 + 节点数。
// 复用容量预测的 PromQL 与回归（LinearRegression），按 cluster 标签过滤。
func (h *Handler) DashboardResources(w http.ResponseWriter, r *http.Request) {
	cid := r.URL.Query().Get("cluster_id")
	if cid == "" {
		cid = "all"
	}

	// 节点数：P3.8 移除 default→id=1 映射（V9.2 §9）。仅按 name/slug 匹配，或 all 累加。
	nodeCount := 0
	if clusters, err := (&store.ClusterDAO{}).List(); err == nil {
		for _, c := range clusters {
			if cid == "all" {
				nodeCount += c.NodeCount
			} else if c.Name == cid || c.Slug == cid {
				nodeCount = c.NodeCount
				break
			}
		}
	}

	// 三指标：阈值默认 CPU 80 / 内存 90 / 磁盘 85（与容量页一致）
	type mc struct{ metric, threshold string }
	metrics := []mc{{"cpu", "80"}, {"memory", "90"}, {"disk", "85"}}
	end := time.Now().Unix()
	start := end - 3600 // 近 1h 采样，足够计算趋势与 ETT
	step := 300

	items := []map[string]interface{}{}
	for _, m := range metrics {
		thr, _ := strconv.ParseFloat(m.threshold, 64)

		// 修复(P2-5)：内存口径与 nodes/metrics 统一（K8s metrics-server usage/capacity）。
		// 此前工作台走 PromQL MemAvailable 口径（不含 cache）与节点页（含 cache）数字不一致，
		// 现直接复用节点页数据源，保证两页内存数字一致。CPU/磁盘保持 PromQL。
		if m.metric == "memory" {
			cur, err := clusterMemoryUsagePct()
			if err != nil {
				items = append(items, map[string]interface{}{
					"metric": m.metric, "current": nil, "threshold": thr, "ett_seconds": 0,
				})
				continue
			}
			// 单点采样无趋势：ETT 为 0（与节点页口径一致，不再用 PromQL 趋势外推）
			items = append(items, map[string]interface{}{
				"metric": m.metric, "current": round2(cur), "threshold": thr, "ett_seconds": 0, "source": "k8s-api",
			})
			continue
		}

		promQL := capacityPromQLForScope(m.metric, "", cid, extractTenantID(r))
		if promQL == "" {
			continue
		}
		series, err := h.vmRangeQuery(promQL, start, end, step)
		if err != nil || len(series) == 0 {
			items = append(items, map[string]interface{}{
				"metric": m.metric, "current": nil, "threshold": thr, "ett_seconds": 0,
			})
			continue
		}
		current := series[len(series)-1]
		// 线性外推 ETT：斜率>0 时预估达到阈值的时间；已超阈值返回 0
		ett := 0
		slope, _ := LinearRegression(series)
		if current >= thr {
			ett = 0
		} else if slope > 0 {
			ett = int((thr - current) / slope * float64(step))
		}
		items = append(items, map[string]interface{}{
			"metric": m.metric, "current": current, "threshold": thr, "ett_seconds": ett,
		})
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"cluster_id": cid,
		"node_count": nodeCount,
		"resources":  items,
	})
}

// clusterMemoryUsagePct 用与 nodes/metrics 相同的口径（K8s metrics-server usage / capacity）
// 计算集群平均内存使用率（%），确保工作台与节点页内存数字一致（P2-5 修复）。
func clusterMemoryUsagePct() (float64, error) {
	data, err := k8sAPIFn("/apis/metrics.k8s.io/v1beta1/nodes")
	if err != nil {
		return 0, err
	}
	capMap := map[string]map[string]string{}
	if nd, nerr := k8sAPIFn("/api/v1/nodes"); nerr == nil {
		for _, n := range parseNodes(nd) {
			name, _ := n["name"].(string)
			cpu, _ := n["cpu"].(string)
			mem, _ := n["memory"].(string)
			if name != "" {
				capMap[name] = map[string]string{"cpu": cpu, "memory": mem}
			}
		}
	}
	nodes := parseNodeMetrics(data, capMap)
	if len(nodes) == 0 {
		return 0, fmt.Errorf("no node memory metrics")
	}
	sum := 0.0
	for _, n := range nodes {
		sum += toFloat(n["mem_usage_pct"])
	}
	return sum / float64(len(nodes)), nil
}
