package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

var ErrMySQLUnavailable = errors.New("mysql unavailable")

const (
	DenialInvalidContext   = "invalid_context"
	DenialMySQLUnavailable = "mysql_unavailable"
	DenialIdentityNotFound = "identity_not_found"
	DenialUserDisabled     = "user_disabled"
	DenialSessionDisabled  = "session_disabled"
	DenialTenantMismatch   = "tenant_mismatch"
	DenialClusterMismatch  = "cluster_mismatch"
	DenialActionDenied     = "action_denied"
	DenialScopeDenied      = "scope_denied"
)

// AuthorizationQuery is caller-provided identity and target context. It deliberately
// has no role, permission, or scope fields: those are read only from MySQL.
type AuthorizationQuery struct {
	UserID       string
	SessionID    string
	TenantRef    string
	ClusterRef   string
	Namespace    string
	ResourceType string
	ResourceName string
	Action       string
}

// AuthorizationDecision is a MySQL-derived authorization result.
type AuthorizationDecision struct {
	Allowed    bool
	UserID     string
	TenantID   string
	ClusterID  string
	Action     string
	DenialCode string
}

// AuthorizationDAO evaluates current identity, tenant, cluster, permission, and exact scope state.
type AuthorizationDAO struct{}

func (d *AuthorizationDAO) Authorize(ctx AuthorizationQuery) (AuthorizationDecision, error) {
	decision := AuthorizationDecision{UserID: ctx.UserID, TenantID: ctx.TenantRef, Action: ctx.Action}
	if !completeAuthorizationQuery(ctx) {
		decision.DenialCode = DenialInvalidContext
		return decision, nil
	}
	conn := GetDB()
	if conn == nil {
		decision.DenialCode = DenialMySQLUnavailable
		return decision, ErrMySQLUnavailable
	}

	var userID, sessionStatus string
	var userStatus int
	var expiresAt, revokedAt sql.NullTime
	var storedVersion int64
	// Consistent with resolveMySQLAuthorizationContext: read token_version (V9.2 §8).
	err := conn.QueryRow(`SELECT u.user_uuid, u.status, s.status, s.expires_at, s.revoked_at, s.token_version FROM users u JOIN user_sessions s ON s.user_uuid = u.user_uuid
WHERE u.user_uuid = ? AND s.session_id = ? LIMIT 1`, ctx.UserID, ctx.SessionID).Scan(&userID, &userStatus, &sessionStatus, &expiresAt, &revokedAt, &storedVersion)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			decision.DenialCode = DenialIdentityNotFound
			return decision, nil
		}
		decision.DenialCode = DenialMySQLUnavailable
		return decision, err
	}
	if userStatus != 1 {
		decision.DenialCode = DenialUserDisabled
		return decision, nil
	}
	if sessionStatus != "active" || !expiresAt.Valid || !expiresAt.Time.After(time.Now()) || (revokedAt.Valid && !revokedAt.Time.IsZero()) {
		decision.DenialCode = DenialSessionDisabled
		return decision, nil
	}

	var tenantID string
	err = conn.QueryRow(`SELECT t.id FROM tenants t JOIN user_tenants ut ON ut.tenant_id = t.id
WHERE ut.user_uuid = ? AND t.id = ? AND t.enabled = 1 AND ut.status = 'active' LIMIT 1`, userID, ctx.TenantRef).Scan(&tenantID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			decision.DenialCode = DenialTenantMismatch
			return decision, nil
		}
		decision.DenialCode = DenialMySQLUnavailable
		return decision, err
	}
	decision.TenantID = tenantID

	cluster, err := (&ClusterDAO{}).ResolveRef(tenantID, ctx.ClusterRef)
	if err != nil {
		if errors.Is(err, ErrClusterNotFound) || errors.Is(err, ErrClusterAmbiguous) || errors.Is(err, ErrInvalidClusterRef) {
			decision.DenialCode = DenialClusterMismatch
			return decision, nil
		}
		decision.DenialCode = DenialMySQLUnavailable
		return decision, err
	}
	decision.ClusterID = cluster.ClusterID

	var allowed int
	err = conn.QueryRow(`SELECT 1 FROM user_roles ur
JOIN roles r ON r.role_id = ur.role_id
JOIN role_permissions rp ON rp.role_id = r.role_id
JOIN permissions p ON p.permission_id = rp.permission_id
WHERE ur.user_uuid = ? AND ur.tenant_id = ? AND ur.status = 'active' AND r.status = 'active' AND p.status = 'active' AND p.action = ? LIMIT 1`,
		userID, tenantID, ctx.Action).Scan(&allowed)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			decision.DenialCode = DenialActionDenied
			return decision, nil
		}
		decision.DenialCode = DenialMySQLUnavailable
		return decision, err
	}

	err = conn.QueryRow(`SELECT 1 FROM user_roles ur
JOIN role_permissions rp ON rp.role_id = ur.role_id
JOIN permissions p ON p.permission_id = rp.permission_id
JOIN scope_assignments sa ON sa.role_id = ur.role_id AND sa.tenant_id = ur.tenant_id
WHERE ur.user_uuid = ? AND ur.tenant_id = ? AND sa.cluster_id = ? AND sa.namespace = ? AND sa.resource_type = ? AND sa.resource_name = ? AND p.action = ? AND sa.action = ? AND ur.status = 'active' AND p.status = 'active' AND sa.status = 'active' LIMIT 1`,
		userID, tenantID, cluster.ClusterID, ctx.Namespace, ctx.ResourceType, ctx.ResourceName, ctx.Action, ctx.Action).Scan(&allowed)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			decision.DenialCode = DenialScopeDenied
			return decision, nil
		}
		decision.DenialCode = DenialMySQLUnavailable
		return decision, err
	}
	decision.Allowed = true
	return decision, nil
}

func completeAuthorizationQuery(ctx AuthorizationQuery) bool {
	return strings.TrimSpace(ctx.UserID) != "" && strings.TrimSpace(ctx.SessionID) != "" &&
		strings.TrimSpace(ctx.TenantRef) != "" && strings.TrimSpace(ctx.ClusterRef) != "" && ctx.ClusterRef != "all" &&
		strings.TrimSpace(ctx.Namespace) != "" && strings.TrimSpace(ctx.ResourceType) != "" &&
		strings.TrimSpace(ctx.ResourceName) != "" && strings.TrimSpace(ctx.Action) != ""
}

func authorizationSchemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS user_sessions (
  session_id CHAR(36) PRIMARY KEY,
  user_uuid CHAR(36) NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  token_version BIGINT NOT NULL DEFAULT 0,
  expires_at DATETIME NULL,
  revoked_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  INDEX idx_user_sessions_user_status (user_uuid, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS user_tenants (
  user_uuid CHAR(36) NOT NULL,
  tenant_id VARCHAR(64) NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (user_uuid, tenant_id),
  INDEX idx_user_tenants_tenant_status (tenant_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS roles (
  role_id CHAR(36) PRIMARY KEY,
  name VARCHAR(128) NOT NULL UNIQUE,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS permissions (
  permission_id CHAR(36) PRIMARY KEY,
  action VARCHAR(128) NOT NULL UNIQUE,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS user_roles (
  user_uuid CHAR(36) NOT NULL,
  tenant_id VARCHAR(64) NOT NULL,
  role_id CHAR(36) NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (user_uuid, tenant_id, role_id),
  INDEX idx_user_roles_lookup (user_uuid, tenant_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS role_permissions (
  role_id CHAR(36) NOT NULL,
  permission_id CHAR(36) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (role_id, permission_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS scope_assignments (
  scope_id BIGINT PRIMARY KEY AUTO_INCREMENT,
  role_id CHAR(36) NOT NULL,
  tenant_id VARCHAR(64) NOT NULL,
  cluster_id CHAR(36) NOT NULL,
  namespace VARCHAR(253) NOT NULL,
  resource_type VARCHAR(128) NOT NULL,
  resource_name VARCHAR(253) NOT NULL,
  action VARCHAR(128) NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  INDEX idx_scope_assignments_lookup (tenant_id, cluster_id, action, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		// tenant_clusters expresses explicit Tenant 1:N Cluster ownership (V9.2 §6.3).
		// UNIQUE(cluster_id) guarantees a cluster has exactly one owning tenant.
		`CREATE TABLE IF NOT EXISTS tenant_clusters (
  tenant_id VARCHAR(64) NOT NULL,
  cluster_id CHAR(36) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (tenant_id, cluster_id),
  UNIQUE KEY uq_tenant_clusters_cluster (cluster_id),
  INDEX idx_tenant_clusters_tenant (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}
}
