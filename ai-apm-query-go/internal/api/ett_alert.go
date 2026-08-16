package api

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── 容量 ETT 联动告警（P3-3b）──
// 复用 capacity.go 的容量 PromQL 与 EstimateTimeToThreshold 计算：
// 对每个已纳管节点（node-exporter instance）× 容量指标计算 ETT（预计触达阈值时间），
// ETT ≤ 阈值小时数（env ETT_ALERT_HOURS，默认 72h）时生成 warning 告警事件
// （rule_id 固定 capacity-ett-imminent，写入 alertEvents 内存并 saveAlertEvents 落库）。
// 资源恢复（ETT > 阈值或无命中）时该资源此前 firing 的事件自动 resolved。
// 检查随现有 60s 评估循环运行，但放入独立 goroutine + 超时，不阻塞告警主循环。

// ettAlertRuleID 容量 ETT 告警的固定规则 ID。
const ettAlertRuleID = "capacity-ett-imminent"

// ettAlertService 容量 ETT 告警的事件服务维度。
const ettAlertService = "capacity"

// ettAlertStep / ettAlertHistoryHours / ettAlertHorizon：复用容量页默认参数。
// step=300s、历史 24h → 288 个数据点；horizon=1000 步可覆盖 1000×300s≈83h，
// 足以探测 ETT≤72h 的触达（capacity.go 的 horizon 上限同为 1000）。
const (
	ettAlertStep         = 300
	ettAlertHistoryHours = 24
	ettAlertHorizon      = 1000
	ettAlertTimeout      = 60 * time.Second
)

// ettMetricSpec 容量 ETT 告警覆盖的指标与默认阈值（复用容量页默认 threshold=80；
// network 在容量页要求显式阈值，这里统一用默认 80）。
type ettMetricSpec struct {
	Metric    string
	Threshold float64
}

var ettAlertMetrics = []ettMetricSpec{
	{"cpu", 80},
	{"memory", 80},
	{"disk", 80},
	{"network", 80},
}

// ettAlertHours 返回 ETT 告警阈值小时数（env ETT_ALERT_HOURS，默认 72）。
func ettAlertHours() int {
	if v := os.Getenv("ETT_ALERT_HOURS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 72
}

// capacityETTResult 单个实例 × 指标的 ETT 检查结果。
type capacityETTResult struct {
	Metric     string
	Instance   string
	ETTSeconds int64
}

// evaluateInstanceETT 计算单个实例 × 指标的 ETT（复用 capacity.go 的
// capacityPromQLForCluster / LinearRegression / EstimateTimeToThreshold）。
// 无数据点、查询失败或 horizon 内未触达阈值时返回 nil（无数据不误报）。
func (h *Handler) evaluateInstanceETT(metric string, threshold float64, instance string) *capacityETTResult {
	promQL := capacityPromQLForCluster(metric, instance, "")
	if promQL == "" {
		return nil
	}
	end := time.Now().Unix()
	start := end - int64(ettAlertHistoryHours*3600)
	series, err := h.vmRangeQuery(promQL, start, end, ettAlertStep)
	if err != nil || len(series) == 0 {
		return nil
	}
	slope, intercept := LinearRegression(series)
	n := len(series)
	k, hit := EstimateTimeToThreshold(slope, intercept, n, ettAlertHorizon, threshold)
	if !hit || k <= 0 {
		return nil
	}
	return &capacityETTResult{
		Metric:     metric,
		Instance:   instance,
		ETTSeconds: int64(k) * ettAlertStep,
	}
}

// checkCapacityETTAlerts 执行一轮容量 ETT 联动告警检查：
//   - ETT ≤ 阈值小时数 → 生成/聚合 firing warning 事件（rule_id=capacity-ett-imminent）
//   - ETT > 阈值小时数（已恢复）→ 该资源此前 firing 的事件自动 resolved
//   - 无数据点/查询失败/VM 不可用 → 跳过（不误报也不误恢复）
func (h *Handler) checkCapacityETTAlerts() {
	alertHours := ettAlertHours()
	limit := int64(alertHours) * 3600

	instances, err := h.vmInstanceLabels(`up{job="node-exporter"}`)
	if err != nil || len(instances) == 0 {
		// VM 不可用或无 node-exporter：无数据不误报（也不误恢复）
		return
	}

	// 本轮 ETT 结果：key = metric|instance → ETT 秒数（仅保留 ≤limit 的命中项）
	imminent := map[string]int64{}
	for _, m := range ettAlertMetrics {
		for _, inst := range instances {
			if res := h.evaluateInstanceETT(m.Metric, m.Threshold, inst); res != nil && res.ETTSeconds <= limit {
				imminent[res.Metric+"|"+res.Instance] = res.ETTSeconds
			}
		}
	}

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	alertEventsMu.Lock()
	resolvedAny := false

	// 恢复：对每个资源（metric|instance），若本轮 ETT 不再 imminent（>limit 或无命中）
	// 且存在 firing 事件 → 自动 resolved（按 rule_id+service 匹配，签名区分具体资源）。
	for _, m := range ettAlertMetrics {
		for _, inst := range instances {
			key := m.Metric + "|" + inst
			if _, ok := imminent[key]; ok {
				continue // 仍 imminent，保持 firing（或本轮下方更新）
			}
			sig := eventSignature(ettAlertRuleID, ettAlertService, key)
			for i := range alertEvents {
				e := &alertEvents[i]
				if e.RuleID == ettAlertRuleID && e.Service == ettAlertService && e.Signature == sig && e.Status == "firing" {
					transitionStatus(e, "resolved", "system")
					appendTimeline(e, "recovered", "system")
					resolvedAny = true
				}
			}
		}
	}

	// 触发：ETT ≤ limit → 生成/聚合 firing warning 事件。
	// 窗口内已有同 (rule_id, service, signature) firing 事件 → 仅更新计数/时间（降噪）。
	for key, ett := range imminent {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) != 2 {
			continue
		}
		metric, instance := parts[0], parts[1]
		sig := eventSignature(ettAlertRuleID, ettAlertService, key)

		var existing *AlertEvent
		for i := range alertEvents {
			e := &alertEvents[i]
			if e.RuleID == ettAlertRuleID && e.Service == ettAlertService && e.Signature == sig && e.Status == "firing" {
				if t, err := time.Parse(time.RFC3339, e.LastTimestamp); err == nil && now.Sub(t) <= alertGroupInterval {
					existing = e
					break
				}
			}
		}
		if existing != nil {
			existing.Count++
			existing.LastTimestamp = nowStr
			continue
		}

		event := AlertEvent{
			ID:             generateID(),
			RuleID:         ettAlertRuleID,
			RuleName:       "容量预测 ETT 即将触达阈值",
			Service:        ettAlertService,
			Severity:       "warning",
			Object:         instance,
			Message:        fmt.Sprintf("容量预测: %s(%s) 预计 %.1f 小时内触达阈值 %.0f%%", metric, instance, float64(ett)/3600, thresholdOf(metric)),
			Value:          float64(ett),
			Threshold:      float64(limit),
			Timestamp:      nowStr,
			Count:          1,
			FirstTimestamp: nowStr,
			LastTimestamp:  nowStr,
			Status:         "firing",
			Signature:      sig,
		}
		appendTimeline(&event, "created", "system")
		alertEvents = append(alertEvents, event)
		if len(alertEvents) > maxAlertEvents {
			alertEvents = alertEvents[len(alertEvents)-maxAlertEvents:]
		}
	}
	alertEventsMu.Unlock()

	if len(imminent) > 0 || resolvedAny {
		saveAlertEvents()
		log.Printf("CAPACITY_ETT: %d 资源 ETT 即将触达阈值（≤%dh），resolved=%v", len(imminent), alertHours, resolvedAny)
	}
}

// thresholdOf 返回指标对应的告警阈值（与 ettAlertMetrics 一致）。
func thresholdOf(metric string) float64 {
	for _, m := range ettAlertMetrics {
		if m.Metric == metric {
			return m.Threshold
		}
	}
	return 80
}

var (
	capacityETTCheckRunning bool
	capacityETTCheckMu      sync.Mutex
)

// launchCapacityETTCheck 在独立 goroutine 中执行容量 ETT 检查（带超时），
// 不阻塞告警主循环。上一轮未结束时跳过本轮，避免检查堆积。
func (h *Handler) launchCapacityETTCheck() {
	capacityETTCheckMu.Lock()
	if capacityETTCheckRunning {
		capacityETTCheckMu.Unlock()
		return
	}
	capacityETTCheckRunning = true
	capacityETTCheckMu.Unlock()

	go func() {
		defer func() {
			capacityETTCheckMu.Lock()
			capacityETTCheckRunning = false
			capacityETTCheckMu.Unlock()
		}()
		done := make(chan struct{})
		go func() {
			h.checkCapacityETTAlerts()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(ettAlertTimeout):
			log.Printf("capacity ETT alert check timed out after %v", ettAlertTimeout)
		}
	}()
}
