package query

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ChangeScope 是 changes 资源域的作用域（租户/集群）。
type ChangeScope struct {
	TenantID    string
	ClusterID   string
	WindowStart *time.Time
	WindowEnd   *time.Time
}

// ChangeRecord 一条结构化变更记录（Change Agent / changes.read 的事实来源）。
// SoT 固定 ClickHouse observability.change_records（冻结职责），不经 ProxyAI。
type ChangeRecord struct {
	ChangeID   string `json:"change_id"`
	Service    string `json:"service"`
	ChangeType string `json:"change_type"` // deploy / config / scale / restart / delete
	StartTime  string `json:"start_time"`
	Actor      string `json:"actor"`
	Summary    string `json:"summary"`
	Revision   string `json:"revision"`
}

// ChangeRepository 是 changes 资源域的 domain repository（V9.2 Phase 6 mandatory gap）。
// 按冻结 SoT（ClickHouse change_records）查询，禁止 ProxyAI 成为事实查询 fallback。
type ChangeRepository struct {
	ch *ClickHouseRepo
}

// NewChangeRepository 构造 change repository，共享 ClickHouseExecutor。
func NewChangeRepository(ch *ClickHouseRepo) *ChangeRepository {
	return &ChangeRepository{ch: ch}
}

// List 查询指定服务（或全部）在时间范围内的变更记录。
// service 为空表示所有服务；since 为空表示近 24h。SQL ownership 在 repository。
func (r *ChangeRepository) List(ctx context.Context, scope ChangeScope, service, since string) ([]ChangeRecord, error) {
	var conds []string
	conds = append(conds, "tenant_id='"+scope.TenantID+"'")
	if scope.ClusterID != "" {
		conds = append(conds, "cluster_id='"+scope.ClusterID+"'")
	}
	if service != "" {
		conds = append(conds, "service_name="+sqlStr(service))
	}
	if scope.WindowStart != nil && scope.WindowEnd != nil {
		conds = append(conds, fmt.Sprintf("observability.change_records.start_time >= %s AND observability.change_records.start_time < %s", chTimeLiteral(*scope.WindowStart), chTimeLiteral(*scope.WindowEnd)))
	} else if since != "" {
		conds = append(conds, "observability.change_records.start_time >= '"+since+"'")
	} else {
		conds = append(conds, "observability.change_records.start_time >= now() - INTERVAL 24 HOUR")
	}
	sql := fmt.Sprintf(
		"SELECT change_id, service_name, change_type, toString(start_time) AS start_time, actor, summary, revision "+
			"FROM observability.change_records WHERE %s ORDER BY observability.change_records.start_time DESC LIMIT 200",
		strings.Join(conds, " AND "))
	rows, err := r.ch.QueryJSON(ctx, sql)
	if err != nil {
		return nil, err
	}
	out := make([]ChangeRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, ChangeRecord{
			ChangeID:   str(row, "change_id"),
			Service:    str(row, "service_name"),
			ChangeType: str(row, "change_type"),
			StartTime:  str(row, "start_time"),
			Actor:      str(row, "actor"),
			Summary:    str(row, "summary"),
			Revision:   str(row, "revision"),
		})
	}
	return out, nil
}

// ByService 查询指定服务最近 changes（Change Agent 时间线用）。service 必须非空。
func (r *ChangeRepository) ByService(ctx context.Context, scope ChangeScope, service string) ([]ChangeRecord, error) {
	return r.List(ctx, scope, service, "")
}
