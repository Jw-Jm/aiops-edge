package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ---------- 实体 ----------

// TopologyNode 拓扑顶点（typed property graph 的 node）。
type TopologyNode struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"`
	Name      string    `json:"name"`
	PropsJSON string    `json:"props_json,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TopologyRelation 拓扑有向边（src→dst, type 唯一）。
type TopologyRelation struct {
	ID        int64     `json:"id"`
	SrcID     int64     `json:"src_id"`
	DstID     int64     `json:"dst_id"`
	Type      string    `json:"type"`
	PropsJSON string    `json:"props_json,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// TopologyNodeType 节点类型目录。
type TopologyNodeType struct {
	Name          string `json:"name"`
	DisplayName   string `json:"display_name"`
	DisplayNameEN string `json:"display_name_en,omitempty"`
	Builtin       bool   `json:"builtin"`
	Tier          int    `json:"tier"`
	Description   string `json:"description"`
}

// TopologyRelationType 关系类型目录。
type TopologyRelationType struct {
	Name              string `json:"name"`
	DisplayName       string `json:"display_name"`
	DisplayNameEN     string `json:"display_name_en,omitempty"`
	Builtin           bool   `json:"builtin"`
	PropagatesFailure bool   `json:"propagates_failure"`
	Direction         string `json:"direction"`
	SemanticsTag      string `json:"semantics_tag"`
	Description       string `json:"description"`
}

// ---------- 种子数据 ----------

// builtinNodeTypes 内置 5 种节点类型（tier 0=业务顶 → 底层）。
var builtinNodeTypes = []TopologyNodeType{
	{Name: "app", DisplayName: "应用", DisplayNameEN: "App", Builtin: true, Tier: 0, Description: "业务系统 / 产品 — 跨多服务的业务能力；故障影响评估的业务视角。"},
	{Name: "service", DisplayName: "服务", DisplayNameEN: "Service", Builtin: true, Tier: 1, Description: "可部署的进程 / 容器 / 二进制 — 一个 git 仓库一个 SLO 的研发单位。"},
	{Name: "cluster", DisplayName: "集群", DisplayNameEN: "Cluster", Builtin: true, Tier: 2, Description: "一组节点状态绑定的有状态组件（MySQL 主备、Etcd 共识、Redis Sentinel）。"},
	{Name: "device", DisplayName: "设备", DisplayNameEN: "Device", Builtin: true, Tier: 3, Description: "物理 / 逻辑主机 — edge agent 所在机器。"},
	{Name: "rack", DisplayName: "机架", DisplayNameEN: "Rack", Builtin: true, Tier: 4, Description: "物理位置（机房 / 机架 / 可用区）— 故障域物理边界。"},
}

// builtinRelationTypes 内置 7 种关系类型（语义关系）。
var builtinRelationTypes = []TopologyRelationType{
	{Name: "member_of", DisplayName: "成员属于", DisplayNameEN: "Member of", Builtin: true, PropagatesFailure: false, Direction: "src_to_dst", SemanticsTag: "aggregation", Description: "src 是 dst 的成员。聚合关系，不传播故障。"},
	{Name: "depends_on", DisplayName: "依赖", DisplayNameEN: "Depends on", Builtin: true, PropagatesFailure: true, Direction: "dst_to_src", SemanticsTag: "hard_dep", Description: "src 依赖 dst；dst 出故障会影响 src。核心边类型。"},
	{Name: "deployed_on", DisplayName: "部署于", DisplayNameEN: "Deployed on", Builtin: true, PropagatesFailure: true, Direction: "dst_to_src", SemanticsTag: "runtime_dep", Description: "src 部署在 dst 上；dst 故障传到 src。"},
	{Name: "replicates_to", DisplayName: "复制到", DisplayNameEN: "Replicates to", Builtin: true, PropagatesFailure: false, Direction: "bidirectional", SemanticsTag: "redundancy", Description: "src 与 dst 互为副本；参与冗余度计算。"},
	{Name: "monitors", DisplayName: "监控", DisplayNameEN: "Monitors", Builtin: true, PropagatesFailure: false, Direction: "src_to_dst", SemanticsTag: "observation", Description: "src 监控 dst。纯观测关系。"},
	{Name: "routes_to", DisplayName: "路由到", DisplayNameEN: "Routes to", Builtin: true, PropagatesFailure: true, Direction: "src_to_dst", SemanticsTag: "traffic", Description: "src 把流量打到 dst；上游故障导致下游不可达。"},
	{Name: "connected_to", DisplayName: "连接到", DisplayNameEN: "Connected to", Builtin: true, PropagatesFailure: false, Direction: "bidirectional", SemanticsTag: "observation", Description: "两个设备之间存在已确认的网络连接。"},
}

// SeedTopologyTypes upsert 内置 node_types / relation_types（幂等，可重复执行）。
func SeedTopologyTypes() error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	for _, nt := range builtinNodeTypes {
		if _, err := conn.Exec(
			"INSERT INTO topology_node_types (name, display_name, display_name_en, builtin, tier, description) VALUES (?,?,?,?,?,?) "+
				"ON DUPLICATE KEY UPDATE display_name=VALUES(display_name), display_name_en=VALUES(display_name_en), tier=VALUES(tier), description=VALUES(description)",
			nt.Name, nt.DisplayName, nt.DisplayNameEN, boolInt(nt.Builtin), nt.Tier, nt.Description); err != nil {
			return err
		}
	}
	for _, rt := range builtinRelationTypes {
		if _, err := conn.Exec(
			"INSERT INTO topology_relation_types (name, display_name, display_name_en, builtin, propagates_failure, direction, semantics_tag, description) VALUES (?,?,?,?,?,?,?,?) "+
				"ON DUPLICATE KEY UPDATE display_name=VALUES(display_name), display_name_en=VALUES(display_name_en), propagates_failure=VALUES(propagates_failure), direction=VALUES(direction), semantics_tag=VALUES(semantics_tag), description=VALUES(description)",
			rt.Name, rt.DisplayName, rt.DisplayNameEN, boolInt(rt.Builtin), boolInt(rt.PropagatesFailure), rt.Direction, rt.SemanticsTag, rt.Description); err != nil {
			return err
		}
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---------- TopologyNodeDAO ----------

// TopologyNodeDAO 拓扑顶点数据访问对象。
type TopologyNodeDAO struct{}

// List 按类型/关键字过滤，返回节点列表与总数。
func (d *TopologyNodeDAO) List(typ, q string, limit, offset int) ([]TopologyNode, int, error) {
	conn := GetDB()
	if conn == nil {
		return nil, 0, errors.New("mysql unavailable")
	}
	if limit <= 0 {
		limit = 200
	}
	var where []string
	var args []interface{}
	if typ != "" {
		where = append(where, "type = ?")
		args = append(args, typ)
	}
	if q != "" {
		where = append(where, "name LIKE ?")
		args = append(args, "%"+q+"%")
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := conn.QueryRow("SELECT COUNT(*) FROM topology_nodes"+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := conn.Query("SELECT id, type, name, props_json, created_at, updated_at FROM topology_nodes"+cond+" ORDER BY id LIMIT ? OFFSET ?",
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []TopologyNode{}
	for rows.Next() {
		var n TopologyNode
		if err := rows.Scan(&n.ID, &n.Type, &n.Name, &n.PropsJSON, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, n)
	}
	return items, total, nil
}

// Get 按 ID 查节点。
func (d *TopologyNodeDAO) Get(id int64) (*TopologyNode, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	row := conn.QueryRow("SELECT id, type, name, props_json, created_at, updated_at FROM topology_nodes WHERE id = ?", id)
	var n TopologyNode
	if err := row.Scan(&n.ID, &n.Type, &n.Name, &n.PropsJSON, &n.CreatedAt, &n.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &n, nil
}

// Create 新增节点。
func (d *TopologyNodeDAO) Create(n *TopologyNode) (int64, error) {
	conn := GetDB()
	if conn == nil {
		return 0, errors.New("mysql unavailable")
	}
	res, err := conn.Exec("INSERT INTO topology_nodes (type, name, props_json) VALUES (?, ?, ?)",
		n.Type, n.Name, n.PropsJSON)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Update 更新节点（name/props）。
func (d *TopologyNodeDAO) Update(id int64, n *TopologyNode) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec("UPDATE topology_nodes SET type=?, name=?, props_json=? WHERE id=?", n.Type, n.Name, n.PropsJSON, id)
	return err
}

// Delete 删除节点。
func (d *TopologyNodeDAO) Delete(id int64) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec("DELETE FROM topology_nodes WHERE id=?", id)
	return err
}

// ---------- TopologyRelationDAO ----------

// TopologyRelationDAO 拓扑关系数据访问对象。
type TopologyRelationDAO struct{}

// List 按 src/dst/type 过滤。
func (d *TopologyRelationDAO) List(srcID, dstID int64, typ string, limit, offset int) ([]TopologyRelation, int, error) {
	conn := GetDB()
	if conn == nil {
		return nil, 0, errors.New("mysql unavailable")
	}
	if limit <= 0 {
		limit = 200
	}
	var where []string
	var args []interface{}
	if srcID > 0 {
		where = append(where, "src_id = ?")
		args = append(args, srcID)
	}
	if dstID > 0 {
		where = append(where, "dst_id = ?")
		args = append(args, dstID)
	}
	if typ != "" {
		where = append(where, "type = ?")
		args = append(args, typ)
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := conn.QueryRow("SELECT COUNT(*) FROM topology_relations"+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := conn.Query("SELECT id, src_id, dst_id, type, props_json, created_at FROM topology_relations"+cond+" ORDER BY id LIMIT ? OFFSET ?",
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []TopologyRelation{}
	for rows.Next() {
		var r TopologyRelation
		if err := rows.Scan(&r.ID, &r.SrcID, &r.DstID, &r.Type, &r.PropsJSON, &r.CreatedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, r)
	}
	return items, total, nil
}

// Get 按 ID 查关系。
func (d *TopologyRelationDAO) Get(id int64) (*TopologyRelation, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	row := conn.QueryRow("SELECT id, src_id, dst_id, type, props_json, created_at FROM topology_relations WHERE id = ?", id)
	var r TopologyRelation
	if err := row.Scan(&r.ID, &r.SrcID, &r.DstID, &r.Type, &r.PropsJSON, &r.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// Create 新增关系（(src,dst,type) 唯一）。
func (d *TopologyRelationDAO) Create(r *TopologyRelation) (int64, error) {
	conn := GetDB()
	if conn == nil {
		return 0, errors.New("mysql unavailable")
	}
	res, err := conn.Exec("INSERT INTO topology_relations (src_id, dst_id, type, props_json) VALUES (?, ?, ?, ?)",
		r.SrcID, r.DstID, r.Type, r.PropsJSON)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Update 更新关系 props。
func (d *TopologyRelationDAO) Update(id int64, r *TopologyRelation) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec("UPDATE topology_relations SET props_json=? WHERE id=?", r.PropsJSON, id)
	return err
}

// Delete 删除关系。
func (d *TopologyRelationDAO) Delete(id int64) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec("DELETE FROM topology_relations WHERE id=?", id)
	return err
}

// ---------- TopologyNodeTypeDAO ----------

// TopologyNodeTypeDAO 节点类型目录数据访问对象。
type TopologyNodeTypeDAO struct{}

// List 返回全部节点类型。
func (d *TopologyNodeTypeDAO) List() ([]TopologyNodeType, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query("SELECT name, display_name, display_name_en, builtin, tier, description FROM topology_node_types ORDER BY tier")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TopologyNodeType{}
	for rows.Next() {
		var nt TopologyNodeType
		var b int
		if err := rows.Scan(&nt.Name, &nt.DisplayName, &nt.DisplayNameEN, &b, &nt.Tier, &nt.Description); err != nil {
			return nil, err
		}
		nt.Builtin = b == 1
		items = append(items, nt)
	}
	return items, nil
}

// Get 按 name 查节点类型。
func (d *TopologyNodeTypeDAO) Get(name string) (*TopologyNodeType, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	row := conn.QueryRow("SELECT name, display_name, display_name_en, builtin, tier, description FROM topology_node_types WHERE name = ?", name)
	var nt TopologyNodeType
	var b int
	if err := row.Scan(&nt.Name, &nt.DisplayName, &nt.DisplayNameEN, &b, &nt.Tier, &nt.Description); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	nt.Builtin = b == 1
	return &nt, nil
}

// Create 新增节点类型。
func (d *TopologyNodeTypeDAO) Create(nt *TopologyNodeType) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec("INSERT INTO topology_node_types (name, display_name, display_name_en, builtin, tier, description) VALUES (?,?,?,?,?,?)",
		nt.Name, nt.DisplayName, nt.DisplayNameEN, boolInt(nt.Builtin), nt.Tier, nt.Description)
	return err
}

// Delete 删除节点类型（内置不可删由业务层拦截）。
func (d *TopologyNodeTypeDAO) Delete(name string) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec("DELETE FROM topology_node_types WHERE name=?", name)
	return err
}

// ---------- TopologyRelationTypeDAO ----------

// TopologyRelationTypeDAO 关系类型目录数据访问对象。
type TopologyRelationTypeDAO struct{}

// List 返回全部关系类型。
func (d *TopologyRelationTypeDAO) List() ([]TopologyRelationType, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query("SELECT name, display_name, display_name_en, builtin, propagates_failure, direction, semantics_tag, description FROM topology_relation_types ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TopologyRelationType{}
	for rows.Next() {
		var rt TopologyRelationType
		var b, pf int
		if err := rows.Scan(&rt.Name, &rt.DisplayName, &rt.DisplayNameEN, &b, &pf, &rt.Direction, &rt.SemanticsTag, &rt.Description); err != nil {
			return nil, err
		}
		rt.Builtin = b == 1
		rt.PropagatesFailure = pf == 1
		items = append(items, rt)
	}
	return items, nil
}

// Get 按 name 查关系类型。
func (d *TopologyRelationTypeDAO) Get(name string) (*TopologyRelationType, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	row := conn.QueryRow("SELECT name, display_name, display_name_en, builtin, propagates_failure, direction, semantics_tag, description FROM topology_relation_types WHERE name = ?", name)
	var rt TopologyRelationType
	var b, pf int
	if err := row.Scan(&rt.Name, &rt.DisplayName, &rt.DisplayNameEN, &b, &pf, &rt.Direction, &rt.SemanticsTag, &rt.Description); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	rt.Builtin = b == 1
	rt.PropagatesFailure = pf == 1
	return &rt, nil
}

// Create 新增关系类型。
func (d *TopologyRelationTypeDAO) Create(rt *TopologyRelationType) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec("INSERT INTO topology_relation_types (name, display_name, display_name_en, builtin, propagates_failure, direction, semantics_tag, description) VALUES (?,?,?,?,?,?,?,?)",
		rt.Name, rt.DisplayName, rt.DisplayNameEN, boolInt(rt.Builtin), boolInt(rt.PropagatesFailure), rt.Direction, rt.SemanticsTag, rt.Description)
	return err
}

// Delete 删除关系类型（内置不可删由业务层拦截）。
func (d *TopologyRelationTypeDAO) Delete(name string) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec("DELETE FROM topology_relation_types WHERE name=?", name)
	return err
}
