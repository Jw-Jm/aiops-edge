package api

import (
	"errors"
	"testing"
	"time"
)

// TestStripPodSuffix 验证 pod 名去随机后缀：
// ReplicaSet 双 hash（query-api-5fcc6d754f-zhzwp）、StatefulSet 序号（clickhouse-0）、
// DaemonSet 单 hash（deepflow-agent-qkv8w）。
func TestStripPodSuffix(t *testing.T) {
	cases := []struct{ pod, want string }{
		{"query-api-5fcc6d754f-zhzwp", "query-api"},
		{"ingest-b699c6449-rr65r", "ingest"},
		{"frontend-6596b875fd-w59xz", "frontend"},
		{"ai-orchestrator-85679595b7-bqqnh", "ai-orchestrator"},
		{"victoria-metrics-7dbf779c4c-8plr2", "victoria-metrics"},
		{"clickhouse-0", "clickhouse"},
		{"mysql-0", "mysql"},
		{"deepflow-agent-qkv8w", "deepflow-agent"},
		{"deepflow-clickhouse-0", "deepflow-clickhouse"},
		{"no-suffix", "no-suffix"},
		{"", ""},
	}
	for _, c := range cases {
		if got := stripPodSuffix(c.pod); got != c.want {
			t.Errorf("stripPodSuffix(%q) = %q, want %q", c.pod, got, c.want)
		}
	}
}

// TestPodNSResolverResolve 验证 K8s pod 兜底映射：注入 mock fetch，
// 服务名精确匹配基础名；deleted 服务 / 未匹配服务返回空。
func TestPodNSResolverResolve(t *testing.T) {
	r := newPodNSResolver()
	r.ttl = 100 * time.Millisecond
	r.fetchFn = func() (map[string]string, error) {
		return map[string]string{
			"query-api":        "observability",
			"victoria-logs":    "observability",
			"deepflow-server":  "deepflow",
			"deepflow-agent":   "deepflow",
			"clickhouse":       "observability",
		}, nil
	}

	cases := []struct{ svc, want string }{
		{"query-api", "observability"},
		{"victoria-logs", "observability"},
		{"deepflow-server", "deepflow"},
		{"clickhouse", "observability"},
		{"unknown-service", ""},
		{"query-api (deleted)", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := r.resolve(c.svc); got != c.want {
			t.Errorf("resolve(%q) = %q, want %q", c.svc, got, c.want)
		}
	}
}

// TestPodNSResolverCacheTTL 验证缓存：TTL 内不重复 fetch（命中缓存），过期后重新拉取。
func TestPodNSResolverCacheTTL(t *testing.T) {
	calls := 0
	r := newPodNSResolver()
	r.ttl = 50 * time.Millisecond
	r.fetchFn = func() (map[string]string, error) {
		calls++
		return map[string]string{"query-api": "observability"}, nil
	}
	r.resolve("query-api") // 第一次 fetch
	r.resolve("query-api") // 命中缓存，不再 fetch
	if calls != 1 {
		t.Fatalf("expected 1 fetch within TTL, got %d", calls)
	}
	time.Sleep(60 * time.Millisecond)
	r.resolve("query-api") // TTL 过期，重新 fetch
	if calls != 2 {
		t.Fatalf("expected 2 fetches after TTL expiry, got %d", calls)
	}
}

// TestPodNSResolverFetchError 验证拉取失败时不 panic，保持上次缓存/空映射。
func TestPodNSResolverFetchError(t *testing.T) {
	r := newPodNSResolver()
	r.ttl = 10 * time.Millisecond
	r.fetchFn = func() (map[string]string, error) {
		return nil, errors.New("k8s unreachable")
	}
	if got := r.resolve("query-api"); got != "" {
		t.Errorf("resolve on fetch error = %q, want empty", got)
	}
}
