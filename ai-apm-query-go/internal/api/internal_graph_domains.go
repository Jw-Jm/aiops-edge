package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/query"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

var graphDomainRoutes = map[string]struct {
	capability string
	entityType []string
}{
	"/internal/v1/query/kubevirt":           {"kubevirt.resources.read", []string{"vm", "vmi", "migration"}},
	"/internal/v1/query/hardware/inventory": {"hardware.inventory.read", []string{"physical_server", "cpu", "dimm", "nic", "disk", "mainboard", "bmc", "psu", "fan"}},
	"/internal/v1/query/hardware/health":    {"hardware.health.read", []string{"physical_server", "cpu", "dimm", "nic", "disk", "mainboard", "bmc", "psu", "fan"}},
	"/internal/v1/query/catalog":            {"catalog.read", []string{"business", "application", "service", "middleware"}},
	"/internal/v1/query/network-topology":   {"network.topology.read", []string{"nad", "network", "switch", "switch_port", "nic"}},
}

// InternalQueryGraphDomain exposes the typed, authoritative source views
// needed by builders.  It must not read the projected graph: doing so would
// make a missing vertex self-reinforcing and would prevent fresh-install
// discovery.  Every branch below stays inside an existing query-api boundary.
func (h *Handler) InternalQueryGraphDomain(w http.ResponseWriter, r *http.Request) {
	route, ok := graphDomainRoutes[r.URL.Path]
	if !ok {
		respondInternalQueryError(w, &internalQueryError{Code: "VALIDATION_FAILED", Message: "unsupported graph domain route"})
		return
	}
	rctx, req, err := decodeInternalRequest(r, route.capability)
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	entityType := strings.TrimSpace(req.EntityType)
	if entityType != "" && !containsString(route.entityType, entityType) {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "GRAPH_UNKNOWN_ENTITY_TYPE"})
		return
	}
	h.execToolQuery(w, rctx, req, func() ([]byte, error) {
		return h.graphSourceView(r.Context(), r.URL.Path, rctx, req)
	})
}

func (h *Handler) graphSourceView(ctx context.Context, path string, rctx *internalQueryCtx, req *internalQueryRequest) ([]byte, error) {
	scope := query.KubernetesScope{TenantID: rctx.TenantID, ClusterID: rctx.ClusterID}
	switch path {
	case "/internal/v1/query/kubevirt":
		if h.kubeRepo == nil {
			return nil, query.Unavailable("kubevirt: access boundary not configured")
		}
		data, err := h.kubeRepo.ListKubeVirtObjects(ctx, scope, rctx.ClusterID)
		if err != nil {
			return nil, err
		}
		return json.Marshal(data)
	case "/internal/v1/query/hardware/inventory":
		data, err := (&store.HardwareInventoryDAO{}).ListGraphHardware(rctx.TenantID, rctx.ClusterID)
		if err != nil {
			return nil, err
		}
		return json.Marshal(data)
	case "/internal/v1/query/hardware/health":
		// Health/SEL has no canonical inventory table in the current schema.
		// Return an explicit successful no-data view; do not infer healthy from
		// a missing collector or from projected graph state.
		return json.Marshal(map[string]interface{}{"items": []map[string]interface{}{}, "count": 0, "no_data": true})
	case "/internal/v1/query/catalog":
		data, err := (&store.BusinessCatalogDAO{}).ListGraphCatalog(rctx.TenantID, rctx.ClusterID)
		if err != nil {
			return nil, err
		}
		return json.Marshal(data)
	case "/internal/v1/query/network-topology":
		if h.kubeRepo == nil {
			return nil, query.Unavailable("network: access boundary not configured")
		}
		objects, err := h.kubeRepo.ListGraphObjects(ctx, scope, rctx.ClusterID)
		if err != nil {
			return nil, err
		}
		items := []map[string]interface{}{}
		for _, field := range []string{"nads", "networks"} {
			list, _ := objects[field].([]map[string]interface{})
			for _, object := range list {
				metadata, _ := object["metadata"].(map[string]interface{})
				uid := strings.TrimSpace(stringValue(metadata["uid"]))
				name := strings.TrimSpace(stringValue(metadata["name"]))
				if uid == "" {
					continue
				}
				typ := "network"
				if field == "nads" {
					typ = "nad"
				}
				items = append(items, map[string]interface{}{
					"entity_type": typ, "uid": uid, "name": name, "object": object,
				})
			}
		}
		response := map[string]interface{}{"items": items, "count": len(items)}
		if partial, _ := objects["partial"].(bool); partial {
			response["partial"] = true
			response["errors"] = objects["errors"]
		}
		return json.Marshal(response)
	default:
		return nil, query.Unavailable("unsupported graph source route")
	}
}

func stringValue(value interface{}) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func containsString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}
