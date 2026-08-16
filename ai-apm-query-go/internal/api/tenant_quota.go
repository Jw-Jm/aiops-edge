package api

import (
	"strings"
	"sync"
	"time"
)

// ── QuotaAI 当日 AI 调用配额（P3-2b）──
// 演示级实现：进程内内存计数（重启归零），生产可替换为 Redis/MySQL 计数表
// （如 Redis INCR + EXPIRE 24h，或 MySQL 按 date+tenant 唯一键累加）。
var (
	quotaUsage   = map[string]int{} // key: "2006-01-02|tenant" → 当日 AI 调用次数
	quotaUsageMu sync.Mutex
)

// quotaKey 当日配额计数的 key：日期 + 租户。
func quotaKey(now time.Time, tenant string) string {
	return now.Format("2006-01-02") + "|" + tenant
}

// tenantQuotaAI 返回租户的 AI 调用配额（QuotaAI，0=不限）。
// 租户不存在（默认 default 恒在）按不限处理。
func tenantQuotaAI(tenant string) int {
	tenantsMu.RLock()
	defer tenantsMu.RUnlock()
	if t, ok := tenants[tenant]; ok {
		return t.QuotaAI
	}
	return 0
}

// quotaUsedToday 返回租户当日已用的 AI 调用数。
func quotaUsedToday(tenant string) int {
	quotaUsageMu.Lock()
	defer quotaUsageMu.Unlock()
	return quotaUsage[quotaKey(time.Now(), tenant)]
}

// quotaIncrementToday 当日 AI 调用计数 +1，返回递增后的使用数（用于转发前计数）。
func quotaIncrementToday(tenant string) int {
	quotaUsageMu.Lock()
	defer quotaUsageMu.Unlock()
	now := time.Now()
	key := quotaKey(now, tenant)
	// 防御性清理：map 过大时清掉非当日 key（进程内计数，避免跨天无限增长）。
	if len(quotaUsage) > 10000 {
		prefix := now.Format("2006-01-02") + "|"
		for k := range quotaUsage {
			if !strings.HasPrefix(k, prefix) {
				delete(quotaUsage, k)
			}
		}
	}
	quotaUsage[key]++
	return quotaUsage[key]
}

// isLLMProxyPath 判断被代理路径是否属于 LLM 调用（消耗 QuotaAI 配额）。
// 仅对 /ai/chat、/ai/nl2sql、/ai/final_report 这类真正调用 LLM 的路径计数；
// sessions/skills/agents/mcp 等工具类接口不消耗配额，避免误限流。
func isLLMProxyPath(path string) bool {
	for _, p := range []string{
		"/api/v1/ai/chat",
		"/api/v1/ai/nl2sql",
		"/api/v1/ai/final_report",
	} {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}
