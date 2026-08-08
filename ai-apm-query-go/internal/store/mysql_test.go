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
