package store

import "testing"

// TestSeedTopologyTypes 验证 node_types / relation_types 内置种子数据可加载。
func TestSeedTopologyTypes(t *testing.T) {
	db := GetDB()
	if db == nil {
		t.Skip("MySQL not available")
	}
	if err := SeedTopologyTypes(); err != nil {
		t.Fatalf("SeedTopologyTypes err: %v", err)
	}
	nts, err := (&TopologyNodeTypeDAO{}).List()
	if err != nil {
		t.Fatalf("node types list err: %v", err)
	}
	if len(nts) == 0 {
		t.Fatal("no node types seeded")
	}
	// 内置 5 种应存在
	builtin := map[string]bool{}
	for _, n := range nts {
		if n.Builtin {
			builtin[n.Name] = true
		}
	}
	for _, want := range []string{"app", "service", "cluster", "device", "rack"} {
		if !builtin[want] {
			t.Fatalf("builtin node type %s missing", want)
		}
	}
	rts, err := (&TopologyRelationTypeDAO{}).List()
	if err != nil {
		t.Fatalf("relation types list err: %v", err)
	}
	if len(rts) == 0 {
		t.Fatal("no relation types seeded")
	}
}

// TestTopologyNodeDAO 验证 node CRUD 基本路径。
func TestTopologyNodeDAO(t *testing.T) {
	db := GetDB()
	if db == nil {
		t.Skip("MySQL not available")
	}
	d := &TopologyNodeDAO{}
	id, err := d.Create(&TopologyNode{Type: "service", Name: "test-topo-svc", PropsJSON: `{"team":"ops"}`})
	if err != nil {
		t.Fatalf("create err: %v", err)
	}
	if id <= 0 {
		t.Fatal("create id <= 0")
	}
	defer d.Delete(id)

	got, err := d.Get(id)
	if err != nil {
		t.Fatalf("get err: %v", err)
	}
	if got == nil || got.Name != "test-topo-svc" {
		t.Fatalf("got %+v", got)
	}
	if got.PropsJSON != `{"team":"ops"}` {
		t.Fatalf("props got %s", got.PropsJSON)
	}
}

// TestTopologyRelationDAO 验证 relation CRUD + 唯一约束 (src,dst,type)。
func TestTopologyRelationDAO(t *testing.T) {
	db := GetDB()
	if db == nil {
		t.Skip("MySQL not available")
	}
	nd := &TopologyNodeDAO{}
	src, _ := nd.Create(&TopologyNode{Type: "service", Name: "rel-src"})
	dst, _ := nd.Create(&TopologyNode{Type: "service", Name: "rel-dst"})
	defer nd.Delete(src)
	defer nd.Delete(dst)

	d := &TopologyRelationDAO{}
	rid, err := d.Create(&TopologyRelation{SrcID: src, DstID: dst, Type: "depends_on", PropsJSON: ""})
	if err != nil {
		t.Fatalf("create rel err: %v", err)
	}
	defer d.Delete(rid)
	if rid <= 0 {
		t.Fatal("rel id <= 0")
	}
	// 重复创建相同 (src,dst,type) 应失败（唯一约束）
	if _, err := d.Create(&TopologyRelation{SrcID: src, DstID: dst, Type: "depends_on", PropsJSON: ""}); err == nil {
		t.Fatal("expected duplicate relation to fail")
	}
}
