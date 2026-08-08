package store

import (
	"database/sql"
	"errors"
	"time"
)

// Cluster 集群实体。
type Cluster struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Provider   string    `json:"provider"`
	Region     string    `json:"region"`
	Version    string    `json:"version"`
	NodeCount  int       `json:"node_count"`
	Status     string    `json:"status"`
	APIServer  string    `json:"api_server"`
	Kubeconfig string    `json:"kubeconfig,omitempty"`
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
		if err := rows.Scan(&c.ID, &c.Name, &c.Provider, &c.Region, &c.Version,
			&c.NodeCount, &c.Status, &c.APIServer, &c.Kubeconfig, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
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
	if err := row.Scan(&c.ID, &c.Name, &c.Provider, &c.Region, &c.Version,
		&c.NodeCount, &c.Status, &c.APIServer, &c.Kubeconfig, &c.CreatedAt, &c.UpdatedAt); err != nil {
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
	if err := row.Scan(&c.ID, &c.Name, &c.Provider, &c.Region, &c.Version,
		&c.NodeCount, &c.Status, &c.APIServer, &c.Kubeconfig, &c.CreatedAt, &c.UpdatedAt); err != nil {
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
