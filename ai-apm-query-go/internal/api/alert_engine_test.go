package api

import (
	"errors"
	"net/http"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// alertRuleRows 构造 AlertRuleDAO.LoadAll 期望返回的行（列序与 SELECT 一致）。
func alertRuleRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "name", "service", "type", "metric", "cond", "threshold", "duration",
		"severity", "enabled", "webhook_url", "cooldown", "dampening",
		"baseline_seconds", "anomaly_method", "slo_id", "keyword", "cluster",
	})
}

// withTestAlertRules 保存/恢复内存规则快照。
func withTestAlertRules(t *testing.T, rules []AlertRule) {
	t.Helper()
	alertRulesMu.Lock()
	original := append([]AlertRule(nil), alertRules...)
	alertRules = rules
	alertRulesMu.Unlock()
	t.Cleanup(func() {
		alertRulesMu.Lock()
		alertRules = original
		alertRulesMu.Unlock()
	})
}

// clearTestRuleEvalIssues 清空评估异常登记，避免测试间串扰。
func clearTestRuleEvalIssues(t *testing.T) {
	t.Helper()
	ruleEvalIssuesMu.Lock()
	ruleEvalIssues = map[string]RuleEvalIssue{}
	ruleEvalIssuesMu.Unlock()
	t.Cleanup(func() {
		ruleEvalIssuesMu.Lock()
		ruleEvalIssues = map[string]RuleEvalIssue{}
		ruleEvalIssuesMu.Unlock()
	})
}

// TestReloadAlertRulesHotLoad 验证 PF-LOGIC-004 规则热加载：
// 每个评估周期可从 MySQL 重载规则，新建规则无需重启即生效；
// MySQL 不可用时保留内存规则（不清空、不清除评估状态）。
func TestReloadAlertRulesHotLoad(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	prevDB := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(prevDB) })

	withTestAlertRules(t, []AlertRule{})
	clearTestRuleEvalIssues(t)

	rows := alertRuleRows().
		AddRow("hot-1", "hot rule", "payments", "threshold", "error_rate", ">", 1.0, 5,
			"warning", 1, nil, 0, 0, 900, nil, nil, nil, "prod-cluster")
	mock.ExpectQuery("SELECT id, name, service").WillReturnRows(rows)

	if !reloadAlertRulesFromStore() {
		t.Fatal("hot reload should succeed with mysql available")
	}
	alertRulesMu.RLock()
	got := alertRules
	alertRulesMu.RUnlock()
	if len(got) != 1 || got[0].ID != "hot-1" || got[0].Cluster != "prod-cluster" || !got[0].Enabled {
		t.Fatalf("hot reload should pick up the new rule without restart, got %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}

	// 第二次加载失败（MySQL 不可用）→ 返回 false 且保留内存规则，不清空
	mock.ExpectQuery("SELECT id, name, service").WillReturnError(errors.New("mysql down"))
	if reloadAlertRulesFromStore() {
		t.Fatal("reload should report failure when mysql is down")
	}
	alertRulesMu.RLock()
	got = alertRules
	alertRulesMu.RUnlock()
	if len(got) != 1 || got[0].ID != "hot-1" {
		t.Fatalf("failed reload must keep in-memory rules, got %+v", got)
	}
}

// TestEvaluateAlertsHotReloadsRules 验证 evaluateAlerts 每轮评估都会触发规则热加载。
func TestEvaluateAlertsHotReloadsRules(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	prevDB := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(prevDB) })

	withTestAlertRules(t, []AlertRule{})
	clearTestRuleEvalIssues(t)

	// 评估循环开始时应从 MySQL 重载规则（这里是唯一一次 DB 交互）。
	// 规则本身用 middleware_metric + 未配置 VM → 评估返回错误（不依赖外部服务），
	// 只验证热加载行为本身。
	rows := alertRuleRows().
		AddRow("reloaded-1", "reloaded rule", "payments", "middleware_metric",
			"mysql_global_status_threads_connected", ">", 100.0, 5,
			"warning", 1, nil, 0, 0, 900, nil, nil, nil, "")
	mock.ExpectQuery("SELECT id, name, service").WillReturnRows(rows)

	h := &Handler{client: &http.Client{}}
	h.evaluateAlerts()

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("evaluateAlerts should hot-reload rules from mysql: %v", err)
	}
	alertRulesMu.RLock()
	got := alertRules
	alertRulesMu.RUnlock()
	if len(got) != 1 || got[0].ID != "reloaded-1" {
		t.Fatalf("evaluateAlerts should swap in reloaded rules, got %+v", got)
	}
}

// TestUnknownMetricRuleWarnsOnceAndSkipped 验证 PF-LOGIC-004 unknown metric 处理：
// 第一次评估登记异常并 warn（仅一次），之后评估周期直接跳过不再报错；
// 规则 metric 被修正或评估成功后自动恢复并清除登记。
func TestUnknownMetricRuleWarnsOnceAndSkipped(t *testing.T) {
	clearTestRuleEvalIssues(t)
	withTestAlertRules(t, []AlertRule{{
		ID: "unk-1", Name: "bogus metric rule", Type: "threshold", Service: "kubernetes",
		Metric: "not_a_metric", Condition: ">", Threshold: 0, Severity: "warning", Enabled: true,
	}})

	h := &Handler{client: &http.Client{}}

	// 第一轮评估：登记 unknown metric 异常
	h.evaluateAlerts()
	issue, ok := lookupRuleEvalIssue("unk-1")
	if !ok || issue.Reason != ruleIssueMetricNotFound || issue.Metric != "not_a_metric" {
		t.Fatalf("unknown metric rule should be registered, got %+v ok=%v", issue, ok)
	}
	if !shouldSkipUnknownMetricRule(alertRulesSnapshot()[0]) {
		t.Fatal("registered rule should be skipped in subsequent cycles")
	}

	// 第二轮评估：不再产生新的登记（warn 一次性，避免每分钟刷屏）
	h.evaluateAlerts()
	if got := len(RuleEvalIssuesSnapshot()); got != 1 {
		t.Fatalf("repeated evaluation must not duplicate the issue, got %d issues", got)
	}

	// 规则 metric 被修正 → 登记不再匹配，恢复评估
	withTestAlertRules(t, []AlertRule{{
		ID: "unk-1", Name: "fixed metric rule", Type: "metric_raw", Service: "payments",
		Metric: "up", Condition: "<", Threshold: 100, Severity: "warning", Enabled: true,
	}})
	srv := mockVMInstant(t, "1")
	defer srv.Close()
	h.vmURL = srv.URL

	h.evaluateAlerts()
	if _, ok := lookupRuleEvalIssue("unk-1"); ok {
		t.Fatal("successful evaluation should clear the metric_not_found registration")
	}
}

// alertRulesSnapshot 读取内存规则快照（测试辅助）。
func alertRulesSnapshot() []AlertRule {
	alertRulesMu.RLock()
	defer alertRulesMu.RUnlock()
	return append([]AlertRule(nil), alertRules...)
}

// TestAlertEventInheritsTenantClusterScope 验证 PF-BE-008/PF-DATA-005：
// 告警事件写入时携带 tenant_id 与 cluster_id——规则带集群 scope 则继承，
// 未限定时回退系统集群；租户来自系统租户配置（AIOPS_SYSTEM_TENANT_ID）。
func TestAlertEventInheritsTenantClusterScope(t *testing.T) {
	t.Setenv("AIOPS_SYSTEM_TENANT_ID", "tenant-t1")
	t.Setenv("AIOPS_SYSTEM_CLUSTER_ID", "cluster-sys")
	clearTestRuleEvalIssues(t)

	alertEventsMu.Lock()
	originalEvents := append([]AlertEvent(nil), alertEvents...)
	alertEvents = []AlertEvent{}
	alertEventsMu.Unlock()
	t.Cleanup(func() {
		alertEventsMu.Lock()
		alertEvents = originalEvents
		alertEventsMu.Unlock()
	})

	srv := mockVMInstant(t, "100") // metric_raw 值恒为 100 > threshold 5 → 必然 breach
	defer srv.Close()

	withTestAlertRules(t, []AlertRule{
		{ID: "scoped-1", Name: "cluster scoped", Type: "metric_raw", Service: "payments",
			Metric: "up", Condition: ">", Threshold: 5, Severity: "warning", Enabled: true,
			Cluster: "prod-cluster"},
		{ID: "scoped-2", Name: "unscoped", Type: "metric_raw", Service: "payments",
			Metric: "up", Condition: ">", Threshold: 5, Severity: "warning", Enabled: true},
	})

	h := &Handler{vmURL: srv.URL, client: &http.Client{}}
	h.evaluateAlerts()

	alertEventsMu.RLock()
	defer alertEventsMu.RUnlock()
	byRule := map[string]AlertEvent{}
	for _, ev := range alertEvents {
		if ev.RuleID == "scoped-1" || ev.RuleID == "scoped-2" {
			byRule[ev.RuleID] = ev
		}
	}
	if len(byRule) != 2 {
		t.Fatalf("expected 2 alert events, got %d (%+v)", len(byRule), alertEvents)
	}
	ev1, ev2 := byRule["scoped-1"], byRule["scoped-2"]
	// 租户：规则表无 tenant 字段 → 系统租户
	if ev1.TenantID != "tenant-t1" || ev2.TenantID != "tenant-t1" {
		t.Fatalf("events must carry tenant_id from system tenant, got %q / %q", ev1.TenantID, ev2.TenantID)
	}
	// 集群：规则带 scope 继承；未限定回退系统集群
	if ev1.Cluster != "prod-cluster" {
		t.Fatalf("cluster-scoped rule event should inherit rule cluster, got %q", ev1.Cluster)
	}
	if ev2.Cluster != "cluster-sys" {
		t.Fatalf("unscoped rule event should fall back to system cluster, got %q", ev2.Cluster)
	}
}

// TestAlertEventScopeUnconfiguredTenantFailsClosed 验证未配置系统租户时不伪造默认租户。
func TestAlertEventScopeUnconfiguredTenantFailsClosed(t *testing.T) {
	t.Setenv("AIOPS_SYSTEM_TENANT_ID", "")
	t.Setenv("AIOPS_SYSTEM_CLUSTER_ID", "")
	tenantID, clusterID := alertEventScope(AlertRule{Cluster: ""})
	if tenantID != "" {
		t.Fatalf("without configured system tenant, tenant must stay empty (fail-closed), got %q", tenantID)
	}
	if clusterID != "" {
		t.Fatalf("without system cluster configured, unscoped rule cluster stays empty, got %q", clusterID)
	}
	tenantID, clusterID = alertEventScope(AlertRule{Cluster: "all"})
	if clusterID != "" {
		t.Fatalf(`cluster "all" must be treated as unscoped, got %q`, clusterID)
	}
}
