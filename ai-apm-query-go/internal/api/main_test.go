package api

import (
	"os"
	"testing"
)

// TestMain 在运行本包所有测试前，注入默认的 JWT_SECRET / LLM_ENCRYPTION_KEY
// （仅当外部未显式设置时）。生产环境中这些密钥由 Secret 注入；此处仅为让
// `go test ./...` 开箱即用（auth.go / settings.go 在缺失密钥时会 panic 兜底）。
func TestMain(m *testing.M) {
	if os.Getenv("JWT_SECRET") == "" {
		os.Setenv("JWT_SECRET", "test-jwt-secret-0123456789abcdef0123456789")
	}
	if os.Getenv("LLM_ENCRYPTION_KEY") == "" {
		os.Setenv("LLM_ENCRYPTION_KEY", "test-llm-key-0123456789abcdef0123456789abcd")
	}
	os.Exit(m.Run())
}
