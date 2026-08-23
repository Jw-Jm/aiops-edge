// Package telemetrylabels 提供 VictoriaMetrics / VictoriaLogs 写入端的 scope label
// 校验与归一化（V9.2 Phase 4 P4.6）。它是独立包，VictoriaMetrics writer、
// VictoriaLogs writer 与派生 ClickHouse writer 按各自需要复用；不绑定任一存储实现。
//
// canonical UUID 校验对齐 Phase 3 冻结语义（与 query-go internal/k8sboundary 的
// canonicalUUID 同一 pattern）——租户/集群/resource 身份必须是 canonical UUID，
// 不允许空字符串 / default / slug / 数值。
package telemetrylabels

import (
	"errors"
	"regexp"
)

var (
	// ErrInvalidTenantID 表示 tenant_id 不是 canonical UUID。
	ErrInvalidTenantID = errors.New("tenant_id must be canonical uuid")
	// ErrInvalidClusterID 表示 cluster_id 不是 canonical UUID。
	ErrInvalidClusterID = errors.New("cluster_id must be canonical uuid")
	// ErrInvalidResourceID 表示 resource_id 不是 canonical UUID。
	ErrInvalidResourceID = errors.New("resource_id must be canonical uuid for resource scope")
)

// canonicalUUID 对齐 Phase 3 冻结 pattern（V9.2 §...，见 query-go k8sboundary）。
var canonicalUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// Scope 描述 telemetry 的数据粒度。
const (
	ScopeResource  = "resource"  // 实例级：resource_id REQUIRED
	ScopeCluster   = "cluster"   // 集群级：resource_id 可选
	ScopeAggregate = "aggregate" // 聚合级：resource_id 可选
)

// ValidateScopeLabels 校验 scope labels：
//   - tenant_id 必为 canonical UUID；
//   - cluster_id 必为 canonical UUID（拒绝空 / default / slug / 数值）；
//   - scope=resource 时 resource_id 必为 canonical UUID。
func ValidateScopeLabels(labels map[string]string, scope string) error {
	if !canonicalUUID.MatchString(labels["tenant_id"]) {
		return ErrInvalidTenantID
	}
	if !canonicalUUID.MatchString(labels["cluster_id"]) {
		return ErrInvalidClusterID
	}
	if scope == ScopeResource && !canonicalUUID.MatchString(labels["resource_id"]) {
		return ErrInvalidResourceID
	}
	return nil
}

// NormalizeScopeLabels 归一化 labels：剔除空值 label，保留非空项。
// 返回新 map，不改动入参。
func NormalizeScopeLabels(labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		if v != "" {
			out[k] = v
		}
	}
	return out
}
