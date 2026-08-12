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
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// AlertRule defines an alert rule.
type AlertRule struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Service     string  `json:"service"`
	Type        string  `json:"type"`      // "threshold", "mutation", "anomaly", "forecast", "burn_rate", "metric_raw"
	Metric      string  `json:"metric"`     // "error_rate", "latency_p99", "call_count", 或 metric_raw 的 PromQL
	Condition   string  `json:"condition"`  // ">", "<", ">=", "<="
	Threshold   float64 `json:"threshold"`
	Duration    int     `json:"duration"` // minutes
	Severity    string  `json:"severity"` // "critical", "warning", "info"
	Enabled     bool    `json:"enabled"`
	WebhookURL  string  `json:"webhook_url,omitempty"` // 规则级 webhook（可选，覆盖全局）
	Cooldown    int     `json:"cooldown,omitempty"`   // 触发冷却（分钟）：冷却期内不重复告警
	Dampening   int     `json:"dampening,omitempty"`  // 连续确认（次数）：连续 N 次 breach 才告警
	BaselineSeconds int    `json:"baseline_seconds,omitempty"` // anomaly 基线窗口（秒）
	AnomalyMethod   string `json:"anomaly_method,omitempty"`   // anomaly 检测方法：zscore|mad
	SLOID           string `json:"slo_id,omitempty"`           // burn_rate 引用的 SLO 目标 id
	Keyword         string `json:"keyword,omitempty"`          // log_keyword 日志关键字（body LIKE '%keyword%'）
}

// inCooldown 判断规则是否处于冷却期（距上次触发 < Cooldown 分钟）。
func inCooldown(rule AlertRule, lastTrigger time.Time, now time.Time) bool {
	if rule.Cooldown <= 0 {
		return false
	}
	return now.Sub(lastTrigger) < time.Duration(rule.Cooldown)*time.Minute
}

// shouldAlertAfterDampening 判断连续 breach 次数是否达到 dampening 阈值。
func shouldAlertAfterDampening(rule AlertRule, streak int) bool {
	if rule.Dampening <= 1 {
		return true
	}
	return streak >= rule.Dampening
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
	Timeline         string  `json:"timeline,omitempty"`       // 状态变更历史（JSON 数组）
	Investigation    string  `json:"investigation,omitempty"` // 调查结果（RCA 分析 JSON）
	Signature        string  `json:"signature,omitempty"`     // dedupe 指纹（rule+service+detail）
}

// eventSignature 生成事件指纹（rule+service+detail 维度），用于 dedupe。
func eventSignature(ruleID, service, detail string) string {
	return ruleID + ":" + service + ":" + detail
}

// logMetricQuery 构造日志类规则的 CH 查询。
// log_error_rate：错误日志占比；log_keyword：关键词命中数。
// service/keyword 由用户输入拼入 SQL，须转义单引号防注入。
func logMetricQuery(service, metric, keyword string) string {
	if metric == "log_error_rate" {
		return fmt.Sprintf(
			"SELECT countIf(severity IN ('ERROR','FATAL')) / count() * 100 FROM observability.log_records WHERE service_name=%s AND timestamp >= now() - INTERVAL 5 MINUTE",
			chQuote(service))
	}
	return fmt.Sprintf(
		"SELECT count() FROM observability.log_records WHERE service_name=%s AND body LIKE %s AND timestamp >= now() - INTERVAL 5 MINUTE",
		chQuote(service), chLike(keyword))
}

// traceMetricQuery 构造链路类规则的 CH 查询。
// trace_latency：P99 延迟（ms）；trace_error_rate：错误率。
// service 由用户输入拼入 SQL，须用 chQuote 转义防注入。
func traceMetricQuery(service, metric string) string {
	if metric == "trace_latency" {
		return fmt.Sprintf(
			"SELECT quantile(0.99)(duration_ns)/1000000 FROM observability.trace_spans WHERE service_name=%s AND start_time >= now() - INTERVAL 5 MINUTE",
			chQuote(service))
	}
	return fmt.Sprintf(
		"SELECT countIf(is_error=1) / count() * 100 FROM observability.trace_spans WHERE service_name=%s AND start_time >= now() - INTERVAL 5 MINUTE",
		chQuote(service))
}

// AggAlertEvent 聚合后的告警事件：按规则聚合，统计触发次数和首次/最近时间。
type AggAlertEvent struct {
	ID             string `json:"id"`
	RuleID         string `json:"rule_id"`
	RuleName       string `json:"rule_name"`
	Service        string `json:"service"`
	Object         string `json:"object"` // 具体告警对象（Pod 名/Deployment 名等），替代"服务"归纳
	Severity       string `json:"severity"`
	Message        string `json:"message"`
	Count          int    `json:"count"`
	FirstTimestamp string `json:"first_timestamp"`
	LastTimestamp  string `json:"last_timestamp"`
}

var (
	alertRules       []AlertRule
	alertRulesMu     sync.RWMutex

	alertEvents       []AlertEvent
	alertEventsMu     sync.RWMutex
	maxAlertEvents    = 1000

	// alertCH: 告警事件持久化用的 ClickHouse 访问（由 main 建 Handler 后注入）。
	// 告警事件属时序数据，CH 列式存储 + TTL 管理生命周期，适配大数据量。
	alertCH *Handler

	// ── 告警降噪配置（借鉴 AlertManager 模式）──
	// groupInterval: 同一 (service, rule_id) 在此窗口内合并为一条事件，降低重复告警
	// repeatInterval: 同一规则持续触发超过该时长仍不恢复 → 升级一条事件
	// 可通过环境变量覆盖
	alertGroupInterval   = 5 * time.Minute
	alertRepeatInterval  = 60 * time.Minute
	alertSilences        []AlertSilence
	alertSilencesMu      sync.RWMutex
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

// SetAlertCH 由 main 建 Handler 后注入，供告警事件持久化读写 ClickHouse。
// 注入后立即从 CH 重载告警事件到内存态（init 阶段 CH 未就绪，此处才是真正加载）。
func SetAlertCH(h *Handler) {
	alertCH = h
	loadAlertEvents()
}

// toCHTime 把 RFC3339 转成 ClickHouse DateTime64(3) 格式；空串返回空（由调用方写 NULL）。
func toCHTime(s string) string {
	if s == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.UTC().Format("2006-01-02 15:04:05.000")
}

// fromCHTime 把 CH datetime 串转回 RFC3339；空串原样返回。
func fromCHTime(s string) string {
	if s == "" {
		return ""
	}
	for _, layout := range []string{"2006-01-02 15:04:05.000", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return s
}

// insertAlertEvents 批量 INSERT 告警事件到 ClickHouse（TabSeparated）。
// version 用当前纳秒时间戳，确保同 id 新版本被 ReplacingMergeTree 保留。
func (h *Handler) insertAlertEvents(events []AlertEvent) error {
	var buf strings.Builder
	buf.WriteString(`INSERT INTO observability.alert_events (id, rule_id, rule_name, service, severity, message, value, threshold, timestamp, count, first_timestamp, last_timestamp, status, acknowledged_at, acknowledged_by, resolved_at, resolved_by, timeline, investigation, signature, version, date) VALUES `)
	version := uint64(time.Now().UnixNano())
	for i, e := range events {
		if i > 0 {
			buf.WriteString(",")
		}
		date := ""
		if t, err := time.Parse(time.RFC3339, e.LastTimestamp); err == nil {
			date = t.UTC().Format("2006-01-02")
		}
		buf.WriteString(fmt.Sprintf("('%s','%s','%s','%s','%s','%s',%v,%v,%s,%d,%s,%s,'%s',%s,'%s',%s,'%s','%s','%s','%s',%d,%s)",
			escCH(e.ID), escCH(e.RuleID), escCH(e.RuleName), escCH(e.Service), escCH(e.Severity), escCH(e.Message),
			e.Value, e.Threshold,
			chTimeVal(e.Timestamp), e.Count,
			chTimeVal(e.FirstTimestamp), chTimeVal(e.LastTimestamp),
			escCH(e.Status),
			chTimeVal(e.AcknowledgedAt), escCH(e.AcknowledgedBy),
			chTimeVal(e.ResolvedAt), escCH(e.ResolvedBy),
			escCH(e.Timeline), escCH(e.Investigation), escCH(e.Signature),
			version,
			dateVal(date),
		))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return h.writeClickHouse(ctx, buf.String())
}

// chTimeVal 返回 CH datetime 字面量；空串写 NULL。
func chTimeVal(s string) string {
	v := toCHTime(s)
	if v == "" {
		return "NULL"
	}
	return fmt.Sprintf("'%s'", v)
}

// dateVal 返回 CH date 字面量；空串写 today。
func dateVal(d string) string {
	if d == "" {
		return fmt.Sprintf("'%s'", time.Now().UTC().Format("2006-01-02"))
	}
	return fmt.Sprintf("'%s'", d)
}

// queryAlertEvents 从 CH 查告警事件；service 非空按服务过滤，limit 限定返回条数。
// 返回按 last_timestamp 倒序的最新事件（内存态高并发读缓存）。
func (h *Handler) queryAlertEvents(service string, offset, limit int) ([]AlertEvent, error) {
	where := ""
	args := []interface{}{}
	if service != "" {
		where = " WHERE service = ?"
		args = append(args, service)
	}
	if limit <= 0 {
		limit = maxAlertEvents
	}
	if offset <= 0 {
		offset = 0
	}
	sql := "SELECT id, rule_id, rule_name, service, severity, message, value, threshold, timestamp, count, first_timestamp, last_timestamp, status, acknowledged_at, acknowledged_by, resolved_at, resolved_by, timeline, investigation, signature FROM observability.alert_events" + where + " ORDER BY last_timestamp DESC LIMIT " + strconv.Itoa(limit) + " OFFSET " + strconv.Itoa(offset)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	body, err := h.queryClickHouse(ctx, sql)
	if err != nil {
		return nil, err
	}
	// CH default_format=JSONEachRow：每行一个 JSON 对象（{...}\n{...}），需逐行解析；空结果容忍
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return []AlertEvent{}, nil
	}
	var out []AlertEvent
	for _, line := range bytes.Split(trimmed, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var r map[string]interface{}
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, err
		}
		out = append(out, AlertEvent{
			ID:             str(r["id"]),
			RuleID:         str(r["rule_id"]),
			RuleName:       str(r["rule_name"]),
			Service:        str(r["service"]),
			Severity:       str(r["severity"]),
			Message:        str(r["message"]),
			Value:          f64(r["value"]),
			Threshold:      f64(r["threshold"]),
			Timestamp:      fromCHTime(str(r["timestamp"])),
			Count:          int(f64(r["count"])),
			FirstTimestamp: fromCHTime(str(r["first_timestamp"])),
			LastTimestamp:  fromCHTime(str(r["last_timestamp"])),
			Status:         str(r["status"]),
			AcknowledgedAt: fromCHTime(str(r["acknowledged_at"])),
			AcknowledgedBy: str(r["acknowledged_by"]),
			ResolvedAt:     fromCHTime(str(r["resolved_at"])),
			ResolvedBy:     str(r["resolved_by"]),
			Timeline:       str(r["timeline"]),
			Investigation:  str(r["investigation"]),
			Signature:      str(r["signature"]),
		})
	}
	return out, nil
}

// escCH 转义 ClickHouse VALUES 字符串字面量中的反斜杠与单引号。
func escCH(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `'`, `\'`)
}

// str 把 JSON 值安全转 string（nil → 空串）。
func str(v interface{}) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// f64 把 JSON 值安全转 float64。
func f64(v interface{}) float64 {
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case json.Number:
		f, _ := n.Float64()
		return f
	default:
		return 0
	}
}

// ---- Persistence (MySQL，从 /tmp JSON 迁入) ----

func loadAlertRules() {
	alertRulesMu.Lock()
	defer alertRulesMu.Unlock()
	d := &store.AlertRuleDAO{}
	rows, err := d.LoadAll()
	if err != nil {
		log.Printf("loadAlertRules(mysql): %v", err)
		alertRules = []AlertRule{}
		return
	}
	alertRules = make([]AlertRule, 0, len(rows))
	for _, r := range rows {
		alertRules = append(alertRules, AlertRule{
			ID: r.ID, Name: r.Name, Service: r.Service, Type: r.Type, Metric: r.Metric,
			Condition: r.Condition, Threshold: r.Threshold, Duration: r.Duration,
			Severity: r.Severity, Enabled: r.Enabled, WebhookURL: r.WebhookURL,
			Cooldown: r.Cooldown, Dampening: r.Dampening,
			BaselineSeconds: r.BaselineSeconds, AnomalyMethod: r.AnomalyMethod, SLOID: r.SLOID,
			Keyword: r.Keyword,
		})
	}
}

func saveAlertRules() {
	// 锁内只快照，释放锁后再写 MySQL，避免长时间持有 RLock 阻塞 listAlertRules
	alertRulesMu.RLock()
	rows := make([]store.AlertRule, 0, len(alertRules))
	for _, r := range alertRules {
		rows = append(rows, store.AlertRule{
			ID: r.ID, Name: r.Name, Service: r.Service, Type: r.Type, Metric: r.Metric,
			Condition: r.Condition, Threshold: r.Threshold, Duration: r.Duration,
			Severity: r.Severity, Enabled: r.Enabled, WebhookURL: r.WebhookURL,
			Cooldown: r.Cooldown, Dampening: r.Dampening,
			BaselineSeconds: r.BaselineSeconds, AnomalyMethod: r.AnomalyMethod, SLOID: r.SLOID,
			Keyword: r.Keyword,
		})
	}
	alertRulesMu.RUnlock()

	d := &store.AlertRuleDAO{}
	if err := d.ReplaceAll(rows); err != nil {
		log.Printf("saveAlertRules(mysql): %v", err)
	}
}

func loadAlertEvents() {
	alertEventsMu.Lock()
	defer alertEventsMu.Unlock()
	// 从 ClickHouse 加载最近事件到内存态（高并发读缓存；持久真源为 CH）
	if alertCH == nil {
		alertEvents = []AlertEvent{}
		return
	}
	rows, err := alertCH.queryAlertEvents("", 0, maxAlertEvents)
	if err != nil {
		log.Printf("loadAlertEvents(clickhouse): %v", err)
		alertEvents = []AlertEvent{}
		return
	}
	alertEvents = rows
}

// fromMySQLTime 把 MySQL datetime（2026-08-09 04:28:59）转回 RFC3339（2026-08-09T04:28:59Z）。
// 解析失败或空串原样返回（兼容已是 RFC3339 的历史值）。
func fromMySQLTime(s string) string {
	if s == "" {
		return ""
	}
	t, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		return s
	}
	return t.UTC().Format(time.RFC3339)
}

func saveAlertEvents() {
	// 锁内快照，锁外写 ClickHouse（批量 INSERT），避免长时间持有锁阻塞读。
	// CH ReplacingMergeTree 按 version 去重并保留最新版本，全量 INSERT 幂等，
	// 不会像旧 MySQL ReplaceAll 那样删除未列出行；生命周期由 CH TTL 管理。
	if alertCH == nil {
		return
	}
	alertEventsMu.RLock()
	rows := make([]AlertEvent, 0, len(alertEvents))
	rows = append(rows, alertEvents...)
	alertEventsMu.RUnlock()
	if len(rows) == 0 {
		return
	}
	if err := alertCH.insertAlertEvents(rows); err != nil {
		log.Printf("saveAlertEvents(clickhouse): %v", err)
	}
}

// transitionStatus 执行告警事件状态迁移；非法迁移返回 false 且不修改。
// 合法：firing -> acknowledged -> resolved，或 firing -> resolved。
// 兼容：历史/未显式标记状态的事件 status 可能为空串，按 firing 处理。
func transitionStatus(ev *AlertEvent, to, by string) bool {
	now := time.Now().Format(time.RFC3339)
	switch to {
	case "acknowledged":
		if ev.Status != "" && ev.Status != "firing" {
			return false
		}
		ev.Status = to
		ev.AcknowledgedAt = now
		ev.AcknowledgedBy = by
		appendTimeline(ev, "acknowledged", by)
	case "resolved":
		if ev.Status == "resolved" {
			return false
		}
		ev.Status = to
		ev.ResolvedAt = now
		ev.ResolvedBy = by
		appendTimeline(ev, "resolved", by)
	default:
		return false
	}
	return true
}

func loadAlertSilences() {
	alertSilencesMu.Lock()
	defer alertSilencesMu.Unlock()
	d := &store.AlertSilenceDAO{}
	rows, err := d.LoadAll()
	if err != nil {
		log.Printf("loadAlertSilences(mysql): %v", err)
		alertSilences = []AlertSilence{}
		return
	}
	alertSilences = make([]AlertSilence, 0, len(rows))
	for _, s := range rows {
		alertSilences = append(alertSilences, AlertSilence{
			ID: s.ID, Service: s.Service, RuleID: s.RuleID, Comment: s.Comment,
			CreatedAt: s.CreatedAt, ExpiresAt: s.ExpiresAt,
		})
	}
}

func saveAlertSilences() {
	// 锁内快照，锁外写 MySQL
	alertSilencesMu.RLock()
	rows := make([]store.AlertSilence, 0, len(alertSilences))
	for _, s := range alertSilences {
		rows = append(rows, store.AlertSilence{
			ID: s.ID, Service: s.Service, RuleID: s.RuleID, Comment: s.Comment,
			CreatedAt: s.CreatedAt, ExpiresAt: s.ExpiresAt,
		})
	}
	alertSilencesMu.RUnlock()

	d := &store.AlertSilenceDAO{}
	if err := d.ReplaceAll(rows); err != nil {
		log.Printf("saveAlertSilences(mysql): %v", err)
	}
}

// saveInvestigation 持久化某事件的调查结果（RCA 结果 JSON）到事件。
func saveInvestigation(eventID, investigation string) {
	alertEventsMu.Lock()
	for i := range alertEvents {
		if alertEvents[i].ID == eventID {
			alertEvents[i].Investigation = investigation
			break
		}
	}
	alertEventsMu.Unlock()
	go saveAlertEvents()
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
	// P1: 支持 ?rule= 按规则过滤（规则页"历史告警"跳转携带）
	ruleFilter := r.URL.Query().Get("rule")
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
		if ruleFilter != "" && ev.RuleID != ruleFilter {
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
		// Task2: 为 K8s 告警补充具体告警对象（Pod 名/Deployment 名），而非仅归纳为 kubernetes 服务
		if a.Object == "" {
			a.Object = k8sAlertObjects(a.RuleID)
		}
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
	case strings.HasSuffix(p, "/investigation"):
		h.AlertEventInvestigation(w, r)
	default:
		h.AlertEventByID(w, r)
	}
}

// AlertEventInvestigation handles POST /api/v1/alerts/events/{id}/investigation
// 持久化该事件的调查结果（RCA 分析 JSON）。
func (h *Handler) AlertEventInvestigation(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/alerts/events/")
	id = strings.TrimSuffix(id, "/investigation")
	id = strings.TrimRight(id, "/")
	if id == "" {
		respondError(w, http.StatusBadRequest, "event id required")
		return
	}
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Investigation string `json:"investigation"`
	}
	_ = json.Unmarshal(body, &req)
	if req.Investigation == "" {
		respondError(w, http.StatusBadRequest, "investigation required")
		return
	}
	saveInvestigation(id, req.Investigation)
	respondJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "event_id": id})
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
	// 锁内修改 + 释放锁，锁外持久化（避免持写锁调 saveAlertEvents 死锁）
	alertEventsMu.Lock()
	idx := -1
	for i := range alertEvents {
		if alertEvents[i].ID == id || alertEvents[i].RuleID == id {
			if !transitionStatus(&alertEvents[i], "acknowledged", by) {
				alertEventsMu.Unlock()
				respondError(w, http.StatusConflict, "cannot acknowledge from current status")
				return
			}
			idx = i
			break
		}
	}
	alertEventsMu.Unlock()
	if idx < 0 {
		respondError(w, http.StatusNotFound, "event not found")
		return
	}
	saveAlertEvents()
	respondJSON(w, http.StatusOK, alertEvents[idx])
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
	// 锁内修改 + 释放锁，锁外持久化（避免持写锁调 saveAlertEvents 死锁）
	alertEventsMu.Lock()
	idx := -1
	for i := range alertEvents {
		if alertEvents[i].ID == id || alertEvents[i].RuleID == id {
			if !transitionStatus(&alertEvents[i], "resolved", by) {
				alertEventsMu.Unlock()
				respondError(w, http.StatusConflict, "cannot resolve from current status")
				return
			}
			idx = i
			break
		}
	}
	alertEventsMu.Unlock()
	if idx < 0 {
		respondError(w, http.StatusNotFound, "event not found")
		return
	}
	saveAlertEvents()
	respondJSON(w, http.StatusOK, alertEvents[idx])
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
	// 安全：含原始 PromQL/预测表达式的规则类型（metric_raw/anomaly/forecast）
	// 仅限 admin 创建，防止任意用户注入任意 PromQL 执行（可读取集群内任意指标）。
	if rule.Type == "metric_raw" || rule.Type == "anomaly" || rule.Type == "forecast" {
		if !hasRole(r, "admin") {
			respondError(w, http.StatusForbidden, "仅 admin 可创建原始 PromQL/预测类规则")
			return
		}
	}
	// burn_rate 规则：仅支持 error_rate 指标 + availability 型 SLO（否则静默永不触发）
	if rule.Type == "burn_rate" {
		if rule.Metric != "error_rate" {
			respondError(w, http.StatusBadRequest, "burn_rate 规则仅支持 error_rate 指标")
			return
		}
		if rule.SLOID != "" {
			dao := &store.SLOTargetDAO{}
			if slo, err := dao.Get(rule.SLOID); err != nil {
				respondError(w, http.StatusInternalServerError, err.Error())
				return
			} else if slo == nil {
				respondError(w, http.StatusBadRequest, "SLO 目标不存在")
				return
			} else if slo.SLOType != "availability" {
				respondError(w, http.StatusBadRequest, "burn_rate 仅支持 availability 型 SLO（烧毁率基于错误预算）")
				return
			}
		}
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
	// 先在锁内删除内存规则并释放锁，再在锁外持久化 MySQL（避免锁内调用 saveAlertRules 死锁）
	alertRulesMu.Lock()
	found := false
	for i, rule := range alertRules {
		if rule.ID == id {
			alertRules = append(alertRules[:i], alertRules[i+1:]...)
			found = true
			break
		}
	}
	alertRulesMu.Unlock()

	if found {
		saveAlertRules()
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"message": "rule deleted",
		})
		return
	}

	respondError(w, http.StatusNotFound, "rule not found")
}

// ---- Alert Evaluation ----

// vmInstantQuery 查 VictoriaMetrics 即时值（/api/v1/query），返回第一个样本数值。
func (h *Handler) vmInstantQuery(promQL string) (float64, error) {
	if h.vmURL == "" {
		return 0, fmt.Errorf("victoria-metrics not configured")
	}
	u, _ := url.Parse(h.vmURL + "/api/v1/query")
	q := u.Query()
	q.Set("query", promQL)
	u.RawQuery = q.Encode()
	req, _ := http.NewRequest(http.MethodGet, u.String(), nil)
	resp, err := h.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var r struct {
		Data struct {
			Result []struct {
				Value []interface{} `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &r) != nil || len(r.Data.Result) == 0 {
		return 0, nil
	}
	if len(r.Data.Result[0].Value) < 2 {
		return 0, nil
	}
	s, ok := r.Data.Result[0].Value[1].(string)
	if !ok {
		return 0, nil
	}
	return strconv.ParseFloat(s, 64)
}

// promLabelVal 转义 PromQL 标签值中的 \ 与 "，防 PromQL 注入。
func promLabelVal(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`)
}

// metricPromQL 返回规则对应指标的 VM PromQL 表达式。
// 支持内置语义指标（error_rate/latency_p99/call_count）与 metric_raw 原始指标。
// 安全：service 来自用户输入，拼入标签值前须转义，防 PromQL 注入。
func metricPromQL(rule AlertRule) string {
	svc := promLabelVal(rule.Service)
	switch rule.Metric {
	case "error_rate":
		return fmt.Sprintf(`sum(rate(service_errors_total{service="%s"}[%dm])) / clamp_min(sum(rate(service_requests_total{service="%s"}[%dm])), 1) * 100`,
			svc, rule.Duration, svc, rule.Duration)
	case "call_count":
		return fmt.Sprintf(`sum(rate(service_requests_total{service="%s"}[%dm])) * %d`, svc, rule.Duration, rule.Duration)
	case "latency_p99":
		return fmt.Sprintf(`histogram_quantile(0.99, sum(rate(service_request_duration_seconds_bucket{service="%s"}[%dm])) by (le))`, svc, rule.Duration)
	default:
		// metric_raw：metric 字段即 PromQL 原始表达式（仅限 admin 创建，见 createAlertRule 鉴权）
		return rule.Metric
	}
}

// ComputeBurnRate 计算烧毁率 = 实际错误率 / 目标错误率。targetPct 为 SLO 目标（如 99.9）。
func ComputeBurnRate(errRatePct, targetPct float64) float64 {
	targetErrPct := 100 - targetPct
	if targetErrPct <= 0 {
		return 0 // 100% SLO（无错误预算）不计算烧毁
	}
	return errRatePct / targetErrPct
}

// evaluateRuleAnomaly 拉 baseline 窗口的历史序列，用 zscore/MAD 统计检测当前值是否异常。
// 数据不足（<3 点）时不误报。
func (h *Handler) evaluateRuleAnomaly(rule AlertRule) (float64, bool) {
	baseline := time.Duration(rule.BaselineSeconds) * time.Second
	if baseline <= 0 {
		baseline = 15 * time.Minute
	}
	end := time.Now().Unix()
	start := end - int64(baseline.Seconds())
	promQL := metricPromQL(rule)
	series, err := h.vmRangeQuery(promQL, start, end, 60)
	if err != nil || len(series) < 3 {
		return 0, false // 数据不足或 VM 故障不误报
	}
	method := rule.AnomalyMethod
	if method == "" {
		method = "zscore"
	}
	current := series[len(series)-1]
	hist := series[:len(series)-1]
	threshold := rule.Threshold
	if threshold <= 0 {
		// 默认阈值：zscore=3 / mad=3.5
		threshold = defaultAnomalyThreshold(method)
	}
	_, anom := ComputeAnomaly(hist, current, method, threshold)
	return current, anom
}

func defaultAnomalyThreshold(method string) float64 {
	if method == "mad" {
		return 3.5
	}
	return 3
}

// evaluateRuleBurnRate 按 SLO 目标计算当前错误率烧毁率，超阈值告警。
// errRate 为已评估的当前错误率（%）。烧毁率 = 实际错误率 / 目标错误率（目标错误率 = 1 - slo.target）。
// rule.Threshold 默认 14.4。仅 error_rate 指标语义正确，其他 metric 返回 false。
func (h *Handler) evaluateRuleBurnRate(rule AlertRule, errRate float64) (float64, bool) {
	if rule.SLOID == "" {
		return 0, false
	}
	// 烧毁率语义仅对 error_rate 有意义；call_count/latency_p99 等不能当作错误率
	if rule.Metric != "error_rate" {
		return 0, false
	}
	dao := &store.SLOTargetDAO{}
	slo, err := dao.Get(rule.SLOID)
	if err != nil || slo == nil {
		return 0, false
	}
	burn := ComputeBurnRate(errRate, slo.Target)
	threshold := rule.Threshold
	if threshold <= 0 {
		threshold = 14.4 // 默认：SLO 预算 1% 窗口烧毁率上限
	}
	return burn, burn > threshold
}

// evaluateRuleHistorical 查 VM 历史窗口的指标基线（用于 mutation/forecast 检测）。
// 对比窗口：当前值 vs 前一相同 duration 窗口。
func (h *Handler) evaluateRuleHistorical(rule AlertRule) (float64, error) {
	// 历史窗口：当前 duration 往前推一个 duration（即 2×duration 到 duration 之间）
	windowMin := rule.Duration
	if windowMin <= 0 {
		windowMin = 5
	}
	// 用 rate 的过去窗口近似：查询从 [now-2d, now-d] 的平均速率
	promQL := fmt.Sprintf(`avg_over_time((%s)[%dm:1m])`, metricPromQL(rule), windowMin)
	u, _ := url.Parse(h.vmURL + "/api/v1/query")
	q := u.Query()
	q.Set("query", promQL)
	// 指定时间戳为过去窗口起点（now - windowMin 分钟）
	q.Set("time", time.Now().Add(-time.Duration(windowMin)*time.Minute).Format(time.RFC3339))
	u.RawQuery = q.Encode()
	req, _ := http.NewRequest(http.MethodGet, u.String(), nil)
	resp, err := h.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var r struct {
		Data struct {
			Result []struct {
				Value []interface{} `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &r) != nil || len(r.Data.Result) == 0 {
		return 0, fmt.Errorf("no historical data")
	}
	if len(r.Data.Result[0].Value) < 2 {
		return 0, fmt.Errorf("no historical value")
	}
	s, ok := r.Data.Result[0].Value[1].(string)
	if !ok {
		return 0, fmt.Errorf("bad historical value")
	}
	return strconv.ParseFloat(s, 64)
}

// sendWebhook sends alert to rule-level webhook（优先）或全局 env webhook。
func (h *Handler) sendWebhook(event AlertEvent, rule *AlertRule) {
	webhookURL := ""
	if rule != nil && rule.WebhookURL != "" {
		webhookURL = rule.WebhookURL
	} else {
		webhookURL = os.Getenv("ALERT_WEBHOOK_URL")
	}
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

// appendTimeline 向事件追加一条状态变更记录（timeline JSON 数组）。
func appendTimeline(event *AlertEvent, action, by string) {
	entry := map[string]string{"action": action, "by": by, "at": time.Now().UTC().Format(time.RFC3339)}
	entries := []map[string]string{}
	if event.Timeline != "" {
		_ = json.Unmarshal([]byte(event.Timeline), &entries)
	}
	entries = append(entries, entry)
	data, _ := json.Marshal(entries)
	event.Timeline = string(data)
}

// evalCHQuery 执行 CH 查询并返回第一行第一个非空数值（供 log/trace 类型规则使用）。
func (h *Handler) evalCHQuery(ctx context.Context, sql string) (float64, error) {
	body, err := h.queryClickHouse(ctx, sql)
	if err != nil {
		return 0, err
	}
	rows, err := parseRows(body)
	if err != nil || len(rows) == 0 {
		return 0, nil
	}
	row := rows[0]
	for _, v := range row {
		if f, err := toFloat64(v); err == nil {
			return f, nil
		}
	}
	return 0, nil
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

	// log 类型：CH 日志查询（log_error_rate / log_keyword）
	if rule.Type == "log" {
		sql := logMetricQuery(rule.Service, rule.Metric, rule.Keyword)
		v, err := h.evalCHQuery(ctx, sql)
		if err != nil {
			return 0, err
		}
		return v, nil
	}

	// trace_latency / trace_error_rate 类型：CH trace 查询
	if rule.Type == "trace_latency" || rule.Type == "trace_error_rate" {
		sql := traceMetricQuery(rule.Service, rule.Metric)
		v, err := h.evalCHQuery(ctx, sql)
		if err != nil {
			return 0, err
		}
		return v, nil
	}

	// metric_raw / anomaly / forecast / burn_rate 类型：metric 字段作为 VM PromQL 原始指标
	if rule.Type == "metric_raw" || rule.Type == "anomaly" || rule.Type == "forecast" || rule.Type == "burn_rate" {
		v, err := h.vmInstantQuery(metricPromQL(rule))
		if err != nil {
			return 0, err
		}
		return v, nil
	}

	var sql string
	switch rule.Metric {
	case "error_rate":
		sql = fmt.Sprintf(
			"SELECT countIf(is_error=1) as errors, count() as total FROM observability.trace_spans WHERE service_name=%s AND start_time >= now() - INTERVAL %d MINUTE",
			chQuote(rule.Service), duration,
		)
	case "latency_p99":
		sql = fmt.Sprintf(
			"SELECT quantile(0.99)(duration_ns)/1000000 as p99_ms FROM observability.trace_spans WHERE service_name=%s AND start_time >= now() - INTERVAL %d MINUTE",
			chQuote(rule.Service), duration,
		)
	case "call_count":
		sql = fmt.Sprintf(
			"SELECT count() as cnt FROM observability.trace_spans WHERE service_name=%s AND start_time >= now() - INTERVAL %d MINUTE",
			chQuote(rule.Service), duration,
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

// k8sAlertObjects 查询 K8s 中触发告警的具体对象名（Pod 名 / Deployment 名），
// 供告警事件展示"具体告警对象"而非仅归纳为 kubernetes 服务（Task2）。
// 按 ruleID 分派：pod 类返回 crash/oom 的 Pod 名；deployment 类返回不可用副本的 Deployment 名。
func k8sAlertObjects(ruleID string) string {
	switch ruleID {
	case "k8s-pod-crash", "k8s-pod-pending", "k8s-oom-killed":
		return k8sCrashPods(ruleID)
	case "k8s-deployment-unavailable":
		return k8sUnavailableDeployments()
	default:
		return ""
	}
}

// k8sCrashPods 返回处于异常状态的 Pod 名（OOMKilled / CrashLoopBackOff / Pending / 高重启）。
func k8sCrashPods(ruleID string) string {
	data, err := k8sAPI("/api/v1/pods")
	if err != nil {
		return ""
	}
	var r struct {
		Items []struct {
			Metadata struct {
				Name      string            `json:"name"`
				Namespace string            `json:"namespace"`
				Labels    map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				Phase             string `json:"phase"`
				ContainerStatuses []struct {
					RestartCount int `json:"restartCount"`
					State        struct {
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
		return ""
	}
	names := []string{}
	seen := map[string]bool{}
	for _, p := range r.Items {
		hit := false
		switch ruleID {
		case "k8s-oom-killed":
			for _, cs := range p.Status.ContainerStatuses {
				if cs.State.Waiting.Reason == "CrashLoopBackOff" || cs.LastTerminationState.Terminated.Reason == "OOMKilled" {
					hit = true
				}
			}
		case "k8s-pod-pending":
			hit = p.Status.Phase == "Pending"
		default: // k8s-pod-crash
			for _, cs := range p.Status.ContainerStatuses {
				if cs.RestartCount > 3 {
					hit = true
				}
			}
		}
		if hit && !seen[p.Metadata.Name] {
			seen[p.Metadata.Name] = true
			names = append(names, p.Metadata.Name)
		}
	}
	// Issue3: 集群已恢复（实时无异常对象）时，回退到最近 K8s 事件中记录的对象名，
	// 使历史告警也能展示具体的 Pod/Deployment 对象
	if len(names) == 0 {
		var evReasons map[string]bool
		switch ruleID {
		case "k8s-oom-killed":
			evReasons = map[string]bool{"OOMKilled": true, "CrashLoopBackOff": true}
		case "k8s-pod-pending":
			evReasons = map[string]bool{"FailedScheduling": true, "Pending": true}
		default:
			evReasons = map[string]bool{"CrashLoopBackOff": true, "BackOff": true}
		}
		names = k8sEventObjects(evReasons, seen)
	}
	if len(names) > 5 {
		names = names[:5] // 只展示前 5 个对象，避免超长
	}
	return strings.Join(names, ", ")
}

// k8sUnavailableDeployments 返回存在不可用副本的 Deployment 名。
// 若当前无不可用副本（集群已恢复），回退到最近 K8s 事件中记录的异常 Deployment 名。
func k8sUnavailableDeployments() string {
	names := []string{}
	seen := map[string]bool{}
	data, err := k8sAPI("/apis/apps/v1/deployments")
	if err == nil {
		var r struct {
			Items []struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Status struct {
					UnavailableReplicas *int32 `json:"unavailableReplicas"`
				} `json:"status"`
			} `json:"items"`
		}
		if json.Unmarshal(data, &r) == nil {
			for _, d := range r.Items {
				if d.Status.UnavailableReplicas != nil && *d.Status.UnavailableReplicas > 0 && !seen[d.Metadata.Name] {
					seen[d.Metadata.Name] = true
					names = append(names, d.Metadata.Name)
				}
			}
		}
	}
	// 回退：从最近 K8s 事件中找与 Deployment 不可用相关的对象（FailedUpdate / Unavailable）
	if len(names) == 0 {
		names = k8sEventObjects(map[string]bool{
			"FailedUpdate": true, "Unavailable": true, "FailedCreate": true,
			"FailedScheduling": true,
		}, seen)
	}
	if len(names) > 5 {
		names = names[:5]
	}
	return strings.Join(names, ", ")
}

// k8sEventObjects 从 K8s 最近事件中提取对象名（Pod/Deployment），reason 命中即纳入。
func k8sEventObjects(reasons map[string]bool, seen map[string]bool) []string {
	data, err := k8sAPI("/api/v1/events?limit=50")
	if err != nil {
		return []string{}
	}
	var r struct {
		Items []struct {
			InvolvedObject struct {
				Kind string `json:"kind"`
				Name string `json:"name"`
			} `json:"involvedObject"`
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"items"`
	}
	if json.Unmarshal(data, &r) != nil {
		return []string{}
	}
	names := []string{}
	for _, e := range r.Items {
		if (reasons[e.Reason] || reasonsContain(reasons, e.Message)) && e.InvolvedObject.Name != "" && !seen[e.InvolvedObject.Name] {
			seen[e.InvolvedObject.Name] = true
			names = append(names, e.InvolvedObject.Name)
		}
	}
	return names
}

// reasonsContain 在消息中命中任一 reason 关键词（如 OOMKilled / CrashLoopBackOff）。
func reasonsContain(reasons map[string]bool, msg string) bool {
	for k := range reasons {
		if strings.Contains(msg, k) {
			return true
		}
	}
	return false
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
	alertRulesMu.Lock()
	existingIDs := make(map[string]bool)
	for _, r := range alertRules {
		existingIDs[r.ID] = true
	}
	for _, r := range k8sRules {
		if !existingIDs[r.ID] {
			alertRules = append(alertRules, r)
		}
	}
	alertRulesMu.Unlock()

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
