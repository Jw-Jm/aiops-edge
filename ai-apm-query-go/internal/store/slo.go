package store

import (
	"database/sql"
	"errors"
)

// SLOTarget SLO 目标（slo_targets 表）。
type SLOTarget struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Service       string  `json:"service"`
	SLOType       string  `json:"slo_type"` // availability | latency
	Target        float64 `json:"target"`   // 如 99.9
	WindowSeconds int     `json:"window_seconds"`
	Enabled       bool    `json:"enabled"`
}

// SLOTargetDAO SLO 目标数据访问。
type SLOTargetDAO struct{}

// List 返回全部 SLO 目标。
func (d *SLOTargetDAO) List() ([]SLOTarget, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query("SELECT id, name, service, slo_type, target, window_seconds, enabled FROM slo_targets ORDER BY service, name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SLOTarget{}
	for rows.Next() {
		var s SLOTarget
		var en int
		if err := rows.Scan(&s.ID, &s.Name, &s.Service, &s.SLOType, &s.Target, &s.WindowSeconds, &en); err != nil {
			return nil, err
		}
		s.Enabled = en == 1
		out = append(out, s)
	}
	return out, nil
}

// Get 按 id 查单个 SLO 目标（供 burn_rate 规则引用）。
func (d *SLOTargetDAO) Get(id string) (*SLOTarget, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	row := conn.QueryRow("SELECT id, name, service, slo_type, target, window_seconds, enabled FROM slo_targets WHERE id = ?", id)
	var s SLOTarget
	var en int
	if err := row.Scan(&s.ID, &s.Name, &s.Service, &s.SLOType, &s.Target, &s.WindowSeconds, &en); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	s.Enabled = en == 1
	return &s, nil
}

// Upsert 插入或更新 SLO 目标。
func (d *SLOTargetDAO) Upsert(s SLOTarget) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(
		"INSERT INTO slo_targets (id, name, service, slo_type, target, window_seconds, enabled) VALUES (?, ?, ?, ?, ?, ?, ?) "+
			"ON DUPLICATE KEY UPDATE name=VALUES(name), service=VALUES(service), slo_type=VALUES(slo_type), target=VALUES(target), window_seconds=VALUES(window_seconds), enabled=VALUES(enabled)",
		s.ID, s.Name, s.Service, s.SLOType, s.Target, s.WindowSeconds, boolToInt(s.Enabled))
	return err
}

// Delete 删除 SLO 目标。
func (d *SLOTargetDAO) Delete(id string) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec("DELETE FROM slo_targets WHERE id = ?", id)
	return err
}
