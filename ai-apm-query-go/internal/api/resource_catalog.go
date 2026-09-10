package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	graphpkg "github.com/observability-platform/ai-apm-query-go/internal/graph"
)

type ResourceReadMeta struct {
	GeneratedAt  time.Time `json:"generated_at"`
	Partial      bool      `json:"partial"`
	Stale        bool      `json:"stale"`
	WarningCodes []string  `json:"warning_codes"`
}

type resourceCatalogItem struct {
	UID          string                 `json:"uid"`
	ClusterID    string                 `json:"cluster_id"`
	Type         string                 `json:"type"`
	Domain       string                 `json:"domain"`
	Name         string                 `json:"name"`
	Namespace    string                 `json:"namespace,omitempty"`
	Location     string                 `json:"location"`
	Health       string                 `json:"health"`
	Source       string                 `json:"source"`
	Resolution   string                 `json:"resolution,omitempty"`
	LastSeenAt   string                 `json:"last_seen_at,omitempty"`
	Capabilities []string               `json:"capabilities,omitempty"`
	Attributes   map[string]interface{} `json:"attributes,omitempty"`
}

type resourceDomainSummary struct {
	Domain     string         `json:"domain"`
	Count      int            `json:"count"`
	Health     map[string]int `json:"health"`
	Incomplete bool           `json:"incomplete"`
}

type resourceCursor struct {
	NameKey string `json:"name_key"`
	UID     string `json:"uid"`
}

var resourceDomains = []string{"compute", "network", "storage", "kubernetes", "application"}

var resourceTypesByDomain = map[string]map[string]struct{}{
	"compute":     {"physical_server": {}, "k8s_node": {}, "vm": {}, "vmi": {}},
	"network":     {"switch": {}, "switch_port": {}, "nic": {}, "network": {}, "nad": {}},
	"storage":     {"disk": {}, "storage_class": {}, "pv": {}, "pvc": {}},
	"kubernetes":  {"namespace": {}, "deployment": {}, "replicaset": {}, "statefulset": {}, "daemonset": {}, "job": {}, "cronjob": {}, "pod": {}, "container": {}, "k8s_service": {}, "ingress": {}, "endpoint_slice": {}},
	"application": {"business": {}, "application": {}, "service": {}, "middleware": {}},
}

var primaryTypesByGroup = map[string]map[string]struct{}{
	"containers": {"deployment": {}, "statefulset": {}, "daemonset": {}, "job": {}, "cronjob": {}, "pod": {}, "k8s_service": {}, "ingress": {}},
	"kubevirt":   {"vm": {}, "vmi": {}},
}

func (h *Handler) ResourceCatalog(w http.ResponseWriter, r *http.Request) {
	scope, err := h.resourceReadScope(r)
	if err != nil {
		respondAuthorizationError(w, err)
		return
	}
	if h.graphRepo == nil {
		respondResourceError(w, http.StatusServiceUnavailable, "RESOURCE_CATALOG_UNAVAILABLE")
		return
	}
	domain := strings.TrimSpace(r.URL.Query().Get("domain"))
	group := strings.TrimSpace(r.URL.Query().Get("group"))
	typeFilter := strings.TrimSpace(r.URL.Query().Get("type"))
	health := strings.TrimSpace(r.URL.Query().Get("health"))
	if domain != "" {
		if _, ok := resourceTypesByDomain[domain]; !ok {
			respondResourceError(w, http.StatusBadRequest, "INVALID_RESOURCE_DOMAIN")
			return
		}
	}
	if group != "" {
		if _, ok := primaryTypesByGroup[group]; !ok {
			respondResourceError(w, http.StatusBadRequest, "INVALID_RESOURCE_GROUP")
			return
		}
	}
	if group != "" && typeFilter != "" {
		if _, ok := primaryTypesByGroup[group][typeFilter]; !ok {
			respondResourceError(w, http.StatusBadRequest, "INVALID_RESOURCE_TYPE")
			return
		}
	}
	if typeFilter != "" && !resourceTypeAllowed(typeFilter, domain) {
		respondResourceError(w, http.StatusBadRequest, "INVALID_RESOURCE_TYPE")
		return
	}
	if health != "" && !validResourceHealth(health) {
		respondResourceError(w, http.StatusBadRequest, "INVALID_RESOURCE_HEALTH")
		return
	}
	limit, err := parseResourceLimit(r.URL.Query().Get("limit"))
	if err != nil {
		respondResourceError(w, http.StatusBadRequest, "INVALID_RESOURCE_LIMIT")
		return
	}
	cursor, err := decodeResourceCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		respondResourceError(w, http.StatusBadRequest, "INVALID_RESOURCE_CURSOR")
		return
	}
	entities, err := h.graphRepo.SearchEntities(r.Context(), scope, graphpkg.EntitySearchQuery{
		EntityType: typeFilter,
		Name:       strings.TrimSpace(r.URL.Query().Get("q")),
		Limit:      graphpkg.DefaultPublicMaxVertices,
	})
	if err != nil {
		respondResourceGraphError(w, err)
		return
	}
	items := make([]resourceCatalogItem, 0, len(entities))
	for _, entity := range entities {
		if !resourceEntityAllowed(entity, domain, group, typeFilter, health) || !afterResourceCursor(entity, cursor) {
			continue
		}
		items = append(items, projectResourceCatalogItem(entity))
	}
	sort.Slice(items, func(i, j int) bool {
		left, right := healthPriority(items[i].Health), healthPriority(items[j].Health)
		if left != right {
			return left < right
		}
		if items[i].Name != items[j].Name {
			return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
		}
		return items[i].UID < items[j].UID
	})
	partial := len(entities) == graphpkg.DefaultPublicMaxVertices
	total := len(items)
	if len(items) > limit {
		items = items[:limit]
	}
	nextCursor := ""
	if len(items) == limit {
		last := entitiesForCursor(items[len(items)-1], entities)
		nextCursor = encodeResourceCursor(resourceCursor{NameKey: last.NameKey, UID: last.EntityUID})
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"items": items, "next_cursor": nextCursor, "total": total,
		"meta": resourceReadMeta(partial, partialWarning(partial)),
	})
}

func (h *Handler) ResourceSummary(w http.ResponseWriter, r *http.Request) {
	scope, err := h.resourceReadScope(r)
	if err != nil {
		respondAuthorizationError(w, err)
		return
	}
	if h.graphRepo == nil {
		respondResourceError(w, http.StatusServiceUnavailable, "RESOURCE_CATALOG_UNAVAILABLE")
		return
	}
	entities, err := h.graphRepo.SearchEntities(r.Context(), scope, graphpkg.EntitySearchQuery{Limit: graphpkg.DefaultPublicMaxVertices})
	if err != nil {
		respondResourceGraphError(w, err)
		return
	}
	summaries := make([]resourceDomainSummary, 0, len(resourceDomains))
	for _, domain := range resourceDomains {
		summary := resourceDomainSummary{Domain: domain, Health: map[string]int{}}
		for _, entity := range entities {
			if resourceDomain(entity.EntityType) != domain {
				continue
			}
			summary.Count++
			summary.Health[normalizeResourceHealth(entity.Health, entity.Status)]++
		}
		summaries = append(summaries, summary)
	}
	partial := len(entities) == graphpkg.DefaultPublicMaxVertices
	for i := range summaries {
		summaries[i].Incomplete = partial
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"domains": summaries,
		"meta":    resourceReadMeta(partial, partialWarning(partial)),
	})
}

func (h *Handler) ResourceDetail(w http.ResponseWriter, r *http.Request) {
	scope, err := h.resourceReadScope(r)
	if err != nil {
		respondAuthorizationError(w, err)
		return
	}
	uid := strings.TrimSpace(r.URL.Query().Get("uid"))
	if uid == "" {
		respondResourceError(w, http.StatusBadRequest, "RESOURCE_UID_REQUIRED")
		return
	}
	if h.graphRepo == nil {
		respondResourceError(w, http.StatusServiceUnavailable, "RESOURCE_CATALOG_UNAVAILABLE")
		return
	}
	entity, err := h.graphRepo.GetEntity(r.Context(), scope, uid)
	if err != nil {
		respondResourceGraphError(w, err)
		return
	}
	if resourceDomain(entity.EntityType) == "" {
		respondResourceError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND")
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"data": projectResourceCatalogItem(entity),
		"meta": resourceReadMeta(false, nil),
	})
}

func (h *Handler) resourceReadScope(r *http.Request) (graphpkg.GraphScope, error) {
	authorization, ok := requestAuthorizationContext(r)
	if !ok {
		var err error
		authorization, err = RequestAuthorizationContext(r)
		if err != nil {
			return graphpkg.GraphScope{}, err
		}
	}
	if authorization.TenantID == "" || authorization.ActiveClusterID == "" {
		return graphpkg.GraphScope{}, authorizationFailure("SCOPE_SELECTION_REQUIRED")
	}
	return graphpkg.GraphScope{TenantID: authorization.TenantID, ClusterIDs: map[string]struct{}{authorization.ActiveClusterID: {}}}, nil
}

func resourceDomain(entityType string) string {
	for domain, types := range resourceTypesByDomain {
		if _, ok := types[entityType]; ok {
			return domain
		}
	}
	return ""
}

func resourceTypeAllowed(entityType, domain string) bool {
	if domain != "" {
		_, ok := resourceTypesByDomain[domain][entityType]
		return ok
	}
	return resourceDomain(entityType) != ""
}

func resourceEntityAllowed(entity graphpkg.Entity, domain, group, typeFilter, health string) bool {
	if !resourceTypeAllowed(entity.EntityType, domain) {
		return false
	}
	if group != "" {
		if _, ok := primaryTypesByGroup[group][entity.EntityType]; !ok {
			return false
		}
	}
	if typeFilter != "" && entity.EntityType != typeFilter {
		return false
	}
	return health == "" || normalizeResourceHealth(entity.Health, entity.Status) == health
}

func projectResourceCatalogItem(entity graphpkg.Entity) resourceCatalogItem {
	item := resourceCatalogItem{
		UID: entity.EntityUID, ClusterID: entity.ClusterID, Type: entity.EntityType,
		Domain: resourceDomain(entity.EntityType), Name: entity.Name, Namespace: entity.Namespace,
		Health: normalizeResourceHealth(entity.Health, entity.Status), Source: entity.Source,
		Resolution: entity.Resolution, Location: entity.Name, Capabilities: resourceCapabilities(entity.Attrs),
		Attributes: resourceDetailAttributes(entity),
	}
	if item.Namespace != "" {
		item.Location = item.Namespace + " / " + item.Name
	}
	if entity.LastSeenMS > 0 {
		item.LastSeenAt = time.UnixMilli(entity.LastSeenMS).UTC().Format(time.RFC3339)
	}
	return item
}

// resourceDetailAttributes is deliberately allow-listed. Detail projections
// may expose operational identity/capacity fields, but never provider URLs,
// credentials, raw inventory payloads, or internal connection data.
func resourceDetailAttributes(entity graphpkg.Entity) map[string]interface{} {
	allowed := map[string]struct{}{}
	switch entity.EntityType {
	case "physical_server":
		allowed = map[string]struct{}{"vendor": {}, "model": {}, "product_name": {}, "serial_number": {}, "bmc_identifier": {}, "component_health": {}}
	case "k8s_node":
		allowed = map[string]struct{}{"role": {}, "version": {}, "ready": {}, "taints": {}, "capacity": {}, "host": {}}
	case "vm", "vmi":
		allowed = map[string]struct{}{"namespace": {}, "node": {}, "cpu": {}, "memory": {}, "disk": {}, "network": {}, "migration": {}, "vm_dependencies": {}}
	default:
		return nil
	}
	if len(entity.Attrs) == 0 {
		return nil
	}
	result := make(map[string]interface{})
	for key, value := range entity.Attrs {
		if _, ok := allowed[key]; ok {
			result[key] = value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func resourceCapabilities(attrs map[string]interface{}) []string {
	if attrs == nil {
		return nil
	}
	switch values := attrs["capabilities"].(type) {
	case []string:
		return append([]string(nil), values...)
	case []interface{}:
		result := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok && text != "" {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func normalizeResourceHealth(health, status string) string {
	switch strings.ToLower(strings.TrimSpace(health)) {
	case "critical", "fatal", "down":
		return "critical"
	case "degraded", "warning", "unhealthy":
		return "degraded"
	case "risk", "unknown-risk":
		return "risk"
	case "healthy", "ok", "ready", "active":
		return "healthy"
	}
	if strings.EqualFold(strings.TrimSpace(status), "active") {
		return "unknown"
	}
	return "unknown"
}

func healthPriority(health string) int {
	switch health {
	case "critical":
		return 0
	case "degraded":
		return 1
	case "risk":
		return 2
	case "unknown":
		return 3
	default:
		return 4
	}
}

func validResourceHealth(value string) bool {
	switch value {
	case "critical", "degraded", "risk", "healthy", "unknown":
		return true
	default:
		return false
	}
}

func parseResourceLimit(raw string) (int, error) {
	if raw == "" {
		return 50, nil
	}
	var limit int
	if _, err := fmtSscanf(raw, &limit); err != nil || limit < 1 || limit > 100 {
		return 0, errors.New("invalid limit")
	}
	return limit, nil
}

func encodeResourceCursor(cursor resourceCursor) string {
	value, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(value)
}

func decodeResourceCursor(raw string) (*resourceCursor, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	value, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	var cursor resourceCursor
	if err := json.Unmarshal(value, &cursor); err != nil || cursor.UID == "" {
		return nil, errors.New("invalid cursor")
	}
	return &cursor, nil
}

func afterResourceCursor(entity graphpkg.Entity, cursor *resourceCursor) bool {
	if cursor == nil {
		return true
	}
	if entity.NameKey != cursor.NameKey {
		return strings.ToLower(entity.NameKey) > strings.ToLower(cursor.NameKey)
	}
	return entity.EntityUID > cursor.UID
}

func entitiesForCursor(item resourceCatalogItem, entities []graphpkg.Entity) graphpkg.Entity {
	for _, entity := range entities {
		if entity.EntityUID == item.UID {
			return entity
		}
	}
	return graphpkg.Entity{EntityUID: item.UID, NameKey: item.Name}
}

func partialWarning(partial bool) []string {
	if partial {
		return []string{"RESOURCE_GRAPH_LIMIT"}
	}
	return nil
}

func resourceReadMeta(partial bool, warnings []string) ResourceReadMeta {
	return ResourceReadMeta{GeneratedAt: time.Now().UTC(), Partial: partial, Stale: false, WarningCodes: warnings}
}

func respondResourceGraphError(w http.ResponseWriter, err error) {
	var graphErr *graphpkg.Error
	if errors.As(err, &graphErr) {
		switch graphErr.Code {
		case graphpkg.ErrGraphScopeViolation:
			respondResourceError(w, http.StatusForbidden, "PERMISSION_DENIED")
		case graphpkg.ErrGraphEntityNotFound, graphpkg.ErrGraphEmpty:
			respondResourceError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND")
		default:
			respondResourceError(w, http.StatusServiceUnavailable, "RESOURCE_CATALOG_UNAVAILABLE")
		}
		return
	}
	respondResourceError(w, http.StatusServiceUnavailable, "RESOURCE_CATALOG_UNAVAILABLE")
}

// fmtSscanf keeps limit parsing local to this read-only boundary without making
// the catalog handler depend on the broader query parser package.
func fmtSscanf(value string, target *int) (int, error) {
	if value == "" {
		return 0, errors.New("empty value")
	}
	var parsed int
	for _, runeValue := range value {
		if runeValue < '0' || runeValue > '9' {
			return 0, errors.New("not an integer")
		}
		parsed = parsed*10 + int(runeValue-'0')
		if parsed > 1000 {
			return 0, errors.New("integer too large")
		}
	}
	*target = parsed
	return 1, nil
}
