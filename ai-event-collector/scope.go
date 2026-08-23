package main

import (
	"errors"
	"regexp"
)

// canonicalUUID 对齐 Phase 3/Phase 4 冻结语义（telemetrylabels 同 pattern）。
// 拒绝空 / default / slug / 数值。
var canonicalUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

var (
	ErrInvalidTenantID  = errors.New("tenant_id must be canonical uuid")
	ErrInvalidClusterID = errors.New("cluster_id must be canonical uuid")
)

// EventScope 是事件写入的 tenant/cluster 身份。两者都必须是 canonical UUID。
type EventScope struct {
	TenantID  string
	ClusterID string
}

func validateCanonicalUUID(s string) bool {
	return canonicalUUID.MatchString(s)
}

func (s EventScope) Validate() error {
	if !validateCanonicalUUID(s.TenantID) {
		return ErrInvalidTenantID
	}
	if !validateCanonicalUUID(s.ClusterID) {
		return ErrInvalidClusterID
	}
	return nil
}
