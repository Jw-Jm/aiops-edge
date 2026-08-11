package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LinearRegression 用最小二乘对 (x=0..n-1, y) 拟合 y = intercept + slope*x。
// n<2 或分母为 0 时返回 (0, mean(series))。
func LinearRegression(series []float64) (slope, intercept float64) {
	n := len(series)
	if n == 0 {
		return 0, 0
	}
	if n < 2 {
		return 0, series[0]
	}
	var sumX, sumY, sumXY, sumX2 float64
	for i, y := range series {
		x := float64(i)
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}
	denom := float64(n)*sumX2 - sumX*sumX
	if denom == 0 {
		return 0, mean(series)
	}
	slope = (float64(n)*sumXY - sumX*sumY) / denom
	intercept = (sumY - slope*sumX) / float64(n)
	return slope, intercept
}

// EWMA 指数加权平滑：s[0]=y[0]，s[t]=alpha*y[t]+(1-alpha)*s[t-1]。
// alpha<=0 || alpha>1 时回退默认 0.3。
func EWMA(series []float64, alpha float64) []float64 {
	if alpha <= 0 || alpha > 1 {
		alpha = 0.3
	}
	out := make([]float64, len(series))
	if len(series) == 0 {
		return out
	}
	out[0] = series[0]
	for t := 1; t < len(series); t++ {
		out[t] = alpha*series[t] + (1-alpha)*out[t-1]
	}
	return out
}

// EstimateTimeToThreshold 沿线性回归预测 y(x)=intercept+slope*x 求未来首个 y>=threshold 的步数。
// 未来 x 取 n-1+1 .. n-1+horizon。达到则返回 (k, true)（k 为第几个未来点，从 1 起）；
// 否则返回 (0, false)。
func EstimateTimeToThreshold(slope, intercept float64, n, horizon int, threshold float64) (int, bool) {
	for k := 1; k <= horizon; k++ {
		x := float64(n - 1 + k)
		y := intercept + slope*x
		if y >= threshold {
			return k, true
		}
	}
	return 0, false
}

// capacityPromQL 返回指定资源维度的 range 查询 PromQL。未知 metric 返回空串。
// instance 非空时按精确 instance 标签过滤（如 node-exporter 的 192.168.139.2:9100）。
func capacityPromQL(metric, instance string) string {
	// instPart：用于需要多 label 的维度，作为 ", instance=\"x\"" 追加进 {mode="idle", instance="x"}
	// 安全：instance 来自用户输入，须转义 PromQL 标签值（\ 与 "），防 PromQL 注入。
	instPart := ""
	// instSel：用于只有 instance 一个 label 的维度，独立 {instance="x"}
	instSel := ""
	if instance != "" {
		esc := strings.ReplaceAll(strings.ReplaceAll(instance, `\`, `\\`), `"`, `\"`)
		instPart = fmt.Sprintf(`, instance="%s"`, esc)
		instSel = fmt.Sprintf(`{instance="%s"}`, esc)
	}
	switch metric {
	case "cpu":
		// node_cpu_seconds_total 每个 mode 各一条 series，必须加 mode="idle" 才能正确计算使用率；
		// instance 与 mode 必须在同一对大括号内（逗号分隔），不能嵌套大括号
		return fmt.Sprintf(`100 - avg(rate(node_cpu_seconds_total{mode="idle"%s}[5m])) * 100`, instPart)
	case "memory":
		// avg 聚合所有节点/维度为单 series，避免 vmRangeQuery 拼接多条 series
		return fmt.Sprintf(`avg(100 * (1 - node_memory_MemAvailable_bytes%s / node_memory_MemTotal_bytes%s))`, instSel, instSel)
	case "disk":
		return fmt.Sprintf(`avg(1 - node_filesystem_avail_bytes%s / node_filesystem_size_bytes%s) * 100`, instSel, instSel)
	case "network":
		// sum 聚合所有网卡接口为单 series（总带宽），避免 vmRangeQuery 拼接多网卡 series
		return fmt.Sprintf(`sum(rate(node_network_receive_bytes_total%s[5m]) + rate(node_network_transmit_bytes_total%s[5m]))`, instSel, instSel)
	}
	return ""
}

// CapacityForecast 处理 GET /api/v1/capacity/forecast。
// 参数：metric(cpu|memory|disk|network)、instance、hours、step、horizon、threshold。
func (h *Handler) CapacityForecast(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	metric := q.Get("metric")
	instance := q.Get("instance")
	hours := parseIntDefault(q.Get("hours"), 24)
	step := parseIntDefault(q.Get("step"), 300)
	horizon := parseIntDefault(q.Get("horizon"), 12)
	thresholdStr := q.Get("threshold")

	if metric == "" {
		respondError(w, http.StatusBadRequest, "metric is required")
		return
	}
	if capacityPromQL(metric, "") == "" {
		respondError(w, http.StatusBadRequest, "invalid metric, must be cpu|memory|disk|network")
		return
	}
	if hours <= 0 || step <= 0 || horizon <= 0 {
		respondError(w, http.StatusBadRequest, "hours, step, horizon must be positive")
		return
	}
	// 上限防护：防超大 horizon 内存放大、hours*3600 int 溢出、step 过小产生巨量数据点
	if hours > 168 || step < 15 || step > 86400 || horizon > 1000 {
		respondError(w, http.StatusBadRequest, "hours must be <=168, step in [15,86400], horizon <=1000")
		return
	}
	if metric == "network" && thresholdStr == "" {
		respondError(w, http.StatusBadRequest, "threshold is required for network")
		return
	}
	threshold := 80.0
	if thresholdStr != "" {
		v, err := strconv.ParseFloat(thresholdStr, 64)
		if err != nil || v <= 0 {
			respondError(w, http.StatusBadRequest, "invalid threshold")
			return
		}
		threshold = v
	}

	end := time.Now().Unix()
	start := end - int64(hours*3600)
	series, err := h.vmRangeQuery(capacityPromQL(metric, instance), start, end, step)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "query failed: "+err.Error())
		return
	}
	n := len(series)
	if n == 0 {
		// 无历史数据：返回空历史/预测，前端展示"暂无数据"
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"metric": metric, "instance": instance, "threshold": threshold,
			"current": 0, "change_pct": 0, "timestamps": []int64{}, "history": []float64{},
			"forecasts": map[string]interface{}{
				"linear": map[string]interface{}{"values": []float64{}, "ett_seconds": 0, "within_horizon": false, "already_breached": false},
				"ewma":   map[string]interface{}{"values": []float64{}, "ett_seconds": 0, "within_horizon": false, "already_breached": false},
			},
		})
		return
	}

	// 历史 + 预测完整时间戳
	now := time.Now().Unix()
	timestamps := make([]int64, 0, n+horizon)
	base := now - int64(n-1)*int64(step)
	for i := 0; i < n+horizon; i++ {
		timestamps = append(timestamps, base+int64(i)*int64(step))
	}

	current := series[n-1]
	// change_pct：对比 24h 前同相位（step 为秒，86400/step 即 24h 的样本数），
	// 数据不足 24h 时降级为对比近端均值（最后 10% 样本）——避免受周期峰谷/早期异常点影响。
	changePct := 0.0
	phaseStep := 86400 / step
	var baseVal float64
	if n > phaseStep+1 && series[n-1-phaseStep] != 0 {
		baseVal = series[n-1-phaseStep]
	} else {
		startIdx := n - n/10
		if startIdx < 0 {
			startIdx = 0
		}
		sum := 0.0
		for i := startIdx; i < n; i++ {
			sum += series[i]
		}
		if startIdx < n {
			baseVal = sum / float64(n-startIdx)
		}
	}
	if baseVal != 0 {
		changePct = (current - baseVal) / baseVal * 100
	}

	// 线性回归预测：全窗口最小二乘，输出预测曲线 + ETT
	slope, intercept := LinearRegression(series)
	linearValues := make([]float64, horizon)
	for k := 1; k <= horizon; k++ {
		linearValues[k-1] = intercept + slope*float64(n-1+k)
	}
	linearETT, linearHit := EstimateTimeToThreshold(slope, intercept, n, horizon, threshold)
	linearBreached := current >= threshold

	// 真实 EWMA 预测：平滑序列末值 + 末段平滑斜率外推（而非全窗口回归）。
	// EWMA 体现"近期趋势持续"且对噪声平滑，与外推起点严格贴合。
	smoothed := EWMA(series, 0.3)
	ewmaTail := smoothed
	if len(smoothed) > 4 {
		ewmaTail = smoothed[len(smoothed)-4:]
	}
	ewmaSlope, _ := LinearRegression(ewmaTail)
	ewmaBase := smoothed[n-1]
	ewmaValues := make([]float64, horizon)
	for k := 1; k <= horizon; k++ {
		ewmaValues[k-1] = ewmaBase + ewmaSlope*float64(k)
	}
	// ETT 与外推直线一致：直线 y=ewmaSlope*x + b 过点 (n-1, ewmaBase) → b=ewmaBase-ewmaSlope*(n-1)
	ewmaETT, ewmaHit := EstimateTimeToThreshold(ewmaSlope, ewmaBase-ewmaSlope*float64(n-1), n, horizon, threshold)
	ewmaBreached := current >= threshold

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"metric": metric, "instance": instance, "threshold": threshold,
		"current": current, "change_pct": changePct,
		"timestamps": timestamps, "history": series,
		"forecasts": map[string]interface{}{
			"linear": map[string]interface{}{
				"values": linearValues, "ett_seconds": linearETT * step,
				"within_horizon": linearHit, "already_breached": linearBreached,
			},
			"ewma": map[string]interface{}{
				"values": ewmaValues, "ett_seconds": ewmaETT * step,
				"within_horizon": ewmaHit, "already_breached": ewmaBreached,
			},
		},
	})
}

// parseIntDefault 解析正整数参数，失败或<=0返回默认值。
func parseIntDefault(s string, def int) int {
	v, err := strconv.Atoi(s)
	if err != nil || v <= 0 {
		return def
	}
	return v
}

// vmInstanceLabels 查 VM 即时查询结果中的 instance 标签并去重排序。
func (h *Handler) vmInstanceLabels(promQL string) ([]string, error) {
	if h.vmURL == "" {
		return nil, fmt.Errorf("victoria-metrics not configured")
	}
	u, _ := url.Parse(h.vmURL + "/api/v1/query")
	q := u.Query()
	q.Set("query", promQL)
	u.RawQuery = q.Encode()
	req, _ := http.NewRequest(http.MethodGet, u.String(), nil)
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	// 显式检查状态码：VM 错误响应可能带 JSON body，若跳过会静默吞掉 500 错误
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("VM query returned %d: %s", resp.StatusCode, string(body)[:min(len(body), 200)])
	}
	var r struct {
		Data struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
			} `json:"result"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &r) != nil {
		return nil, fmt.Errorf("invalid VM response")
	}
	seen := map[string]bool{}
	for _, item := range r.Data.Result {
		if inst := item.Metric["instance"]; inst != "" && !seen[inst] {
			seen[inst] = true
		}
	}
	out := make([]string, 0, len(seen))
	for inst := range seen {
		out = append(out, inst)
	}
	sort.Strings(out)
	return out, nil
}

// CapacityInstances 处理 GET /api/v1/capacity/instances，返回 node-exporter 的实例列表。
// 供前端容量预测页的 node 选择器使用；值为 VM 中真实的 instance 标签（如 192.168.139.2:9100）。
// P2-1 修复：若 VM 无 node-exporter 实例（metrics 未接入），回退到从 kubectl 读取真实节点列表，
// 确保容量页节点选择器始终有可选项。
func (h *Handler) CapacityInstances(w http.ResponseWriter, r *http.Request) {
	labels, err := h.vmInstanceLabels(`up{job="node-exporter"}`)
	// VM 查询失败或无实例时回退到 K8s 节点（真实可观测目标）
	if err != nil || len(labels) == 0 {
		fallback := []string{}
		for _, n := range k8sNodes() {
			if n.Name != "" {
				fallback = append(fallback, n.Name)
			}
		}
		if len(fallback) > 0 {
			respondJSON(w, http.StatusOK, map[string]interface{}{
				"instances": fallback,
				"count":     len(fallback),
				"source":    "k8s",
			})
			return
		}
		if err != nil {
			respondError(w, http.StatusInternalServerError, "query failed: "+err.Error())
			return
		}
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"instances": labels,
		"count":     len(labels),
		"source":    "victoriametrics",
	})
}
