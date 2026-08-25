package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidCanonicalBootstrap means the runtime was given a legacy alias or
// incomplete canonical identity. Runtime bootstrap must never silently create
// a "default" tenant/cluster because those values cannot participate in the
// canonical authorization boundary.
var ErrInvalidCanonicalBootstrap = errors.New("invalid canonical bootstrap identity")

// CanonicalBootstrapConfig is the non-secret local/system identity that query-api
// uses to make a fresh install usable. CredentialRef and KubernetesIdentityUID
// are optional: when absent, the cluster remains visible for read-only tenant
// scope, while the Kubernetes access boundary continues to fail closed until a
// real registration binds credentials to an observed Kubernetes identity.
type CanonicalBootstrapConfig struct {
	TenantID              string
	TenantName            string
	ClusterID             string
	ClusterSlug           string
	ClusterName           string
	ClusterEnvironment    string
	ClusterRegion         string
	ClusterCredentialRef  string
	KubernetesIdentityUID string
}

func (c CanonicalBootstrapConfig) Validate() error {
	if !canonicalUUIDPattern.MatchString(strings.TrimSpace(c.TenantID)) ||
		!canonicalUUIDPattern.MatchString(strings.TrimSpace(c.ClusterID)) ||
		strings.TrimSpace(c.ClusterSlug) == "" || strings.TrimSpace(c.ClusterName) == "" ||
		strings.EqualFold(strings.TrimSpace(c.TenantID), "default") ||
		strings.EqualFold(strings.TrimSpace(c.ClusterID), "default") {
		return ErrInvalidCanonicalBootstrap
	}
	return nil
}

// EnsureBootstrapData 幂等地写入 bootstrap seed DML（V9.2 Phase 4 P4.4）。
//
// 这是 DML-only：绝不执行 CREATE/ALTER/DROP/INDEX，因此可用 aiops_app 账号运行。
// 所有 DDL 与一次性 backfill 已迁入 versioned migration（schema-migrator 执行）。
// 本函数只负责"合法启动 seed"（初始默认面板等幂等数据）。
//
// 原则（用户 P4.4 拍板）：一次性历史数据修正 → migration；正常业务运行 DML → runtime。
func EnsureBootstrapData(conn *sql.DB) error {
	if conn == nil {
		return nil
	}
	if err := seedDefaultDashboardPanels(conn); err != nil {
		return err
	}
	return nil
}

// EnsureCanonicalBootstrapData writes the minimum canonical authorization and
// cluster registry rows needed by a fresh install. It is deliberately DML-only
// and idempotent so the same source can be used by local and production runtime
// upgrades after the versioned schema migration has completed.
func EnsureCanonicalBootstrapData(conn *sql.DB, cfg CanonicalBootstrapConfig) error {
	if conn == nil {
		return ErrMySQLUnavailable
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("%w: tenant_id, cluster_id, slug and name are required canonical values", err)
	}

	tx, err := conn.Begin()
	if err != nil {
		return fmt.Errorf("canonical bootstrap begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`INSERT INTO tenants (id, name, quota_ai, enabled)
VALUES (?, ?, 0, 1)
ON DUPLICATE KEY UPDATE name=VALUES(name), enabled=1`,
		strings.TrimSpace(cfg.TenantID), firstNonEmpty(cfg.TenantName, cfg.TenantID)); err != nil {
		return fmt.Errorf("canonical bootstrap tenant: %w", err)
	}

	var adminUUID string
	if err := tx.QueryRow("SELECT user_uuid FROM users WHERE username = 'admin' LIMIT 1").Scan(&adminUUID); err != nil {
		return fmt.Errorf("canonical bootstrap admin lookup: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO user_tenants (user_uuid, tenant_id, status)
VALUES (?, ?, 'active')
ON DUPLICATE KEY UPDATE status='active'`, adminUUID, cfg.TenantID); err != nil {
		return fmt.Errorf("canonical bootstrap tenant membership: %w", err)
	}

	// Keep the relational RBAC projection consistent with users.role. The
	// endpoint role checks still read users as their authority; this projection
	// is required by the stricter resource authorization path.
	const adminRoleID = "00000000-0000-4000-8000-000000000001"
	const kubernetesReadPermissionID = "00000000-0000-4000-8000-000000000002"
	if _, err := tx.Exec(`INSERT IGNORE INTO roles (role_id, name, status)
VALUES (?, 'admin', 'active')`, adminRoleID); err != nil {
		return fmt.Errorf("canonical bootstrap admin role: %w", err)
	}
	if _, err := tx.Exec(`INSERT IGNORE INTO permissions (permission_id, action, status)
VALUES (?, 'kubernetes.read', 'active')`, kubernetesReadPermissionID); err != nil {
		return fmt.Errorf("canonical bootstrap permission: %w", err)
	}
	if _, err := tx.Exec(`INSERT IGNORE INTO user_roles (user_uuid, tenant_id, role_id, status)
VALUES (?, ?, ?, 'active')`, adminUUID, cfg.TenantID, adminRoleID); err != nil {
		return fmt.Errorf("canonical bootstrap admin role binding: %w", err)
	}
	if _, err := tx.Exec(`INSERT IGNORE INTO role_permissions (role_id, permission_id)
VALUES (?, ?)`, adminRoleID, kubernetesReadPermissionID); err != nil {
		return fmt.Errorf("canonical bootstrap role permission: %w", err)
	}

	// The optional identity fields are intentionally preserved when the runtime
	// has no real registration evidence. This makes restart idempotent and never
	// replaces an observed binding with an empty value.
	if _, err := tx.Exec(`INSERT INTO clusters
(cluster_id, tenant_id, slug, name, provider, region, environment, version, node_count, status, api_server, kubeconfig, credential_ref, lifecycle_status, type, capabilities, labels, kubernetes_identity_uid)
VALUES (?, ?, ?, ?, 'kubernetes', ?, ?, '', 0, 'active', '', NULL, ?, 'active', 'kubernetes', 'metrics,logs,traces,kubernetes', NULL, ?)
ON DUPLICATE KEY UPDATE
tenant_id=VALUES(tenant_id), slug=VALUES(slug), name=VALUES(name), provider=VALUES(provider), region=VALUES(region), environment=VALUES(environment),
credential_ref=IF(VALUES(credential_ref)='', clusters.credential_ref, VALUES(credential_ref)),
lifecycle_status=IF(clusters.lifecycle_status='deleted', 'active', clusters.lifecycle_status),
kubernetes_identity_uid=IF(VALUES(kubernetes_identity_uid)='', clusters.kubernetes_identity_uid, VALUES(kubernetes_identity_uid))`,
		cfg.ClusterID, cfg.TenantID, cfg.ClusterSlug, cfg.ClusterName, cfg.ClusterRegion, cfg.ClusterEnvironment,
		cfg.ClusterCredentialRef, cfg.KubernetesIdentityUID); err != nil {
		return fmt.Errorf("canonical bootstrap cluster registry: %w", err)
	}
	if _, err := tx.Exec(`INSERT IGNORE INTO tenant_clusters (tenant_id, cluster_id) VALUES (?, ?)`, cfg.TenantID, cfg.ClusterID); err != nil {
		return fmt.Errorf("canonical bootstrap cluster ownership: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("canonical bootstrap commit: %w", err)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// seedDefaultDashboardPanels 首次初始化默认看板面板（幂等：仅当面板表为空时写入）。
func seedDefaultDashboardPanels(conn *sql.DB) error {
	var count int
	if err := conn.QueryRow("SELECT count(*) FROM dashboard_panels").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	seedPanels := []struct {
		id, title, query, chart string
		span                    int
	}{
		{"panel-1", "服务请求速率", "sum(rate(http_requests_total[5m])) by (service)", "line", 6},
		{"panel-2", "服务错误率", "sum(rate(http_requests_total{status=~\"5..\"}[5m])) by (service)", "line", 6},
		{"panel-3", "延迟 P95", "histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket[5m])) by (le, service))", "line", 6},
		{"panel-4", "CPU 使用率", "100 - avg(rate(node_cpu_seconds_total{mode=\"idle\"}[5m])) * 100", "line", 6},
	}
	for i, sp := range seedPanels {
		if _, err := conn.Exec(
			"INSERT IGNORE INTO dashboard_panels (id, title, query, chart_type, span, sort, enabled) VALUES (?, ?, ?, ?, ?, ?, 1)",
			sp.id, sp.title, sp.query, sp.chart, sp.span, i); err != nil {
			return err
		}
	}
	return nil
}
