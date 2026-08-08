package store

import (
	"database/sql"
	"errors"
	"time"
)

// Device 设备实体。
type Device struct {
	ID        int64     `json:"id"`
	Hostname  string    `json:"hostname"`
	IP        string    `json:"ip"`
	OS        string    `json:"os"`
	CPUCores  int       `json:"cpu_cores"`
	MemoryMB  int64     `json:"memory_mb"`
	Status    string    `json:"status"`
	Role      string    `json:"role"`
	Location  string    `json:"location"`
	Tags      string    `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DeviceDAO 设备数据访问对象。
type DeviceDAO struct{}

// List 返回设备列表（分页）。
func (d *DeviceDAO) List(page, size int) ([]Device, int, error) {
	conn := GetDB()
	if conn == nil {
		return nil, 0, errors.New("mysql unavailable")
	}
	if size <= 0 {
		size = 100
	}
	offset := (page - 1) * size
	var total int
	if err := conn.QueryRow("SELECT COUNT(*) FROM devices").Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := conn.Query(
		"SELECT id, hostname, ip, os, cpu_cores, memory_mb, status, role, location, tags, created_at, updated_at FROM devices ORDER BY id LIMIT ? OFFSET ?",
		size, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []Device{}
	for rows.Next() {
		var dv Device
		if err := rows.Scan(&dv.ID, &dv.Hostname, &dv.IP, &dv.OS, &dv.CPUCores,
			&dv.MemoryMB, &dv.Status, &dv.Role, &dv.Location, &dv.Tags, &dv.CreatedAt, &dv.UpdatedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, dv)
	}
	return items, total, nil
}

// GetByHostname 按主机名查设备。
func (d *DeviceDAO) GetByHostname(hostname string) (*Device, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	row := conn.QueryRow(
		"SELECT id, hostname, ip, os, cpu_cores, memory_mb, status, role, location, tags, created_at, updated_at FROM devices WHERE hostname = ?",
		hostname)
	var dv Device
	if err := row.Scan(&dv.ID, &dv.Hostname, &dv.IP, &dv.OS, &dv.CPUCores,
		&dv.MemoryMB, &dv.Status, &dv.Role, &dv.Location, &dv.Tags, &dv.CreatedAt, &dv.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &dv, nil
}

// Create 新增设备。
func (d *DeviceDAO) Create(dv *Device) (int64, error) {
	conn := GetDB()
	if conn == nil {
		return 0, errors.New("mysql unavailable")
	}
	res, err := conn.Exec(
		"INSERT INTO devices (hostname, ip, os, cpu_cores, memory_mb, status, role, location, tags) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		dv.Hostname, dv.IP, dv.OS, dv.CPUCores, dv.MemoryMB, dv.Status, dv.Role, dv.Location, dv.Tags)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Update 更新设备。
func (d *DeviceDAO) Update(id int64, dv *Device) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(
		"UPDATE devices SET hostname=?, ip=?, os=?, cpu_cores=?, memory_mb=?, status=?, role=?, location=?, tags=? WHERE id=?",
		dv.Hostname, dv.IP, dv.OS, dv.CPUCores, dv.MemoryMB, dv.Status, dv.Role, dv.Location, dv.Tags, id)
	return err
}

// Delete 删除设备。
func (d *DeviceDAO) Delete(id int64) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec("DELETE FROM devices WHERE id=?", id)
	return err
}
