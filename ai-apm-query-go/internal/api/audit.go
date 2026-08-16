package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// auditWrite 记录一次写操作审计到 MySQL aiops.audit_logs 表。
//
// 与 orchestrator 侧 AuditStore.log 语义对齐（P0-8 统一审计）：
//   - operator 从请求 JWT 的 sub 提取；无/无效 JWT 时回退 "system"
//   - target_service 存资源目标（服务名/资源名/ID）
//   - command 存 HTTP 方法与路径（如 "POST /api/v1/users"）
//   - result 固定 "success"（仅应在写操作成功路径调用）
//   - task_id 生成短 id（进程内唯一即可，语义同 orchestrator 的 _audit_log）
//   - detail 按 detail JSON 列适配：合法 JSON 原样写入，否则 JSON 编码为字符串
//
// 审计失败仅 log，绝不阻断主流程（调用方无需处理返回值）。
func auditWrite(r *http.Request, action, target, detail string) {
	db := store.GetDB()
	if db == nil {
		log.Printf("AUDIT: skipped (mysql unavailable): action=%s target=%s", action, target)
		return
	}

	operator := ""
	if token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); token != "" {
		if username, _, _, ok := validateJWT(token); ok {
			operator = username
		}
	}
	if operator == "" {
		operator = "system"
	}

	// detail 为 JSON 列：合法 JSON 原样写入，否则 JSON 编码为字符串，保证不违反列约束。
	detailVal := "null"
	if detail != "" {
		if json.Valid([]byte(detail)) {
			detailVal = detail
		} else if b, err := json.Marshal(detail); err == nil {
			detailVal = string(b)
		}
	}

	command := r.Method + " " + r.URL.Path
	_, err := db.Exec(
		"INSERT INTO audit_logs (task_id, action, operator, target_service, command, result, detail) VALUES (?, ?, ?, ?, ?, ?, ?)",
		generateID(), action, operator, target, command, "success", detailVal,
	)
	if err != nil {
		log.Printf("AUDIT: write failed: action=%s target=%s err=%v", action, target, err)
	}
}
