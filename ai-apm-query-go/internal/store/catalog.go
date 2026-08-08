package store

import (
	"database/sql"
	"errors"
	"time"
)

// ServiceCatalog 服务目录项。
type ServiceCatalog struct {
	ID          int64     `json:"id"`
	ServiceName string    `json:"service_name"`
	DisplayName string    `json:"display_name"`
	Description string    `json:"description"`
	Owner       string    `json:"owner"`
	Team        string    `json:"team"`
	Tags        string    `json:"tags"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CatalogDAO 服务目录数据访问对象。
type CatalogDAO struct{}

// List 返回服务目录（分页）。
func (d *CatalogDAO) List(page, size int) ([]ServiceCatalog, int, error) {
	conn := GetDB()
	if conn == nil {
		return nil, 0, errors.New("mysql unavailable")
	}
	if size <= 0 {
		size = 100
	}
	offset := (page - 1) * size
	var total int
	if err := conn.QueryRow("SELECT COUNT(*) FROM service_catalog").Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := conn.Query(
		"SELECT id, service_name, display_name, description, owner, team, tags, status, created_at, updated_at FROM service_catalog ORDER BY service_name LIMIT ? OFFSET ?",
		size, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []ServiceCatalog{}
	for rows.Next() {
		var c ServiceCatalog
		if err := rows.Scan(&c.ID, &c.ServiceName, &c.DisplayName, &c.Description,
			&c.Owner, &c.Team, &c.Tags, &c.Status, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, c)
	}
	return items, total, nil
}

// GetByService 按服务名查目录项。
func (d *CatalogDAO) GetByService(service string) (*ServiceCatalog, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	row := conn.QueryRow(
		"SELECT id, service_name, display_name, description, owner, team, tags, status, created_at, updated_at FROM service_catalog WHERE service_name = ?",
		service)
	var c ServiceCatalog
	if err := row.Scan(&c.ID, &c.ServiceName, &c.DisplayName, &c.Description,
		&c.Owner, &c.Team, &c.Tags, &c.Status, &c.CreatedAt, &c.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// Create 新增目录项。
func (d *CatalogDAO) Create(c *ServiceCatalog) (int64, error) {
	conn := GetDB()
	if conn == nil {
		return 0, errors.New("mysql unavailable")
	}
	res, err := conn.Exec(
		"INSERT INTO service_catalog (service_name, display_name, description, owner, team, tags, status) VALUES (?, ?, ?, ?, ?, ?, ?)",
		c.ServiceName, c.DisplayName, c.Description, c.Owner, c.Team, c.Tags, c.Status)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Update 更新目录项。
func (d *CatalogDAO) Update(id int64, c *ServiceCatalog) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(
		"UPDATE service_catalog SET display_name=?, description=?, owner=?, team=?, tags=?, status=? WHERE id=?",
		c.DisplayName, c.Description, c.Owner, c.Team, c.Tags, c.Status, id)
	return err
}

// Delete 删除目录项。
func (d *CatalogDAO) Delete(id int64) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec("DELETE FROM service_catalog WHERE id=?", id)
	return err
}
