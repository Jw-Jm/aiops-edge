package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AlertRule defines an alert rule.
type AlertRule struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Service   string  `json:"service"`
	Type      string  `json:"type"`      // "threshold" or "mutation"
	Metric    string  `json:"metric"`     // "error_rate", "latency_p99", "call_count"
	Condition string  `json:"condition"`  // ">", "<", ">=", "<="
	Threshold float64 `json:"threshold"`
	Duration  int     `json:"duration"` // minutes
	Severity  string  `json:"severity"` // "critical", "warning", "info"
	Enabled   bool    `json:"enabled"`
}

// AlertEvent represents a triggered alert event.
type AlertEvent struct {
	ID               string  `json:"id"`
	RuleID           string  `json:"rule_id"`
	RuleName         string  `json:"rule_name"`
	Service          string  `json:"service"`
	Severity         string  `json:"severity"`
	Message          string  `json:"message"`
	Value            float64 `json:"value"`
	Threshold        float64 `json:"threshold"`
	Timestamp        string  `json:"timestamp"`
	Count            int     `json:"count"`
	FirstTimestamp   string  `json:"first_timestamp"`
	LastTimestamp    string  `json:"last_timestamp"`
	Status           string  `json:"status"` // firing/acknowledged/resolved
	AcknowledgedAt   string  `json:"acknowledged_at,omitempty"`
	AcknowledgedBy   string  `json:"acknowledged_by,omitempty"`
	ResolvedAt       string  `json:"resolved_at,omitempty"`
	ResolvedBy       string  `json:"resolved_by,omitempty"`
}

// AggAlertEvent 聚合后的告警事件：按规则聚合，统计触发次数和首次/最近时间。
type AggAlertEvent struct {
	ID             string `json:"id"`
	RuleID         string `json:"rule_id"`
	RuleName       string `json:"rule_name"`
	Service        string `json:"service"`
	Severity       string `json:"severity"`
	Message        string `json:"message"`
	Count          int    `json:"count"`
	FirstTimestamp string `json:"first_timestamp"`
	LastTimestamp  string `json:"last_timestamp"`
}

var (
	alertRules       []AlertRule
	alertRulesMu     sync.RWMutex
	alertRulesPath   = "/tmp/observability-alerts.json"

	alertEvents       []AlertEvent
	alertEventsMu     sync.RWMutex
	alertEventsPath   = "/tmp/observability-alert-events.json"
	maxAlertEvents    = 1000

	// ── 告警降噪配置（借鉴 AlertManager 模式）──
	// groupInterval: 同一 (service, rule_id) 在此窗口内合并为一条事件，降低重复告警
	// repeatInterval: 同一规则持续触发超过该时长仍不恢复 → 升级一条事件
	// 可通过环境变量覆盖
	alertGroupInterval   = 5 * time.Minute
	alertRepeatInterval  = 60 * time.Minute
	alertSilences        []AlertSilence
	alertSilencesMu      sync.RWMutex
	alertSilencesPath    = "/tmp/observability-alert-silences.json"
)

// AlertSilence 表示一条告警静默规则（抑制指定服务/规则的告警一段时间）
type AlertSilence struct {
	ID        string `json:"id"`
	Service   string `json:"service"`            // 空 = 所有服务
	RuleID    string `json:"rule_id"`            // 空 = 所有规则
	Comment   string `json:"comment"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`         // RFC3339，到此后失效
}

func init() {
	if f := os.Getenv("ALERT_RULES_FILE"); f != "" {
		alertRulesPath = f
	}
	if f := os.Getenv("ALERT_EVENTS_FILE"); f != "" {
		alertEventsPath = f
	}
	if f := os.Getenv("ALERT_SILENCES_FILE"); f != "" {
		alertSilencesPath = f
	}
	if v := os.Getenv("ALERT_GROUP_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			alertGroupInterval = d
		}
	}
	if v := os.Getenv("ALERT_REPEAT_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			alertRepeatInterval = d
		}
	}
	loadAlertRules()
	loadAlertEvents()
	loadAlertSilences()
}

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ---- Persistence ----

func loadAlertRules() {
	data, err := os.ReadFile(alertRulesPath)
	if err != nil {
		alertRules = []AlertRule{}
		return
	}
	if err := json.Unmarshal(data, &alertRules); err != nil {
		log.Printf("loadAlertRules: %v", err)
		alertRules = []AlertRule{}
	}
}

func saveAlertRules() {
	alertRulesMu.RLock()
	defer alertRulesMu.RUnlock()
	data, err := json.MarshalIndent(alertRules, "", "  ")
	if err != nil {
		log.Printf("saveAlertRules marshal: %v", err)
		return
	}
	if err := os.WriteFile(alertRulesPath, data, 0600); err != nil {
		log.Printf("saveAlertRules write: %v", err)
	}
}

func loadAlertEvents() {
	data, err := os.ReadFile(alertEventsPath)
	if err != nil {
		alertEvents = []AlertEvent{}
		return
	}
	if err := json.Unmarshal(data, &alertEvents); err != nil {
		log.Printf("loadAlertEvents: %v", err)
		alertEvents = []AlertEvent{}
	}
}

func saveAlertEvents() {
	alertEventsMu.RLock()
	defer alertEventsMu.RUnlock()
	data, err := json.MarshalIndent(alertEvents, "", "  ")
	if err != nil {
		log.Printf("saveAlertEvents marshal: %v", err)
		return
	}
	if err := os.WriteFile(alertEventsPath, data, 0600); err != nil {
		log.Printf("saveAlertEvents write: %v", err)
	}
}

// transitionStatus 执行告警事件状态迁移；非法迁移返回 false 且不修改。
// 合法：firing -> acknowledged -> resolved，或 firing -> resolved。
func transitionStatus(ev *AlertEvent, to, by string) bool {
	now := time.Now().Format(time.RFC3339)
	switch to {
	case "acknowledged":
		if ev.Status != "firing" {
			return false
		}
		ev.Status = to
		ev.AcknowledgedAt = now
		ev.AcknowledgedBy = by
	case "resolved":
		if ev.Status == "resolved" {
			return false
		}
		ev.Status = to
		ev.ResolvedAt = now
		ev.ResolvedBy = by
	default:
		return false
	}
	return true
}

func loadAlertSilences() {
	data, err := os.ReadFile(alertSilencesPath)
	if err != nil {
		alertSilences = []AlertSilence{}
		return
	}
	if err := json.Unmarshal(data, &alertSilences); err != nil {
		alertSilences = []AlertSilence{}
	}
}

func saveAlertSilences() {
	alertSilencesMu.RLock()
	defer alertSilencesMu.RUnlock()
	data, err := json.MarshalIndent(alertSilences, "", "  ")
	if err != nil {
		log.Printf("saveAlertSilences marshal: %v", err)
		return
	}
	if err := os.WriteFile(alertSilencesPath, data, 0600); err != nil {
		log.Printf("saveAlertSilences write: %v", err)
	}
}

// isSilenced 判断某服务/规则的告警当前是否被静默抑制
func isSilenced(service, ruleID string) bool {
	alertSilencesMu.RLock()
	defer alertSilencesMu.RUnlock()
	now := time.Now().UTC().Format(time.RFC3339)
	for _, s := range alertSilences {
		if s.ExpiresAt != "" && now > s.ExpiresAt {
			continue // 已过期
		}
		if s.Service != "" && s.Service != service {
			continue
		}
		if s.RuleID != "" && s.RuleID != ruleID {
			continue
		}
		return true
	}
	return false
}

// ---- HTTP Handlers ----

// AlertRules handles GET (list) and POST (create) for /api/v1/alerts/rules
func (h *Handler) AlertRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listAlertRules(w, r)
	case http.MethodPost:
		h.createAlertRule(w, r)
	case http.MethodOptions:
		w.WriteHeader(http.StatusOK)
	default:
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// AlertRuleByID handles GET and DELETE for /api/v1/alerts/rules/{id}
func (h *Handler) AlertRuleByID(w http.ResponseWriter, r *http.Request) {
	// Strip "/api/v1/alerts/rules/" prefix to get the ID
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/alerts/rules/")
	id = strings.TrimRight(id, "/")
	if id == "" {
		respondError(w, http.StatusBadRequest, "rule id required")
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getAlertRule(w, r, id)
	case http.MethodDelete:
		h.deleteAlertRule(w, r, id)
	case http.MethodOptions:
		w.WriteHeader(http.StatusOK)
	default:
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// AlertEvents handles GET for /api/v1/alerts/events
func (h *Handler) AlertEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	serviceFilter := r.URL.Query().Get("service")
	limit := 50
	offset := 0

	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v >= 0 {
			offset = v
		}
	}

	alertEventsMu.RLock()
	defer alertEventsMu.RUnlock()

	// 按规则聚合：相同 rule_id 的告警事件聚合成一行，统计触发次数
	aggMap := map[string]*AggAlertEvent{}
	for _, ev := range alertEvents {
		if serviceFilter != "" && ev.Service != serviceFilter {
			continue
		}
		key := ev.RuleID
		if a, ok := aggMap[key]; ok {
			// 使用事件自身的 Count（聚合降噪后同窗口只存1条，Count 记录真实触发次数）
			a.Count += ev.Count
			if ev.Timestamp > a.LastTimestamp {
				a.LastTimestamp = ev.Timestamp
			}
			if ev.LastTimestamp > a.LastTimestamp {
				a.LastTimestamp = ev.LastTimestamp
			}
			if a.FirstTimestamp == "" {
				a.FirstTimestamp = ev.FirstTimestamp
				if a.FirstTimestamp == "" {
					a.FirstTimestamp = ev.Timestamp
				}
			}
		} else {
			cnt := ev.Count
			if cnt == 0 {
				cnt = 1
			}
			ft := ev.FirstTimestamp
			if ft == "" {
				ft = ev.Timestamp
			}
			lt := ev.LastTimestamp
			if lt == "" {
				lt = ev.Timestamp
			}
			aggMap[key] = &AggAlertEvent{
				ID:             ev.RuleID,
				RuleID:         ev.RuleID,
				RuleName:       ev.RuleName,
				Service:        ev.Service,
				Severity:       ev.Severity,
				Message:        ev.Message,
				Count:          cnt,
				FirstTimestamp: ft,
				LastTimestamp:  lt,
			}
		}
	}

	// 转切片
	aggList := make([]AggAlertEvent, 0, len(aggMap))
	for _, a := range aggMap {
		aggList = append(aggList, *a)
	}

	// 按最近触发时间倒序
	sort.Slice(aggList, func(i, j int) bool {
		return aggList[i].LastTimestamp > aggList[j].LastTimestamp
	})

	total := len(aggList)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	result := aggList[start:end]

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"data":   result,
		"count":  len(result),
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// ---- Incident Detail / Ack / Resolve ----

// AlertEventRouter 分发 /api/v1/alerts/events/{id}[/ack|/resolve]
func (h *Handler) AlertEventRouter(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/api/v1/alerts/events/")
	p = strings.TrimRight(p, "/")
	switch {
	case strings.HasSuffix(p, "/ack"):
		h.AlertEventAck(w, r)
	case strings.HasSuffix(p, "/resolve"):
		h.AlertEventResolve(w, r)
	default:
		h.AlertEventByID(w, r)
	}
}

// AlertEventByID handles GET /api/v1/alerts/events/{id}
func (h *Handler) AlertEventByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/alerts/events/")
	id = strings.TrimRight(id, "/")
	if id == "" {
		respondError(w, http.StatusBadRequest, "event id required")
		return
	}
	alertEventsMu.RLock()
	defer alertEventsMu.RUnlock()
	for _, ev := range alertEvents {
		if ev.ID == id || ev.RuleID == id {
			respondJSON(w, http.StatusOK, ev)
			return
		}
	}
	respondError(w, http.StatusNotFound, "event not found")
}

// AlertEventAck handles POST /api/v1/alerts/events/{id}/ack
func (h *Handler) AlertEventAck(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/alerts/events/")
	id = strings.TrimSuffix(id, "/ack")
	if id == "" {
		respondError(w, http.StatusBadRequest, "event id required")
		return
	}
	by := extractTenantID(r)
	alertEventsMu.Lock()
	defer alertEventsMu.Unlock()
	for i := range alertEvents {
		if alertEvents[i].ID == id || alertEvents[i].RuleID == id {
			if !transitionStatus(&alertEvents[i], "acknowledged", by) {
				respondError(w, http.StatusConflict, "cannot acknowledge from current status")
				return
			}
			saveAlertEvents()
			respondJSON(w, http.StatusOK, alertEvents[i])
			return
		}
	}
	respondError(w, http.StatusNotFound, "event not found")
}

// AlertEventResolve handles POST /api/v1/alerts/events/{id}/resolve
func (h *Handler) AlertEventResolve(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/alerts/events/")
	id = strings.TrimSuffix(id, "/resolve")
	if id == "" {
		respondError(w, http.StatusBadRequest, "event id required")
		return
	}
	by := extractTenantID(r)
	alertEventsMu.Lock()
	defer alertEventsMu.Unlock()
	for i := range alertEvents {
		if alertEvents[i].ID == id || alertEvents[i].RuleID == id {
			if !transitionStatus(&alertEvents[i], "resolved", by) {
				respondError(w, http.StatusConflict, "cannot resolve from current status")
				return
			}
			saveAlertEvents()
			respondJSON(w, http.StatusOK, alertEvents[i])
			return
		}
	}
	respondError(w, http.StatusNotFound, "event not found")
}

// ---- Alert Silence CRUD ----

// AlertSilences handles GET (list) and POST (create) for /api/v1/alerts/silences
func (h *Handler) AlertSilences(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		alertSilencesMu.RLock()
		defer alertSilencesMu.RUnlock()
		now := time.Now().UTC().Format(time.RFC3339)
		active := []AlertSilence{}
		for _, s := range alertSilences {
			if s.ExpiresAt == "" || now <= s.ExpiresAt {
				active = append(active, s)
			}
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{"data": active, "count": len(active)})
	case http.MethodPost:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			respondError(w, http.StatusBadRequest, "failed to read body")
			return
		}
		defer r.Body.Close()
		var req struct {
			Service string `json:"service"`
			RuleID  string `json:"rule_id"`
			Comment string `json:"comment"`
			Minutes int    `json:"minutes"` // 静默时长(分钟)，默认 60
		}
		if err := json.Unmarshal(body, &req); err != nil {
			respondError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.Service == "" && req.RuleID == "" {
			respondError(w, http.StatusBadRequest, "service or rule_id required")
			return
		}
		if req.Minutes <= 0 {
			req.Minutes = 60
		}
		silence := AlertSilence{
			ID:        generateID(),
			Service:   req.Service,
			RuleID:    req.RuleID,
			Comment:   req.Comment,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
			ExpiresAt: time.Now().UTC().Add(time.Duration(req.Minutes) * time.Minute).Format(time.RFC3339),
		}
		alertSilencesMu.Lock()
		alertSilences = append(alertSilences, silence)
		alertSilencesMu.Unlock()
		saveAlertSilences()
		respondJSON(w, http.StatusCreated, map[string]interface{}{"data": silence})
	case http.MethodOptions:
		w.WriteHeader(http.StatusOK)
	default:
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// AlertSilenceByID handles DELETE for /api/v1/alerts/silences/{id}
func (h *Handler) AlertSilenceByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/alerts/silences/")
	id = strings.TrimRight(id, "/")
	if id == "" {
		respondError(w, http.StatusBadRequest, "silence id required")
		return
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodDelete {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	alertSilencesMu.Lock()
	for i, s := range alertSilences {
		if s.ID == id {
			alertSilences = append(alertSilences[:i], alertSilences[i+1:]...)
			break
		}
	}
	alertSilencesMu.Unlock()
	saveAlertSilences()
	respondJSON(w, http.StatusOK, map[string]interface{}{"message": "silence deleted"})
}

// ---- Rule CRUD ----

func (h *Handler) listAlertRules(w http.ResponseWriter, r *http.Request) {
	alertRulesMu.RLock()
	defer alertRulesMu.RUnlock()
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"data":  alertRules,
		"count": len(alertRules),
	})
}

func (h *Handler) createAlertRule(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		respondError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	defer r.Body.Close()

	var rule AlertRule
	if err := json.Unmarshal(body, &rule); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if rule.Name == "" {
		respondError(w, http.StatusBadRequest, "name is required")
		return
	}
	if rule.Service == "" {
		respondError(w, http.StatusBadRequest, "service is required")
		return
	}

	rule.ID = generateID()

	alertRulesMu.Lock()
	alertRules = append(alertRules, rule)
	alertRulesMu.Unlock()

	saveAlertRules()

	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"data": rule,
	})
}

func (h *Handler) getAlertRule(w http.ResponseWriter, r *http.Request, id string) {
	alertRulesMu.RLock()
	defer alertRulesMu.RUnlock()

	for _, rule := range alertRules {
		if rule.ID == id {
			respondJSON(w, http.StatusOK, map[string]interface{}{
				"data": rule,
			})
			return
		}
	}

	respondError(w, http.StatusNotFound, "rule not found")
}

func (h *Handler) deleteAlertRule(w http.ResponseWriter, r *http.Request, id string) {
	alertRulesMu.Lock()
	defer alertRulesMu.Unlock()

	for i, rule := range alertRules {
		if rule.ID == id {
			alertRules = append(alertRules[:i], alertRules[i+1:]...)
			saveAlertRules()
			respondJSON(w, http.StatusOK, map[string]interface{}{
				"message": "rule deleted",
			})
			return
		}
	}

	respondError(w, http.StatusNotFound, "rule not found")
}

// ---- Alert Evaluation ----

// StartAlertEvaluation starts the background alert evaluation loop.
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

	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}

		value, err := h.evaluateRule(rule)
		if err != nil {
			log.Printf("evaluate rule %s (%s): %v", rule.ID, rule.Name, err)
			continue
		}

		breached := checkCondition(value, rule.Condition, rule.Threshold)

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

		if breached {
			// 告警降噪：先检查是否被静默抑制
			if isSilenced(rule.Service, rule.ID) {
				log.Printf("ALERT_SILENCED: %s | %s | %s (value=%.2f)", rule.Severity, rule.Service, rule.Name, value)
				continue
			}

			now := time.Now().UTC()
			nowStr := now.Format(time.RFC3339)

			alertEventsMu.Lock()
			// 时间窗口聚合：窗口内已有同 (service, rule_id) 事件则只更新计数/时间，不新增（降噪）
			var existing *AlertEvent
			for i := range alertEvents {
				e := &alertEvents[i]
				if e.RuleID == rule.ID && e.Service == rule.Service {
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
				event := AlertEvent{
					ID:             generateID(),
					RuleID:         rule.ID,
					RuleName:       rule.Name,
					Service:        rule.Service,
					Severity:       rule.Severity,
					Message:        fmt.Sprintf("%s: %s %.2f > threshold %.2f", rule.Name, rule.Metric, value, rule.Threshold),
					Value:          value,
					Threshold:      rule.Threshold,
					Timestamp:      nowStr,
					Count:          1,
					FirstTimestamp: nowStr,
					LastTimestamp:  nowStr,
					Status:         "firing",
				}
				alertEvents = append(alertEvents, event)
				if len(alertEvents) > maxAlertEvents {
					alertEvents = alertEvents[len(alertEvents)-maxAlertEvents:]
				}
				// Webhook 通知（仅新事件通知，聚合事件不重复通知）
				h.sendWebhook(event)
				log.Printf("ALERT: %s | %s | %s | value=%.2f threshold=%.2f", rule.Severity, rule.Service, rule.Name, value, rule.Threshold)
			}
			alertEventsMu.Unlock()

			saveAlertEvents()
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

// evaluateRuleHistorical returns historical baseline for mutation detection (stub)
func (h *Handler) evaluateRuleHistorical(rule AlertRule) (float64, error) {
	return 0, fmt.Errorf("historical query not implemented")
}

// sendWebhook sends alert to configured webhook URL
func (h *Handler) sendWebhook(event AlertEvent) {
	webhookURL := os.Getenv("ALERT_WEBHOOK_URL")
	if webhookURL == "" {
		return
	}
	body, _ := json.Marshal(event)
	go func() {
		client := &http.Client{Timeout: 5 * time.Second}
		req, _ := http.NewRequest("POST", webhookURL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		client.Do(req)
	}()
}

func (h *Handler) evaluateRule(rule AlertRule) (float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	duration := rule.Duration
	if duration <= 0 {
		duration = 5
	}

	// K8s 指标：基于 K8s API 数据评估（pod_restarts / pod_pending / node_pressure
	// / unavailable_replicas / pvc_usage / oom），替代走 default 报错。
	switch rule.Metric {
	case "pod_restarts":
		return h.evalK8sPodRestarts(), nil
	case "pod_pending_minutes":
		return h.evalK8sPodPending(), nil
	case "node_pressure":
		return h.evalK8sNodePressure(), nil
	case "unavailable_replicas":
		return h.evalK8sUnavailableReplicas(), nil
	case "pvc_usage_percent":
		return h.evalK8sPVCUsage(), nil
	case "oom_count":
		return h.evalK8sOOMCount(), nil
	}

	var sql string
	switch rule.Metric {
	case "error_rate":
		sql = fmt.Sprintf(
			"SELECT countIf(is_error=1) as errors, count() as total FROM observability.trace_spans WHERE service_name='%s' AND start_time >= now() - INTERVAL %d MINUTE",
			rule.Service, duration,
		)
	case "latency_p99":
		sql = fmt.Sprintf(
			"SELECT quantile(0.99)(duration_ns)/1000000 as p99_ms FROM observability.trace_spans WHERE service_name='%s' AND start_time >= now() - INTERVAL %d MINUTE",
			rule.Service, duration,
		)
	case "call_count":
		sql = fmt.Sprintf(
			"SELECT count() as cnt FROM observability.trace_spans WHERE service_name='%s' AND start_time >= now() - INTERVAL %d MINUTE",
			rule.Service, duration,
		)
	default:
		return 0, fmt.Errorf("unknown metric: %s", rule.Metric)
	}

	body, err := h.queryClickHouse(ctx, sql)
	if err != nil {
		return 0, err
	}

	rows, err := parseRows(body)
	if err != nil {
		return 0, err
	}

	if len(rows) == 0 {
		return 0, nil
	}

	row := rows[0]
	switch rule.Metric {
	case "error_rate":
		errors, _ := toFloat64(row["errors"])
		total, _ := toFloat64(row["total"])
		if total > 0 {
			return (errors / total) * 100, nil // percentage
		}
		return 0, nil
	case "latency_p99":
		val, _ := toFloat64(row["p99_ms"])
		return val, nil
	case "call_count":
		val, _ := toFloat64(row["cnt"])
		return val, nil
	default:
		return 0, fmt.Errorf("unknown metric: %s", rule.Metric)
	}
}

// ---- K8s 指标评估 (基于 K8s API) ----

// evalK8sPodRestarts 返回当前集群中重启次数超过 3 次的 Pod 数量
func (h *Handler) evalK8sPodRestarts() float64 {
	data, err := k8sAPI("/api/v1/pods")
	if err != nil {
		return 0
	}
	var r struct {
		Items []struct {
			Status struct {
				ContainerStatuses []struct {
					RestartCount int `json:"restartCount"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal(data, &r) != nil {
		return 0
	}
	count := 0
	for _, p := range r.Items {
		for _, cs := range p.Status.ContainerStatuses {
			if cs.RestartCount > 3 {
				count++
			}
		}
	}
	return float64(count)
}

// evalK8sPodPending 返回处于 Pending 状态的 Pod 数量
func (h *Handler) evalK8sPodPending() float64 {
	data, err := k8sAPI("/api/v1/pods")
	if err != nil {
		return 0
	}
	var r struct {
		Items []struct {
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal(data, &r) != nil {
		return 0
	}
	count := 0
	for _, p := range r.Items {
		if p.Status.Phase == "Pending" {
			count++
		}
	}
	return float64(count)
}

// evalK8sNodePressure 返回处于 NotReady 状态的节点数量（即资源/网络压力）
func (h *Handler) evalK8sNodePressure() float64 {
	data, err := k8sAPI("/api/v1/nodes")
	if err != nil {
		return 0
	}
	var r struct {
		Items []struct {
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal(data, &r) != nil {
		return 0
	}
	count := 0
	for _, n := range r.Items {
		for _, c := range n.Status.Conditions {
			if c.Type == "Ready" && c.Status != "True" {
				count++
			}
		}
	}
	return float64(count)
}

// evalK8sUnavailableReplicas 返回 Deployment 中不可用副本总数
func (h *Handler) evalK8sUnavailableReplicas() float64 {
	data, err := k8sAPI("/apis/apps/v1/deployments")
	if err != nil {
		return 0
	}
	var r struct {
		Items []struct {
			Status struct {
				Replicas          int `json:"replicas"`
				AvailableReplicas int `json:"availableReplicas"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal(data, &r) != nil {
		return 0
	}
	total := 0
	for _, d := range r.Items {
		if d.Status.Replicas > d.Status.AvailableReplicas {
			total += d.Status.Replicas - d.Status.AvailableReplicas
		}
	}
	return float64(total)
}

// evalK8sPVCUsage 返回使用率超过 85% 的 PVC 数量（基于容量状态近似）
func (h *Handler) evalK8sPVCUsage() float64 {
	data, err := k8sAPI("/api/v1/persistentvolumeclaims")
	if err != nil {
		return 0
	}
	var r struct {
		Items []struct {
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal(data, &r) != nil {
		return 0
	}
	// PVC 无法直接读容量使用率，统计非 Bound 状态的 PVC 数量作为压力信号
	count := 0
	for _, p := range r.Items {
		if p.Status.Phase != "Bound" {
			count++
		}
	}
	return float64(count)
}

// evalK8sOOMCount 返回处于 OOMKilled / CrashLoopBackOff 状态的容器数量
func (h *Handler) evalK8sOOMCount() float64 {
	data, err := k8sAPI("/api/v1/pods")
	if err != nil {
		return 0
	}
	var r struct {
		Items []struct {
			Status struct {
				ContainerStatuses []struct {
					State struct {
						Waiting struct {
							Reason string `json:"reason"`
						} `json:"waiting"`
					} `json:"state"`
					LastTerminationState struct {
						Terminated struct {
							Reason string `json:"reason"`
						} `json:"terminated"`
					} `json:"lastState"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal(data, &r) != nil {
		return 0
	}
	count := 0
	for _, p := range r.Items {
		for _, cs := range p.Status.ContainerStatuses {
			if cs.State.Waiting.Reason == "CrashLoopBackOff" || cs.LastTerminationState.Terminated.Reason == "OOMKilled" {
				count++
			}
		}
	}
	return float64(count)
}

func checkCondition(value float64, condition string, threshold float64) bool {
	switch condition {
	case ">":
		return value > threshold
	case "<":
		return value < threshold
	case ">=":
		return value >= threshold
	case "<=":
		return value <= threshold
	default:
		return false
	}
}

// toFloat64 converts an interface{} to float64, handling various numeric types.
func toFloat64(v interface{}) (float64, error) {
	switch val := v.(type) {
	case float64:
		return val, nil
	case float32:
		return float64(val), nil
	case int:
		return float64(val), nil
	case int64:
		return float64(val), nil
	case int32:
		return float64(val), nil
	case uint64:
		return float64(val), nil
	case string:
		return strconv.ParseFloat(val, 64)
	case json.Number:
		return val.Float64()
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", v)
	}
}

// K8sDefaultRules returns a set of recommended Kubernetes alert rules
func K8sDefaultRules() []AlertRule {
	return []AlertRule{
		{ID: "k8s-pod-crash", Name: "Pod 频繁重启", Service: "kubernetes", Type: "threshold", Metric: "pod_restarts", Condition: ">", Threshold: 3, Duration: 5, Severity: "critical", Enabled: true},
		{ID: "k8s-pod-pending", Name: "Pod 长时间 Pending", Service: "kubernetes", Type: "threshold", Metric: "pod_pending_minutes", Condition: ">", Threshold: 10, Duration: 5, Severity: "warning", Enabled: true},
		{ID: "k8s-node-pressure", Name: "节点资源压力", Service: "kubernetes", Type: "threshold", Metric: "node_pressure", Condition: ">", Threshold: 80, Duration: 10, Severity: "critical", Enabled: true},
		{ID: "k8s-deployment-unavailable", Name: "Deployment 不可用", Service: "kubernetes", Type: "threshold", Metric: "unavailable_replicas", Condition: ">", Threshold: 0, Duration: 5, Severity: "critical", Enabled: true},
		{ID: "k8s-pvc-usage", Name: "PVC 容量不足", Service: "kubernetes", Type: "threshold", Metric: "pvc_usage_percent", Condition: ">", Threshold: 85, Duration: 5, Severity: "warning", Enabled: true},
		{ID: "k8s-oom-killed", Name: "Pod OOMKilled", Service: "kubernetes", Type: "threshold", Metric: "oom_count", Condition: ">", Threshold: 0, Duration: 5, Severity: "critical", Enabled: true},
	}
}

// InitK8sRules initializes Kubernetes alert rules if not already present
func InitK8sRules() {
	k8sRules := K8sDefaultRules()
	alertEventsMu.Lock()
	defer alertEventsMu.Unlock()

	existingIDs := make(map[string]bool)
	for _, r := range alertRules { existingIDs[r.ID] = true }
	for _, r := range k8sRules {
		if !existingIDs[r.ID] {
			alertRules = append(alertRules, r)
		}
	}
	saveAlertRules()
}

// AggregateAlerts groups alert events by service and severity
type AlertAggregation struct {
	Service   string                `json:"service"`
	Total     int                   `json:"total"`
	BySeverity map[string]int       `json:"by_severity"`
	LatestRule string               `json:"latest_rule"`
	LatestTime string               `json:"latest_time"`
	Events    []AlertEvent          `json:"events"`
}

func (h *Handler) AlertAggregation(w http.ResponseWriter, r *http.Request) {
	alertEventsMu.Lock()
	defer alertEventsMu.Unlock()

	agg := make(map[string]*AlertAggregation)
	for _, evt := range alertEvents {
		key := evt.Service
		if a, ok := agg[key]; ok {
			a.Total++
			a.BySeverity[evt.Severity]++
			a.Events = append(a.Events, evt)
			if evt.Timestamp > a.LatestTime {
				a.LatestTime = evt.Timestamp
				a.LatestRule = evt.RuleName
			}
		} else {
			agg[key] = &AlertAggregation{
				Service:    key,
				Total:      1,
				BySeverity: map[string]int{evt.Severity: 1},
				LatestRule: evt.RuleName,
				LatestTime: evt.Timestamp,
				Events:     []AlertEvent{evt},
			}
		}
	}

	result := make([]AlertAggregation, 0, len(agg))
	for _, a := range agg { result = append(result, *a) }
	respondJSON(w, 200, map[string]interface{}{"data": result})
}
