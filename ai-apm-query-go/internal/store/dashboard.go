package store

import (
	"database/sql"
	"errors"
)

// DashboardPanel Monitor 看板面板（dashboard_panels 表）。
type DashboardPanel struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Query     string `json:"query"`
	ChartType string `json:"chart_type"` // line | bar | area | gauge | table
	GridX     int    `json:"grid_x"`
	GridY     int    `json:"grid_y"`
	GridW     int    `json:"grid_w"`
	GridH     int    `json:"grid_h"`
	Span      int    `json:"span"`  // 12 栅格宽度
	Sort      int    `json:"sort"`  // 排序
	Enabled   bool   `json:"enabled"`
}

// DashboardPanelDAO Monitor 看板面板数据访问。
type DashboardPanelDAO struct{}

// List 返回全部启用的看板面板（按 sort 排序）。
func (d *DashboardPanelDAO) List() ([]DashboardPanel, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query("SELECT id, title, query, chart_type, grid_x, grid_y, grid_w, grid_h, span, sort, enabled FROM dashboard_panels ORDER BY sort, grid_y, grid_x")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DashboardPanel{}
	for rows.Next() {
		var p DashboardPanel
		var en int
		if err := rows.Scan(&p.ID, &p.Title, &p.Query, &p.ChartType, &p.GridX, &p.GridY, &p.GridW, &p.GridH, &p.Span, &p.Sort, &en); err != nil {
			return nil, err
		}
		p.Enabled = en == 1
		out = append(out, p)
	}
	return out, nil
}

// Upsert 插入或更新一个面板。
func (d *DashboardPanelDAO) Upsert(p DashboardPanel) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	if p.Span <= 0 {
		p.Span = 6
	}
	if p.GridW <= 0 {
		p.GridW = p.Span
	}
	_, err := conn.Exec(
		"INSERT INTO dashboard_panels (id, title, query, chart_type, grid_x, grid_y, grid_w, grid_h, span, sort, enabled) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) "+
			"ON DUPLICATE KEY UPDATE title=VALUES(title), query=VALUES(query), chart_type=VALUES(chart_type), grid_x=VALUES(grid_x), grid_y=VALUES(grid_y), grid_w=VALUES(grid_w), grid_h=VALUES(grid_h), span=VALUES(span), sort=VALUES(sort), enabled=VALUES(enabled)",
		p.ID, p.Title, p.Query, p.ChartType, p.GridX, p.GridY, p.GridW, p.GridH, p.Span, p.Sort, boolToInt(p.Enabled))
	return err
}

// Delete 删除一个面板。
func (d *DashboardPanelDAO) Delete(id string) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec("DELETE FROM dashboard_panels WHERE id = ?", id)
	return err
}

// Get 按 id 查单个面板。
func (d *DashboardPanelDAO) Get(id string) (*DashboardPanel, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	row := conn.QueryRow("SELECT id, title, query, chart_type, grid_x, grid_y, grid_w, grid_h, span, sort, enabled FROM dashboard_panels WHERE id = ?", id)
	var p DashboardPanel
	var en int
	if err := row.Scan(&p.ID, &p.Title, &p.Query, &p.ChartType, &p.GridX, &p.GridY, &p.GridW, &p.GridH, &p.Span, &p.Sort, &en); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	p.Enabled = en == 1
	return &p, nil
}
