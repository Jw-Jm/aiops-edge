package api

import (
	"fmt"
	"log"
	"strings"
	"time"
)

func (h *Handler) StartAlertEvaluation() {
	go func() {
		// Run once immediately on startup
		h.evaluateAlerts()
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			h.evaluateAlerts()
		}
	}()
	log.Println("Alert evaluation loop started (every 60s)")
}

func (h *Handler) evaluateAlerts() {
	alertRulesMu.RLock()
	rules := make([]AlertRule, len(alertRules))
	copy(rules, alertRules)
	alertRulesMu.RUnlock()

	// 记录每条规则最近触发时间（cooldown 冷却）与连续 breach 次数（dampening）
	lastRuleTrigger := make(map[string]time.Time)
	ruleStreak := make(map[string]int)

	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}

		value, err := h.evaluateRule(rule)
		if err != nil {
			log.Printf("evaluate rule %s (%s): %v", rule.ID, rule.Name, err)
			continue
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

		if breached {
			// 告警降噪：先检查是否被静默抑制
			if isSilenced(rule.Service, rule.ID) {
				log.Printf("ALERT_SILENCED: %s | %s | %s (value=%.2f)", rule.Severity, rule.Service, rule.Name, value)
				continue
			}

			now := time.Now().UTC()
			nowStr := now.Format(time.RFC3339)

			// cooldown 触发冷却：冷却期内不重复告警（即使窗口外）
			if t, ok := lastRuleTrigger[rule.ID]; ok && inCooldown(rule, t, now) {
				continue
			}

			// dampening 连续确认：连续 breach 次数未达阈值则暂不告警
			ruleStreak[rule.ID]++
			if !shouldAlertAfterDampening(rule, ruleStreak[rule.ID]) {
				log.Printf("ALERT_DAMPENED: %s | %s (streak=%d/%d)", rule.Service, rule.Name, ruleStreak[rule.ID], rule.Dampening)
				continue
			}
			lastRuleTrigger[rule.ID] = now

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
				msg := fmt.Sprintf("%s: %s %.2f > threshold %.2f", rule.Name, rule.Metric, value, rule.Threshold)
				if rule.Service == "kubernetes" || strings.HasPrefix(rule.ID, "k8s-") {
					if objs := k8sAlertObjects(rule.ID); objs != "" {
						msg += fmt.Sprintf(" | 对象: %s", objs)
					}
				}
				event := AlertEvent{
					ID:             generateID(),
					RuleID:         rule.ID,
					RuleName:       rule.Name,
					Service:        rule.Service,
					Object:         k8sAlertObjects(rule.ID), // 修复：新建事件时把告警对象名直接存到 Object 字段

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
			// 未 breach：重置连续计数（dampening）
			ruleStreak[rule.ID] = 0
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

