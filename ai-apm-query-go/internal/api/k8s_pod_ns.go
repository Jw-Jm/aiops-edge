package api

import (
	"regexp"
	"strings"
	"sync"
	"time"
)

// podNSFallbackTTL 兜底映射缓存时长：避免每个拓扑请求都打 K8s API。
const podNSFallbackTTL = 60 * time.Second

// 常见 pod 名随机后缀（K8s 生成）：
//   - ReplicaSet（Deployment 生成）：<base>-<hash10>-<hash5>（如 query-api-5fcc6d754f-zhzwp）
//   - StatefulSet：<base>-<ordinal>（如 clickhouse-0）
//   - DaemonSet：<base>-<hash5>（如 deepflow-agent-qkv8w）
//
// ReplicaSet 第一段要求 9~10 位 hash，避免把 DaemonSet 的 <base>-<word5>-<hash5>
// （如 deepflow-agent-qkv8w 中 "agent-qkv8w"）误判为 ReplicaSet 后缀。
var (
	rsSuffixRe  = regexp.MustCompile(`-[a-z0-9]{9,10}-[a-z0-9]{5}$`)
	ordSuffixRe = regexp.MustCompile(`-\d+$`)
	dsSuffixRe  = regexp.MustCompile(`-[a-z0-9]{5}$`)
)

// podNSResolver 把 K8s pod 名 → namespace 映射为 服务名 → namespace（进程内缓存）。
// 用途：GlobalTopology 对 k8s_namespace 为空的服务（如 deepflow 同步服务、存量 span）
// 做 K8s pod 兜底映射，使服务全景可按真实 ns（observability/deepflow）过滤。
type podNSResolver struct {
	mu      sync.Mutex
	svcNS   map[string]string // 基础名 → namespace
	fetched time.Time
	ttl     time.Duration
	fetchFn func() (map[string]string, error) // 可注入测试
}

// newPodNSResolver 创建带默认 K8s 拉取实现的解析器。
func newPodNSResolver() *podNSResolver {
	return &podNSResolver{
		svcNS:   map[string]string{},
		ttl:     podNSFallbackTTL,
		fetchFn: fetchPodNSMap,
	}
}

// resolve 返回服务名对应的 K8s namespace；无匹配或服务名含 "(deleted)" 时返回空串。
// 仅用于 ns 为空时的兜底，不覆盖已有 span ns。
func (r *podNSResolver) resolve(service string) string {
	if service == "" || strings.Contains(service, "(deleted)") {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Since(r.fetched) > r.ttl {
		if m, err := r.fetchFn(); err == nil {
			r.svcNS = m
		}
		r.fetched = time.Now() // 失败也刷新计时，避免每请求重试
	}
	// 精确匹配基础名
	if ns, ok := r.svcNS[service]; ok {
		return ns
	}
	// 兜底：服务名是某基础名的前缀（如服务 "query-api" ← 基础名 "query-api-5fcc6"，
	// 覆盖 5 位 RS hash 等未完全剥掉后缀的情况）。取最短匹配保证确定性。
	best := ""
	bestLen := int(^uint(0) >> 1)
	for base, ns := range r.svcNS {
		if strings.HasPrefix(base, service) && len(base) < bestLen {
			best = ns
			bestLen = len(base)
		}
	}
	return best
}

// stripPodSuffix 去掉 pod 名随机后缀，得到基础名（服务/deployment 名）。
// 处理 ReplicaSet / StatefulSet / DaemonSet 三种后缀形态。
func stripPodSuffix(name string) string {
	if name == "" {
		return ""
	}
	if idx := rsSuffixRe.FindStringIndex(name); idx != nil {
		return name[:idx[0]]
	}
	if idx := ordSuffixRe.FindStringIndex(name); idx != nil {
		return name[:idx[0]]
	}
	if idx := dsSuffixRe.FindStringIndex(name); idx != nil {
		return name[:idx[0]]
	}
	return name
}

// fetchPodNSMap 从 K8s 拉取全部 pod（跨 namespace）并构建 基础名 → namespace 映射。
// 复用 k8sAPIFn（in-cluster API，失败回退 kubectl --all-namespaces）。
func fetchPodNSMap() (map[string]string, error) {
	data, err := k8sAPIFn("/api/v1/pods")
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, p := range parsePods(data) {
		name, _ := p["name"].(string)
		ns, _ := p["namespace"].(string)
		if name == "" || ns == "" {
			continue
		}
		base := stripPodSuffix(name)
		if base == "" {
			continue
		}
		if _, ok := m[base]; !ok {
			m[base] = ns
		}
	}
	return m, nil
}
