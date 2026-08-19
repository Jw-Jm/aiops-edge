package biz

import (
	"errors"
	"fmt"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

var ErrInvalidContext = errors.New("invalid_context")

// ResourceQuery is an untrusted resource locator. ClusterRef may be a canonical
// UUID or a tenant-local slug; Resolve always returns the canonical UUID.
type ResourceQuery struct {
	TenantID     string
	ClusterRef   string
	ResourceType string
	Namespace    string
	Name         string
}

// ResourceResolver converts a caller's resource locator into the strict shared
// ResourceRef contract without consulting legacy numeric IDs, names, or kube
// context.
type ResourceResolver struct {
	Clusters *store.ClusterDAO
}

func (resolver ResourceResolver) Resolve(query ResourceQuery) (contract.ResourceRef, error) {
	var zero contract.ResourceRef
	if strings.TrimSpace(query.TenantID) == "" || strings.TrimSpace(query.ClusterRef) == "" ||
		query.ClusterRef == "all" || isLegacyClusterRef(query.ClusterRef) ||
		strings.TrimSpace(query.ResourceType) == "" || strings.TrimSpace(query.Name) == "" {
		return zero, ErrInvalidContext
	}

	clusters := resolver.Clusters
	if clusters == nil {
		clusters = &store.ClusterDAO{}
	}
	cluster, err := clusters.ResolveRef(query.TenantID, query.ClusterRef)
	if err != nil {
		return zero, fmt.Errorf("resolve resource cluster: %w", err)
	}

	resource := contract.ResourceRef{
		TenantID:     query.TenantID,
		ClusterID:    cluster.ClusterID,
		ResourceType: query.ResourceType,
		Name:         query.Name,
	}
	if query.Namespace != "" {
		namespace := query.Namespace
		resource.Namespace = &namespace
	}
	namespace := "_"
	if resource.Namespace != nil {
		namespace = *resource.Namespace
	}
	resource.ResourceID = fmt.Sprintf("urn:aiops:%s:%s:%s:%s:%s", resource.TenantID, resource.ClusterID, resource.ResourceType, namespace, resource.Name)
	if err := resource.Validate(); err != nil {
		return zero, fmt.Errorf("invalid resolved resource: %w", err)
	}
	return resource, nil
}

func isLegacyClusterRef(ref string) bool {
	if ref == "default" {
		return true
	}
	for _, character := range ref {
		if character < '0' || character > '9' {
			return false
		}
	}
	return ref != ""
}
