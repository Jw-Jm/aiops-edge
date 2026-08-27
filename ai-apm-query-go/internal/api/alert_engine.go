package api

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

var (
	// 记录每条规则最近触发时间（cooldown 冷却）与连续 breach 次数（dampening）。
	// 修复(数据 P1-7)：此前是 evaluateAlerts 的局部变量，每轮评估重置导致
	// cooldown/dampening 永久失效（冷却期从不为真、streak 永远为 1）；
	// 提升为包级持久（进程内），跨评估轮生效。
	lastRuleTrigger = map[string]time.Time{}
	ruleStreak      = map[string]int{}
	ruleStateMu     sync.Mutex
)

// alertLeaderHolder 是本进程的 alert-eval Leader 身份（holder_id/epoch/token）。
// C-03：多 alert-eval pod 只有一个 Leader 评估；非 Leader 进程等待但不评估（避免重复事件）。
var (
	alertLeaderHolderID = "query-api-" + randomSuffix()
	alertLeaderEpoch    int64
	alertLeaderToken    string
)

func (h *Handler) StartAlertEvaluation() {
	go func() {
		// C-03：先获取/续约 Leader 租约（MySQL alert_eval_leader），再决定是否评估。
		// Leader 身份跨进程持久；每次评估前 Renew + IsLeader，过期则让出。
		acquire := func() bool {
			if h.alertLeaderDAO == nil {
				return true // DAO 不可用（本机/sqlmock）→ 单进程评估（兼容）
			}
			epoch, token, isLeader, err := h.alertLeaderDAO.Acquire(alertLeaderHolderID)
			if err != nil {
				log.Printf("alert leader acquire: %v", err)
				return false
			}
			alertLeaderEpoch, alertLeaderToken = epoch, token
			return isLeader
		}
		if acquire() {
			h.evaluateAlerts()
		} else {
			log.Printf("alert-eval: not leader, waiting for leader lease")
		}
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			// 续约 + 确认 Leader；非 Leader 不评估（避免多 pod 重复告警）。
			if h.alertLeaderDAO != nil && alertLeaderEpoch > 0 && alertLeaderToken != "" {
				if _, err := h.alertLeaderDAO.Renew(alertLeaderHolderID, alertLeaderEpoch, alertLeaderToken); err != nil {
					log.Printf("alert leader renew failed: %v; re-acquiring", err)
					if !acquire() {
						continue
					}
				}
			} else if !acquire() {
				continue
			}
			h.evaluateAlerts()
		}
	}()
	log.Println("Alert evaluation loop started (every 60s, single-leader via MySQL lease)")
}

func randomSuffix() string {
	// 简短随机后缀区分多 pod holder_id（不含密码/敏感信息）。
	// 用时间戳 + 进程内计数器近似；真实场景由 Deployment pod name 覆盖。
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// isK8sRule 判断规则是否属于 K8s 类（评估时实时打 K8s API）。
// 此类规则走独立并发分组（上限 2），避免并发打爆 K8s API。
func isK8sRule(rule AlertRule) bool {
	switch rule.Metric {
	case "pod_restarts", "pod_pending_minutes", "node_pressure",
		"unavailable_replicas", "pvc_usage_percent", "oom_count":
		return true
	}
	return false
}

// ruleEvalResult 并行评估阶段单条规则的计算结果（纯计算，不含事件生成）。
type ruleEvalResult struct {
	value    float64
	breached bool
	err      error
}

// computeRuleBreach 并行计算一条规则的 value 与 breach 判定。
// 只读外部数据源（VM/CH/K8s/MySQL SLO），不修改 alertEvents/ruleState 等内存状态，
// 因此可安全并发执行；breach → 事件生成由调用方回收到单一 goroutine 顺序处理。
func (h *Handler) computeRuleBreach(rule AlertRule) ruleEvalResult {
	value, err := h.evaluateRule(rule)
	if err != nil {
		return ruleEvalResult{err: err}
	}

	// anomaly / burn_rate 有自己的统计/烧毁评估逻辑，不走外层 checkCondition（避免阈值语义冲突）。
	// threshold / mutation / forecast / metric_raw 等用 checkCondition 判定。
	breached := false
	if rule.Type != "anomaly" && rule.Type != "burn_rate" {
		breached = checkCondition(value, rule.Condition, rule.Threshold)
	}

	// Mutation detection: compare with historical window
	if !breached && rule.Type == "mutation" {
		historicalValue, err := h.evaluateRuleHistorical(rule)
		if err == nil && historicalValue > 0 {
			change := (value - historicalValue) / historicalValue * 100
			if change > 50 || change < -50 {
				breached = true
				log.Printf("MUTATION: %s changed %.1f%% (current=%.1f, historical=%.1f)", rule.Name, change, value, historicalValue)
			}
		}
	}

	// Anomaly detection: zscore/MAD 统计检测（当前值偏离历史序列均值/中位数）
	if !breached && rule.Type == "anomaly" {
		if current, anom := h.evaluateRuleAnomaly(rule); anom {
			breached = true
			log.Printf("ANOMALY: %s current=%.2f (method=%s)", rule.Name, current, rule.AnomalyMethod)
		}
	}

	// Forecast：基于历史窗口的偏差（简单线性外推 vs 实际）
	if !breached && rule.Type == "forecast" {
		if hist, err := h.evaluateRuleHistorical(rule); err == nil && hist > 0 {
			dev := (value - hist) / hist * 100
			limit := rule.Threshold
			if limit <= 0 {
				limit = 20
			}
			if dev > limit {
				breached = true
				log.Printf("FORECAST: %s dev %.1f%% (current=%.1f, window=%.1f)", rule.Name, dev, value, hist)
			}
		}
	}

	// Burn-rate：SLO 目标错误预算烧毁率（实际错误率 / 目标错误率）
	if !breached && rule.Type == "burn_rate" {
		if burn, ok := h.evaluateRuleBurnRate(rule, value); ok {
			breached = true
			log.Printf("BURN_RATE: %s burn=%.1f (slo=%s)", rule.Name, burn, rule.SLOID)
		}
	}

	return ruleEvalResult{value: value, breached: breached}
}

func (h *Handler) evaluateAlerts() {
	alertRulesMu.RLock()
	rules := make([]AlertRule, len(alertRules))
	copy(rules, alertRules)
	alertRulesMu.RUnlock()

	// P3-3b: 容量 ETT 联动告警。独立 goroutine + 超时执行，不阻塞本评估循环。
	h.launchCapacityETTCheck()

	// ── 并行评估阶段（P3-1a）──
	// 按规则类型分组：非 K8s 规则（threshold/anomaly/forecast/burn_rate/log/trace 类）
	// 并发评估（上限 8，避免打爆 VM/CH）；K8s 类规则（打实时 K8s API）独立分组、
	// 并发上限 2，避免打爆 K8s API。本阶段只做只读计算，不改任何内存状态。
	// 评估结果按规则原始顺序收集到 results，供下一阶段顺序处理。
	const (
		nonK8sMaxConcurrent = 8
		k8sMaxConcurrent    = 2
	)
	results := make([]ruleEvalResult, len(rules))
	nonK8sSem := make(chan struct{}, nonK8sMaxConcurrent)
	k8sSem := make(chan struct{}, k8sMaxConcurrent)
	var wg sync.WaitGroup
	for i, rule := range rules {
		if !rule.Enabled {
			continue
		}
		wg.Add(1)
		go func(idx int, r AlertRule) {
			defer wg.Done()
			if isK8sRule(r) {
				k8sSem <- struct{}{}
				defer func() { <-k8sSem }()
			} else {
				nonK8sSem <- struct{}{}
				defer func() { <-nonK8sSem }()
			}
			results[idx] = h.computeRuleBreach(r)
		}(i, rule)
	}
	wg.Wait()

	// ── 串行处理阶段 ──
	// 按规则原始顺序处理 breach → 事件生成（单一 goroutine 顺序执行，保证事件时序稳定）。
	// 事件生成涉及 alertEventsMu/ruleStateMu 与 CH 持久化，绝不能并发。
	for i, rule := range rules {
		if !rule.Enabled {
			continue
		}
		res := results[i]
		if res.err != nil {
			log.Printf("evaluate rule %s (%s): %v", rule.ID, rule.Name, res.err)
			continue
		}
		value := res.value
		breached := res.breached

		if breached {
			// 告警降噪：先检查是否被静默抑制
			if isSilenced(rule.Service, rule.ID) {
				log.Printf("ALERT_SILENCED: %s | %s | %s (value=%.2f)", rule.Severity, rule.Service, rule.Name, value)
				continue
			}

			now := time.Now().UTC()
			nowStr := now.Format(time.RFC3339)

			// cooldown 触发冷却：冷却期内不重复告警（即使窗口外）。
			// C-03：cooldown/dampening 持久化到 MySQL（alert_rule_runtime_state），
			// 进程内 map 只能当缓存——多 pod / 重启后不丢失冷却期与 streak。
			ruleStateMu.Lock()
			lastT, hasLast := lastRuleTrigger[rule.ID]
			if h.alertRuleStateDAO != nil {
				if st, err := h.alertRuleStateDAO.Get(rule.ID); err == nil && st != nil && st.LastTriggerAt != nil {
					lastT = *st.LastTriggerAt
					hasLast = true
				}
			}
			if hasLast && inCooldown(rule, lastT, now) {
				ruleStateMu.Unlock()
				continue
			}

			// dampening 连续确认：连续 breach 次数未达阈值则暂不告警
			streak := ruleStreak[rule.ID] + 1
			ruleStreak[rule.ID] = streak
			ruleStateMu.Unlock()
			if h.alertRuleStateDAO != nil {
				_ = h.alertRuleStateDAO.Upsert(store.AlertRuleRuntimeState{RuleID: rule.ID, BreachStreak: streak})
			}
			if !shouldAlertAfterDampening(rule, streak) {
				log.Printf("ALERT_DAMPENED: %s | %s (streak=%d/%d)", rule.Service, rule.Name, streak, rule.Dampening)
				continue
			}
			ruleStateMu.Lock()
			lastRuleTrigger[rule.ID] = now
			ruleStateMu.Unlock()
			if h.alertRuleStateDAO != nil {
				t := now
				_ = h.alertRuleStateDAO.Upsert(store.AlertRuleRuntimeState{
					RuleID: rule.ID, LastTriggerAt: &t, BreachStreak: streak,
				})
			}

			alertEventsMu.Lock()
			// 时间窗口聚合 + dedupe：窗口内已有同 (service, rule_id, signature) 事件则只更新计数/时间，不新增（降噪）
			sig := eventSignature(rule.ID, rule.Service, rule.Metric)
			var existing *AlertEvent
			for i := range alertEvents {
				e := &alertEvents[i]
				if e.RuleID == rule.ID && e.Service == rule.Service && e.Signature == sig {
					if t, err := time.Parse(time.RFC3339, e.LastTimestamp); err == nil {
						if now.Sub(t) <= alertGroupInterval {
							existing = e
							break
						}
					}
				}
			}

			if existing != nil {
				// 窗口内重复 → 聚合计数，不产生新事件
				existing.Count++
				existing.LastTimestamp = nowStr
				// 持续告警升级：超过 repeatInterval 仍未恢复 → 提升严重级别
				if first, err := time.Parse(time.RFC3339, existing.FirstTimestamp); err == nil {
					if now.Sub(first) > alertRepeatInterval && existing.Severity == rule.Severity && existing.Severity != "critical" {
						existing.Severity = escalateSeverity(existing.Severity)
						log.Printf("ALERT_ESCALATED: %s -> %s | %s", rule.Name, rule.Severity, existing.Severity)
					}
				}
			} else {
				// 窗口外新事件
				// Issue3: K8s 指标告警（Deployment 不可用 / OOM / Pod 重启等）在触发时把
				// 具体告警对象名写入 Message，避免事件只显示"count>threshold"而无对象。
				msg := buildAlertMessage(rule, value)
				if rule.Service == "kubernetes" || strings.HasPrefix(rule.ID, "k8s-") {
					if objs := k8sAlertObjects(rule.ID); objs != "" {
						msg += fmt.Sprintf(" | 对象: %s", objs)
					}
				}
				event := AlertEvent{
					ID:       generateID(),
					RuleID:   rule.ID,
					RuleName: rule.Name,
					Service:  rule.Service,
					Severity: rule.Severity,            // P0-2 修复：事件继承规则严重级别
					Cluster:  rule.Cluster,             // A-6 修复：事件继承规则集群标记
					Object:   k8sAlertObjects(rule.ID), // 修复：新建事件时把告警对象名直接存到 Object 字段

					Message:        msg,
					Value:          value,
					Threshold:      rule.Threshold,
					Timestamp:      nowStr,
					Count:          1,
					FirstTimestamp: nowStr,
					LastTimestamp:  nowStr,
					Status:         "firing",
					Signature:      sig,
				}
				alertEvents = append(alertEvents, event)
				if len(alertEvents) > maxAlertEvents {
					alertEvents = alertEvents[len(alertEvents)-maxAlertEvents:]
				}
				// Webhook 通知（仅新事件通知，聚合事件不重复通知）；优先规则级 webhook
				ruleCopy := rule
				appendTimeline(&event, "created", "system")
				h.sendWebhook(event, &ruleCopy)
				log.Printf("ALERT: %s | %s | %s | value=%.2f threshold=%.2f", rule.Severity, rule.Service, rule.Name, value, rule.Threshold)
			}
			alertEventsMu.Unlock()

			saveAlertEvents()
		} else {
			// 未 breach：重置连续计数（dampening）；C-03：同步重置到 MySQL。
			ruleStateMu.Lock()
			ruleStreak[rule.ID] = 0
			ruleStateMu.Unlock()
			if h.alertRuleStateDAO != nil {
				_ = h.alertRuleStateDAO.Upsert(store.AlertRuleRuntimeState{RuleID: rule.ID, BreachStreak: 0})
			}
			// 修复(误报)：指标已恢复正常，但之前触发过且仍为 firing 的事件应自动标记为 resolved，
			// 否则误报/已恢复的事件会一直停留在 firing 状态，前端持续展示"异常"。
			// 注意：saveAlertEvents 内部会再取 alertEventsMu.RLock，因此必须先在锁内
			// 完成状态修改、把 resolvedAny 记到本地变量，再在 Unlock 后调用 saveAlertEvents，
			// 避免写锁内再请求读锁导致死锁（与 breached 分支的处理顺序保持一致）。
			alertEventsMu.Lock()
			resolvedAny := false
			for i := range alertEvents {
				e := &alertEvents[i]
				if e.RuleID == rule.ID && e.Service == rule.Service && e.Status == "firing" {
					transitionStatus(e, "resolved", "system")
					appendTimeline(e, "recovered", "system")
					resolvedAny = true
				}
			}
			alertEventsMu.Unlock()
			if resolvedAny {
				saveAlertEvents()
				log.Printf("ALERT_RECOVERED: %s | %s 事件已标记为 resolved", rule.Service, rule.Name)
			}
		}
	}
}

// escalateSeverity 告警级别升级 warning→critical
func escalateSeverity(s string) string {
	if s == "critical" {
		return "critical"
	}
	if s == "warning" {
		return "critical"
	}
	return "critical"
}

// buildAlertMessage 按规则类型生成语义正确的告警消息：
// threshold 保留"值 > 阈值"格式；anomaly 说明 zscore/MAD 检测结果；
// burn_rate 展示烧毁倍数；其余类型回退阈值格式（P1-1 修复）。
func buildAlertMessage(rule AlertRule, value float64) string {
	switch rule.Type {
	case "anomaly":
		method := rule.AnomalyMethod
		if method == "" {
			method = "zscore"
		}
		return fmt.Sprintf("%s: %s 异常（%s=%.2f > 阈值 %.2f，偏离历史基线）",
			rule.Name, rule.Metric, method, value, rule.Threshold)
	case "burn_rate":
		return fmt.Sprintf("SLO 烧毁率告警: %s 烧毁率 %.1fx > 阈值 %.1fx",
			rule.Metric, value, rule.Threshold)
	default:
		return fmt.Sprintf("%s: %s %.2f > threshold %.2f", rule.Name, rule.Metric, value, rule.Threshold)
	}
}
