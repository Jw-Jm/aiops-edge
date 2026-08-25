package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrInvalidClusterRef means a caller supplied a non-canonical cluster reference.
	ErrInvalidClusterRef = errors.New("invalid cluster reference")
	// ErrClusterNotFound means no active canonical registry record matched the reference.
	ErrClusterNotFound = errors.New("cluster not found")
	// ErrClusterAmbiguous means corrupt registry data returned more than one canonical record.
	ErrClusterAmbiguous = errors.New("cluster reference is ambiguous")
	// ErrClusterIdentityMismatch means a resolved credential reached a Kubernetes API
	// whose observed kube-system identity differs from the one bound to the canonical
	// cluster registration. The boundary MUST fail closed and never return that client.
	ErrClusterIdentityMismatch = errors.New("cluster credential identity mismatch")
	// ErrClusterIdentityMissing means the canonical cluster registration has no bound
	// kubernetes_identity_uid, so the boundary cannot prove identity and must fail closed.
	ErrClusterIdentityMissing = errors.New("cluster registration has no kubernetes identity")
	// ErrClusterIdentityDuplicate means another ACTIVE canonical cluster is already bound
	// to the same observed Kubernetes identity; duplicate active registration is rejected.
	ErrClusterIdentityDuplicate = errors.New("kubernetes cluster identity already bound to an active cluster")
)

var canonicalUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// Cluster 集群实体。
type Cluster struct {
	// ID is legacy migration metadata only. New authorization and registry code uses ClusterID.
	ID              int64  `json:"id,omitempty"`
	ClusterID       string `json:"cluster_id"`
	TenantID        string `json:"tenant_id"`
	Slug            string `json:"slug"`
	Name            string `json:"name"`
	Environment     string `json:"environment"`
	Region          string `json:"region"`
	CredentialRef   string `json:"credential_ref,omitempty"`
	Status          string `json:"status"`
	LifecycleStatus string `json:"lifecycle_status,omitempty"`
	Provider        string `json:"provider,omitempty"`
	Version         string `json:"version,omitempty"`
	NodeCount       int    `json:"node_count,omitempty"`
	APIServer       string `json:"api_server,omitempty"`
	// Type/Capabilities/Labels/DeletedAt are V9.2 §9 minimum registry fields.
	Type         string     `json:"type,omitempty"`
	Capabilities string     `json:"capabilities,omitempty"`
	Labels       string     `json:"labels,omitempty"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
	// KubernetesIdentityUID is the authoritative Kubernetes cluster identity
	// (kube-system Namespace metadata.uid) observed at registration time. It is
	// distinct from the AIOps canonical cluster_id and is used by the Kubernetes
	// Access Boundary to fail closed on credential/cluster identity mismatch.
	KubernetesIdentityUID string `json:"kubernetes_identity_uid,omitempty"`
	// Kubeconfig is retained solely for legacy callers pending controlled cutover; it is never serialized.
	Kubeconfig string    `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ClusterNode 集群节点。
type ClusterNode struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	IP        string `json:"ip"`
	OS        string `json:"os"`
	CPU       string `json:"cpu"`
	Memory    string `json:"memory"`
	Kubelet   string `json:"kubelet"`
	CreatedAt string `json:"created_at"`
}

// ClusterDAO 集群数据访问对象。
type ClusterDAO struct{}

// ResolveRef resolves a UUID or readable slug to a single active canonical registry record.
// It intentionally never reads credential material from the legacy kubeconfig column.
func (d *ClusterDAO) ResolveRef(tenantID, clusterRef string) (*Cluster, error) {
	// V9.2: reject all / default / numeric / empty refs (no default-cluster fallback).
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(clusterRef) == "" ||
		clusterRef == "all" || clusterRef == "default" || isLegacyIntegerRef(clusterRef) {
		return nil, ErrInvalidClusterRef
	}
	conn := GetDB()
	if conn == nil {
		return nil, ErrMySQLUnavailable
	}

	field := "slug"
	if canonicalUUIDPattern.MatchString(clusterRef) {
		field = "cluster_id"
	}
	query := fmt.Sprintf(`SELECT id, cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status, created_at, updated_at
FROM clusters WHERE tenant_id = ? AND %s = ? AND cluster_id IS NOT NULL AND cluster_id != '' AND lifecycle_status IN ('active', 'ready')`, field)
	rows, err := conn.Query(query, tenantID, clusterRef)
	if err != nil {
		return nil, fmt.Errorf("resolve cluster: %w", err)
	}
	defer rows.Close()

	var matches []Cluster
	for rows.Next() {
		var c Cluster
		var credential sql.NullString
		var createdAt, updatedAt sql.NullTime
		if err := rows.Scan(&c.ID, &c.ClusterID, &c.TenantID, &c.Slug, &c.Name, &c.Environment, &c.Region, &credential, &c.Status, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan cluster registry record: %w", err)
		}
		c.CredentialRef = credential.String
		if !isAuthorizedClusterLifecycle(c.Status) {
			continue
		}
		if createdAt.Valid {
			c.CreatedAt = createdAt.Time
		}
		if updatedAt.Valid {
			c.UpdatedAt = updatedAt.Time
		}
		matches = append(matches, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cluster registry records: %w", err)
	}
	switch len(matches) {
	case 0:
		return nil, ErrClusterNotFound
	case 1:
		return &matches[0], nil
	default:
		return nil, ErrClusterAmbiguous
	}
}

func isAuthorizedClusterLifecycle(status string) bool {
	return status == "active" || status == "ready"
}

// GetByClusterID returns the canonical registry record for a cluster UUID
// (including credential_ref). cluster_id is globally unique; no tenant is needed.
func (d *ClusterDAO) GetByClusterID(clusterID string) (*Cluster, error) {
	if !canonicalUUIDPattern.MatchString(clusterID) {
		return nil, ErrInvalidClusterRef
	}
	conn := GetDB()
	if conn == nil {
		return nil, ErrMySQLUnavailable
	}
	var c Cluster
	var credential sql.NullString
	var identity sql.NullString
	var createdAt, updatedAt sql.NullTime
	err := conn.QueryRow(`SELECT id, cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status, kubernetes_identity_uid, created_at, updated_at
FROM clusters WHERE cluster_id = ? AND cluster_id IS NOT NULL AND cluster_id != '' AND lifecycle_status IN ('active', 'ready') LIMIT 1`,
		clusterID).Scan(&c.ID, &c.ClusterID, &c.TenantID, &c.Slug, &c.Name, &c.Environment, &c.Region, &credential, &c.Status, &identity, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrClusterNotFound
	}
	if err != nil {
		return nil, err
	}
	c.CredentialRef = credential.String
	c.KubernetesIdentityUID = identity.String
	if createdAt.Valid {
		c.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		c.UpdatedAt = updatedAt.Time
	}
	return &c, nil
}

func isLegacyIntegerRef(ref string) bool {
	_, err := strconv.ParseInt(ref, 10, 64)
	return err == nil
}

// List 返回集群列表。
func (d *ClusterDAO) List() ([]Cluster, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query(
		"SELECT id, cluster_id, tenant_id, slug, name, provider, region, version, node_count, status, api_server, kubeconfig, environment, lifecycle_status, credential_ref, kubernetes_identity_uid, created_at, updated_at FROM clusters ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Cluster{}
	for rows.Next() {
		var c Cluster
		var clusterID, tenantID, slug, kc, credentialRef, identity sql.NullString
		if err := rows.Scan(&c.ID, &clusterID, &tenantID, &slug, &c.Name, &c.Provider, &c.Region, &c.Version,
			&c.NodeCount, &c.Status, &c.APIServer, &kc, &c.Environment, &c.LifecycleStatus, &credentialRef, &identity, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.ClusterID = clusterID.String
		c.TenantID = tenantID.String
		c.Slug = slug.String
		c.Kubeconfig = kc.String // A-2 修复：kubeconfig 为 NULL 时容忍（转空串）
		c.CredentialRef = credentialRef.String
		c.KubernetesIdentityUID = identity.String
		items = append(items, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// GetByID 按 ID 查集群。
func (d *ClusterDAO) GetByID(id int64) (*Cluster, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	row := conn.QueryRow(
		"SELECT id, name, provider, region, version, node_count, status, api_server, kubeconfig, created_at, updated_at FROM clusters WHERE id = ?",
		id)
	var c Cluster
	var kc sql.NullString
	if err := row.Scan(&c.ID, &c.Name, &c.Provider, &c.Region, &c.Version,
		&c.NodeCount, &c.Status, &c.APIServer, &kc, &c.CreatedAt, &c.UpdatedAt); err != nil {
		c.Kubeconfig = kc.String // A-2 修复：kubeconfig 为 NULL 时容忍（转空串）
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// GetByName 按名称查集群。
func (d *ClusterDAO) GetByName(name string) (*Cluster, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	row := conn.QueryRow(
		"SELECT id, name, provider, region, version, node_count, status, api_server, kubeconfig, created_at, updated_at FROM clusters WHERE name = ?",
		name)
	var c Cluster
	var kc sql.NullString
	if err := row.Scan(&c.ID, &c.Name, &c.Provider, &c.Region, &c.Version,
		&c.NodeCount, &c.Status, &c.APIServer, &kc, &c.CreatedAt, &c.UpdatedAt); err != nil {
		c.Kubeconfig = kc.String // A-2 修复：kubeconfig 为 NULL 时容忍（转空串）
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// Create 新增集群（含 kubeconfig），返回新 ID。
func (d *ClusterDAO) Create(c *Cluster) (int64, error) {
	conn := GetDB()
	if conn == nil {
		return 0, errors.New("mysql unavailable")
	}
	res, err := conn.Exec(
		"INSERT INTO clusters (name, provider, region, version, node_count, status, api_server, kubeconfig) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		c.Name, c.Provider, c.Region, c.Version, c.NodeCount, c.Status, c.APIServer, c.Kubeconfig)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Upsert 插入或更新集群（按 name 唯一）。
func (d *ClusterDAO) Upsert(c *Cluster) (int64, error) {
	conn := GetDB()
	if conn == nil {
		return 0, errors.New("mysql unavailable")
	}
	res, err := conn.Exec(
		"INSERT INTO clusters (name, provider, region, version, node_count, status, api_server, kubeconfig) VALUES (?, ?, ?, ?, ?, ?, ?, ?) "+
			"ON DUPLICATE KEY UPDATE provider=?, region=?, version=?, node_count=?, status=?, api_server=?, kubeconfig=?",
		c.Name, c.Provider, c.Region, c.Version, c.NodeCount, c.Status, c.APIServer, c.Kubeconfig,
		c.Provider, c.Region, c.Version, c.NodeCount, c.Status, c.APIServer, c.Kubeconfig)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		existing, _ := d.GetByName(c.Name)
		if existing != nil {
			return existing.ID, nil
		}
	}
	return id, nil
}

// Update 更新集群（含 kubeconfig；kubeconfig 为空则不覆盖已存值）。
func (d *ClusterDAO) Update(id int64, c *Cluster) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	if c.Kubeconfig != "" {
		_, err := conn.Exec(
			"UPDATE clusters SET name=?, provider=?, region=?, version=?, node_count=?, status=?, api_server=?, kubeconfig=? WHERE id=?",
			c.Name, c.Provider, c.Region, c.Version, c.NodeCount, c.Status, c.APIServer, c.Kubeconfig, id)
		return err
	}
	_, err := conn.Exec(
		"UPDATE clusters SET name=?, provider=?, region=?, version=?, node_count=?, status=?, api_server=? WHERE id=?",
		c.Name, c.Provider, c.Region, c.Version, c.NodeCount, c.Status, c.APIServer, id)
	return err
}

// Delete 删除集群。
func (d *ClusterDAO) Delete(id int64) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec("DELETE FROM clusters WHERE id=?", id)
	return err
}

// EnsureTenantCluster records explicit Tenant 1:N Cluster ownership (V9.2 §6.3).
// Idempotent; preserves the single owning tenant enforced by UNIQUE(cluster_id).
func (d *ClusterDAO) EnsureTenantCluster(tenantID, clusterID string) error {
	if strings.TrimSpace(tenantID) == "" || !canonicalUUIDPattern.MatchString(clusterID) {
		return ErrInvalidClusterRef
	}
	conn := GetDB()
	if conn == nil {
		return ErrMySQLUnavailable
	}
	_, err := conn.Exec(
		"INSERT IGNORE INTO tenant_clusters (tenant_id, cluster_id) VALUES (?, ?)",
		tenantID, clusterID)
	return err
}

// RegisterCluster registers a canonical cluster (immutable UUID, slug, name,
// tenant ownership, credential_ref, type/capabilities/labels) and records
// Tenant 1:N Cluster ownership (V9.2 §6.3, §9). Idempotent via unique keys.
func (d *ClusterDAO) RegisterCluster(c *Cluster) error {
	if c == nil {
		return ErrInvalidClusterRef
	}
	if !canonicalUUIDPattern.MatchString(c.ClusterID) {
		return fmt.Errorf("%w: cluster_id must be a canonical UUID", ErrInvalidClusterRef)
	}
	if strings.TrimSpace(c.TenantID) == "" || strings.TrimSpace(c.Slug) == "" {
		return fmt.Errorf("%w: tenant_id and slug are required", ErrInvalidClusterRef)
	}
	conn := GetDB()
	if conn == nil {
		return ErrMySQLUnavailable
	}
	status := c.Status
	if status == "" {
		status = "active"
	}
	// V9.2 P3.10c-final: a physical Kubernetes cluster (identified by its
	// kube-system Namespace UID) must not be active under two canonical UUIDs.
	// Reject duplicate active registration before writing the new row.
	if strings.TrimSpace(c.KubernetesIdentityUID) != "" {
		existing, err := d.FindActiveByKubeSystemUID(c.KubernetesIdentityUID)
		if err != nil {
			return fmt.Errorf("register cluster identity check: %w", err)
		}
		if existing != nil && existing.ClusterID != c.ClusterID {
			return fmt.Errorf("%w: uid=%q already bound to cluster_id=%s", ErrClusterIdentityDuplicate, c.KubernetesIdentityUID, existing.ClusterID)
		}
	}
	_, err := conn.Exec(`INSERT INTO clusters
(cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status, type, capabilities, labels, kubernetes_identity_uid)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
tenant_id=VALUES(tenant_id), name=VALUES(name), environment=VALUES(environment), region=VALUES(region),
credential_ref=VALUES(credential_ref), lifecycle_status=VALUES(lifecycle_status), type=VALUES(type),
capabilities=VALUES(capabilities), labels=VALUES(labels), kubernetes_identity_uid=VALUES(kubernetes_identity_uid)`,
		c.ClusterID, c.TenantID, c.Slug, c.Name, c.Environment, c.Region, c.CredentialRef, status,
		c.Type, c.Capabilities, c.Labels, c.KubernetesIdentityUID)
	if err != nil {
		return fmt.Errorf("register cluster: %w", err)
	}
	// Record explicit Tenant 1:N Cluster ownership.
	return d.EnsureTenantCluster(c.TenantID, c.ClusterID)
}

// FindActiveByKubeSystemUID returns the canonical cluster currently bound to a
// Kubernetes identity UID, or nil when none is ACTIVE with that identity. Used to
// reject duplicate active registration of the same physical Kubernetes cluster.
func (d *ClusterDAO) FindActiveByKubeSystemUID(uid string) (*Cluster, error) {
	if strings.TrimSpace(uid) == "" {
		return nil, ErrClusterIdentityMissing
	}
	conn := GetDB()
	if conn == nil {
		return nil, ErrMySQLUnavailable
	}
	var c Cluster
	var credential sql.NullString
	var identity sql.NullString
	var createdAt, updatedAt sql.NullTime
	err := conn.QueryRow(`SELECT id, cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status, kubernetes_identity_uid, created_at, updated_at
FROM clusters WHERE kubernetes_identity_uid = ? AND lifecycle_status IN ('active', 'ready') AND deleted_at IS NULL LIMIT 1`,
		uid).Scan(&c.ID, &c.ClusterID, &c.TenantID, &c.Slug, &c.Name, &c.Environment, &c.Region, &credential, &c.Status, &identity, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.CredentialRef = credential.String
	c.KubernetesIdentityUID = identity.String
	if createdAt.Valid {
		c.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		c.UpdatedAt = updatedAt.Time
	}
	return &c, nil
}

// TenantClustersForCluster returns the owning tenant of a canonical cluster, if any.
// A single row is expected; more than one would violate UNIQUE(cluster_id).
func (d *ClusterDAO) TenantClustersForCluster(clusterID string) (string, error) {
	if !canonicalUUIDPattern.MatchString(clusterID) {
		return "", ErrInvalidClusterRef
	}
	conn := GetDB()
	if conn == nil {
		return "", ErrMySQLUnavailable
	}
	var tenantID string
	err := conn.QueryRow("SELECT tenant_id FROM tenant_clusters WHERE cluster_id = ?", clusterID).Scan(&tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrClusterNotFound
	}
	if err != nil {
		return "", err
	}
	return tenantID, nil
}
