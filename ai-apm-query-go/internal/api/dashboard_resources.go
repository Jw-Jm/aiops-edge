package api

import (
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

	// 节点数：cluster_id=default 映射主集群（id=1），其余按 name 匹配；all 时累加
	nodeCount := 0
	if clusters, err := (&store.ClusterDAO{}).List(); err == nil {
		for _, c := range clusters {
			if cid == "all" {
				nodeCount += c.NodeCount
			} else if (cid == "default" && c.ID == 1) || c.Name == cid {
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
		promQL := capacityPromQLForCluster(m.metric, "", cid)
		if promQL == "" {
			continue
		}
		series, err := h.vmRangeQuery(promQL, start, end, step)
		thr, _ := strconv.ParseFloat(m.threshold, 64)
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
