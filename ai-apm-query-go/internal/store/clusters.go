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
)

var canonicalUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// Cluster 集群实体。
type Cluster struct {
	// ID is legacy migration metadata only. New authorization and registry code uses ClusterID.
	ID            int64  `json:"id,omitempty"`
	ClusterID     string `json:"cluster_id"`
	TenantID      string `json:"tenant_id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Environment   string `json:"environment"`
	Region        string `json:"region"`
	CredentialRef string `json:"credential_ref,omitempty"`
	Status        string `json:"status"`
	Provider      string `json:"provider,omitempty"`
	Version       string `json:"version,omitempty"`
	NodeCount     int    `json:"node_count,omitempty"`
	APIServer     string `json:"api_server,omitempty"`
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
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(clusterRef) == "" || clusterRef == "all" || isLegacyIntegerRef(clusterRef) {
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
FROM clusters WHERE tenant_id = ? AND %s = ? AND cluster_id IS NOT NULL AND cluster_id != '' AND lifecycle_status != 'deleted'`, field)
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
		"SELECT id, name, provider, region, version, node_count, status, api_server, kubeconfig, created_at, updated_at FROM clusters ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Cluster{}
	for rows.Next() {
		var c Cluster
		var kc sql.NullString
		if err := rows.Scan(&c.ID, &c.Name, &c.Provider, &c.Region, &c.Version,
			&c.NodeCount, &c.Status, &c.APIServer, &kc, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.Kubeconfig = kc.String // A-2 修复：kubeconfig 为 NULL 时容忍（转空串）
		items = append(items, c)
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
