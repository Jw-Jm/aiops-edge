package query

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// KubernetesEvent is the bounded, query-owned view of the unified event
// fact table.  The event collector is the only writer; query-api is the only
// reader exposed to RCA and never falls back to the retired orchestrator
// IPMI/Kubernetes endpoints.
type KubernetesEvent struct {
	TenantID        string `json:"tenant_id"`
	ClusterID       string `json:"cluster_id"`
	Timestamp       string `json:"timestamp"`
	Namespace       string `json:"namespace"`
	Kind            string `json:"kind"`
	Name            string `json:"name"`
	Reason          string `json:"reason"`
	Type            string `json:"type"`
	Message         string `json:"message"`
	InvolvedObject  string `json:"involved_object"`
	SourceComponent string `json:"source_component"`
	Source          string `json:"source"`
	Node            string `json:"node"`
	EventID         string `json:"event_id"`
}

// KubernetesEventRepository owns SQL for observability.k8s_events.  The
// investigation form requires an absolute frozen window and a canonical
// tenant/cluster scope; no relative "last N minutes" or default scope is
// accepted here.
type KubernetesEventRepository struct {
	ch *ClickHouseRepo
}

func NewKubernetesEventRepository(ch *ClickHouseRepo) *KubernetesEventRepository {
	return &KubernetesEventRepository{ch: ch}
}

func (r *KubernetesEventRepository) List(ctx context.Context, tenantID, clusterID string,
	services []string, start, end *time.Time, limit, offset int) ([]KubernetesEvent, error) {
	if r == nil || r.ch == nil {
		return nil, Unavailable("kubernetes events: repository not configured")
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(clusterID) == "" {
		return nil, PermissionDenied("kubernetes events: tenant and cluster scope are required")
	}
	if start == nil || end == nil || start.After(*end) {
		return nil, ValidationFailed("kubernetes events: frozen time window is required")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	conditions := []string{
		"tenant_id=" + sqlStr(tenantID),
		"cluster_id=" + sqlStr(clusterID),
		fmt.Sprintf("ts >= %s", chTimeLiteral(*start)),
		fmt.Sprintf("ts < %s", chTimeLiteral(*end)),
		"type IN ('Warning','Error')",
	}
	if len(services) > 0 {
		matches := make([]string, 0, len(services)*4)
		for _, service := range services {
			service = strings.TrimSpace(service)
			if service == "" {
				continue
			}
			// event-collector stores the target as Kind/name.  Keep exact
			// comparisons first and use a suffix match only for the target
			// name; all literals are escaped by the repository helper.
			matches = append(matches,
				"name="+sqlStr(service),
				"involved_object="+sqlStr(service),
				"involved_object LIKE "+chLike("/"+service),
			)
		}
		if len(matches) > 0 {
			conditions = append(conditions, "("+strings.Join(matches, " OR ")+")")
		}
	}
	sql := "SELECT tenant_id, cluster_id, toString(ts) AS timestamp, namespace, kind, name, reason, type, message, involved_object, source_component, source, node, event_id " +
		"FROM observability.k8s_events FINAL WHERE " + strings.Join(conditions, " AND ") +
		fmt.Sprintf(" ORDER BY ts DESC, event_id DESC LIMIT %d OFFSET %d", limit, offset)
	rows, err := r.ch.QueryJSON(ctx, sql)
	if err != nil {
		return nil, err
	}
	out := make([]KubernetesEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, KubernetesEvent{
			TenantID:        str(row, "tenant_id"),
			ClusterID:       str(row, "cluster_id"),
			Timestamp:       str(row, "timestamp"),
			Namespace:       str(row, "namespace"),
			Kind:            str(row, "kind"),
			Name:            str(row, "name"),
			Reason:          str(row, "reason"),
			Type:            str(row, "type"),
			Message:         str(row, "message"),
			InvolvedObject:  str(row, "involved_object"),
			SourceComponent: str(row, "source_component"),
			Source:          str(row, "source"),
			Node:            str(row, "node"),
			EventID:         str(row, "event_id"),
		})
	}
	return out, nil
}
