package store

import "testing"

// TestUserDAO 验证 UserDAO 基本路径。MySQL 不可达时 GetDB() 返回 nil，测试安全跳过。
func TestUserDAO(t *testing.T) {
	db := GetDB()
	if db == nil {
		t.Skip("MySQL not available")
	}
	_ = (&UserDAO{}).SeedAdmin("x")
	u, err := (&UserDAO{}).GetByUsername("admin")
	if err != nil {
		t.Fatalf("GetByUsername err: %v", err)
	}
	if u == nil {
		t.Skip("admin user not seeded")
	}
	if u.Username != "admin" {
		t.Fatalf("got %s, want admin", u.Username)
	}
}

// TestEnsureSchemaCreatesServiceMetadata 验证 EnsureSchema 执行后
// service_metadata 与 anomaly_events 两张新表存在。
// MySQL 不可达时 GetDB() 返回 nil，测试安全跳过（与现有 store 测试约定一致）。
func TestEnsureSchemaCreatesServiceMetadata(t *testing.T) {
	db := GetDB()
	if db == nil {
		t.Skip("MySQL not available")
	}
	EnsureSchema()

	tables := []string{"service_metadata", "anomaly_events"}
	for _, tbl := range tables {
		var count int
		err := db.QueryRow(
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=?",
			tbl,
		).Scan(&count)
		if err != nil {
			t.Fatalf("query table %s existence err: %v", tbl, err)
		}
		if count == 0 {
			t.Errorf("table %s not created by EnsureSchema", tbl)
		}
	}
}
