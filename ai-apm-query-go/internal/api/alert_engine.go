package api

import (
	"fmt"
	"log"
	"os"
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

	// Task 10 有界补偿扫描：启动后每 5 分钟对最近的 firing critical 告警
	// 重放同一条幂等调查服务。扫描失败只记日志，绝不影响告警权威写入。
	go func() {
		time.Sleep(30 * time.Second)
		service := NewAlertInvestigationService()
		for {
			applied := service.CompensationScan(100)
			if applied > 0 {
				log.Printf("alert-investigation compensation scan applied=%d", applied)
			}
			time.Sleep(5 * time.Minute)
		}
	}()
}

func randomSuffix() string {
	// 简短随机后缀区分多 pod holder_id（不含密码/敏感信息）。
	// 用时间戳 + 进程内计数器近似；真实场景由 Deployment pod name 覆盖。
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// ── PF-LOGIC-004：规则热加载 ──
// 此前规则仅在进程启动时（alerts.go init → loadAlertRules）加载一次，之后新建/修改的
// 规则不被评估，必须重启 pod 才生效。现在每个评估周期开始时从 MySQL 重新加载；
// 加载失败（MySQL 不可用 / 本机测试环境）保留内存规则继续本轮评估（fail-open 到
// 最近一次已知的好配置，不清空）。
func reloadAlertRulesFromStore() bool {
	rows, err := (&store.AlertRuleDAO{}).LoadAll()
	if err != nil {
		log.Printf("alert rule hot-reload skipped (keep in-memory rules): %v", err)
		return false
	}
	rules := make([]AlertRule, 0, len(rows))
	for _, r := range rows {
		rules = append(rules, AlertRule{
			ID: r.ID, Name: r.Name, Service: r.Service, Type: r.Type, Metric: r.Metric,
			Condition: r.Condition, Threshold: r.Threshold, Duration: r.Duration,
			Severity: r.Severity, Enabled: r.Enabled, WebhookURL: r.WebhookURL,
			Cooldown: r.Cooldown, Dampening: r.Dampening,
			BaselineSeconds: r.BaselineSeconds, AnomalyMethod: r.AnomalyMethod, SLOID: r.SLOID,
			Keyword: r.Keyword, Cluster: r.Cluster,
		})
	}
	alertRulesMu.Lock()
	alertRules = rules
	alertRulesMu.Unlock()
	return true
}

// ── PF-LOGIC-004：规则评估异常登记 ──
// UI 允许对任意 metric 建 threshold 规则，引擎遇到不认识的 metric 之前每分钟报错并
// 静默跳过（刷屏）。现在：unknown metric 的规则只 warn 一次并登记评估异常状态，
// 后续评估周期直接跳过；规则被修正（metric 变更或评估成功）后自动清除登记。
type RuleEvalIssue struct {
	RuleID string `json:"rule_id"`
	Metric string `json:"metric"`
	Reason string `json:"reason"` // "metric_not_found"
}

const ruleIssueMetricNotFound = "metric_not_found"

var (
	ruleEvalIssues   = map[string]RuleEvalIssue{}
	ruleEvalIssuesMu sync.Mutex
)

// markRuleEvalIssue 记录规则评估异常；同一规则的同一问题只记录/warn 一次（防刷屏）。
func markRuleEvalIssue(rule AlertRule, issue RuleEvalIssue) {
	ruleEvalIssuesMu.Lock()
	defer ruleEvalIssuesMu.Unlock()
	if prev, ok := ruleEvalIssues[rule.ID]; ok && prev == issue {
		return // 已登记过：不再重复 warn
	}
	ruleEvalIssues[rule.ID] = issue
	log.Printf("WARN[alert-engine]: 规则 %s (%s): %s (metric=%q) — 跳过评估，规则修正后自动恢复（仅提示一次）",
		rule.ID, rule.Name, issue.Reason, rule.Metric)
}

// clearRuleEvalIssue 规则评估恢复正常（或 metric 被修正）后清除异常登记。
func clearRuleEvalIssue(ruleID string) {
	ruleEvalIssuesMu.Lock()
	delete(ruleEvalIssues, ruleID)
	ruleEvalIssuesMu.Unlock()
}

// lookupRuleEvalIssue 查询规则当前评估异常（无则 ok=false）。
func lookupRuleEvalIssue(ruleID string) (RuleEvalIssue, bool) {
	ruleEvalIssuesMu.Lock()
	defer ruleEvalIssuesMu.Unlock()
	issue, ok := ruleEvalIssues[ruleID]
	return issue, ok
}

// shouldSkipUnknownMetricRule 判断该规则是否应因 metric 不存在而跳过本轮评估。
// metric 已被修正（与登记的 metric 不一致）时不再跳过，恢复评估。
func shouldSkipUnknownMetricRule(rule AlertRule) bool {
	issue, ok := lookupRuleEvalIssue(rule.ID)
	return ok && issue.Reason == ruleIssueMetricNotFound && issue.Metric == rule.Metric
}

// RuleEvalIssuesSnapshot 返回全部规则的评估异常登记快照（供规则列表/评估状态 API 接入，
// 在规则列表中体现"metric 不存在"状态）。
func RuleEvalIssuesSnapshot() []RuleEvalIssue {
	ruleEvalIssuesMu.Lock()
	defer ruleEvalIssuesMu.Unlock()
	out := make([]RuleEvalIssue, 0, len(ruleEvalIssues))
	for _, issue := range ruleEvalIssues {
		out = append(out, issue)
	}
	return out
}

// isUnknownMetricError 判断评估错误是否为 unknown metric（引擎不认识规则引用的 metric）。
func isUnknownMetricError(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "unknown metric:")
}

// ── PF-BE-008 / PF-DATA-005：告警事件 tenant/cluster 归属 ──
// 此前写入 ClickHouse alert_events 的 tenant_id/cluster_id 为空字符串，平台 API 按
// scope 过滤后恒为空，前端告警页恒空。修复：事件写入时补齐归属——
//   - cluster：优先继承规则自身 scope（rule.Cluster，空/"all" 视为未限定）；
//   - tenant：规则表无 tenant 字段，回退系统租户 AIOPS_SYSTEM_TENANT_ID
//     （与 log-shipper / 容量 ETT 的 metricsTenantID() 同一约定）。
// 未配置系统租户时返回空（fail-closed，不伪造默认租户）；部署需配置
// AIOPS_SYSTEM_TENANT_ID（集群级回退 AIOPS_SYSTEM_CLUSTER_ID）保证事件可按租户过滤。
// 历史空标签数据：ClickHouse ReplacingMergeTree 按 id 去重保留新版本，旧数据不会自动
// 回填；如需修复可在 CH 执行 ALTER TABLE ... UPDATE tenant_id/cluster_id 回填，或依赖
// TTL 自然过期（见下方 notes，不做强制迁移）。
func alertEventScope(rule AlertRule) (tenantID, clusterID string) {
	tenantID = metricsTenantID()
	clusterID = rule.Cluster
	if clusterID == "" || clusterID == "all" {
		clusterID = strings.TrimSpace(os.Getenv("AIOPS_SYSTEM_CLUSTER_ID"))
	}
	return tenantID, clusterID
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
	// PF-LOGIC-004：规则热加载——每轮评估周期从 MySQL 重载规则，
	// 新建/修改的规则无需重启 pod 即生效。加载失败保留内存规则继续本轮评估。
	reloadAlertRulesFromStore()

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
		// PF-LOGIC-004：已登记"metric 不存在"且 metric 未被修正的规则直接跳过，
		// 避免每分钟重复报错；规则 metric 修正后登记不再匹配，自动恢复评估。
		if shouldSkipUnknownMetricRule(rule) {
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
		// 与并行评估阶段保持一致：已跳过评估的规则不进入串行处理
		//（否则会走 res.err==nil 分支误清评估异常登记）。
		if shouldSkipUnknownMetricRule(rule) {
			continue
		}
		res := results[i]
		if res.err != nil {
			if isUnknownMetricError(res.err) {
				// PF-LOGIC-004：引擎不认识的 metric 只 warn 一次并登记状态
				//（供规则列表/评估状态体现"metric 不存在"），之后跳过评估。
				markRuleEvalIssue(rule, RuleEvalIssue{
					RuleID: rule.ID, Metric: rule.Metric, Reason: ruleIssueMetricNotFound,
				})
				continue
			}
			log.Printf("evaluate rule %s (%s): %v", rule.ID, rule.Name, res.err)
			continue
		}
		// 评估成功：清除该规则此前的评估异常登记（如 metric 已被修正）。
		clearRuleEvalIssue(rule.ID)
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
			var newEvent *AlertEvent
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
				// PF-BE-008/PF-DATA-005：事件写入必须携带 tenant/cluster 归属，
				// 否则平台 API 按 scope 过滤后返回 0，前端告警页恒空。
				eventTenantID, eventClusterID := alertEventScope(rule)
				event := AlertEvent{
					ID:       generateID(),
					TenantID: eventTenantID, // PF-BE-008：事件租户归属（规则无 tenant 字段→系统租户）
					RuleID:   rule.ID,
					RuleName: rule.Name,
					Service:  rule.Service,
					Severity: rule.Severity,     // P0-2 修复：事件继承规则严重级别
					Cluster:  eventClusterID,    // A-6 + PF-DATA-005：继承规则集群，未限定时回退系统集群
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
				newEvent = &event
				h.sendWebhook(event, &ruleCopy)
				log.Printf("ALERT: %s | %s | %s | value=%.2f threshold=%.2f", rule.Severity, rule.Service, rule.Name, value, rule.Threshold)
			}
			alertEventsMu.Unlock()

			saveAlertEvents()

			// Task 10：告警 → 调查的受控关联。必须在释放 alertEventsMu 之后调用，
			// 绝不能在告警锁内访问 MySQL 或 outbox；失败只记日志，不影响告警权威写入。
			if newEvent != nil {
				h.dispatchAlertInvestigation(*newEvent)
			}
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
