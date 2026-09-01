package graph

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type SchemaResource struct {
	Kind    string                 `json:"-"`
	Name    string                 `json:"name"`
	Payload map[string]interface{} `json:"-"`
}

func SchemaResourcesV2() []SchemaResource {
	properties := []SchemaResource{
		{Kind: "propertykey", Name: "entity_uid", Payload: map[string]interface{}{"name": "entity_uid", "data_type": "TEXT", "cardinality": "SINGLE"}},
		{Kind: "propertykey", Name: "entity_type", Payload: map[string]interface{}{"name": "entity_type", "data_type": "TEXT", "cardinality": "SINGLE"}},
		{Kind: "propertykey", Name: "tenant_id", Payload: map[string]interface{}{"name": "tenant_id", "data_type": "TEXT", "cardinality": "SINGLE"}},
		{Kind: "propertykey", Name: "cluster_id", Payload: map[string]interface{}{"name": "cluster_id", "data_type": "TEXT", "cardinality": "SINGLE"}},
		{Kind: "propertykey", Name: "namespace", Payload: map[string]interface{}{"name": "namespace", "data_type": "TEXT", "cardinality": "SINGLE"}},
		{Kind: "propertykey", Name: "name", Payload: map[string]interface{}{"name": "name", "data_type": "TEXT", "cardinality": "SINGLE"}},
		{Kind: "propertykey", Name: "name_key", Payload: map[string]interface{}{"name": "name_key", "data_type": "TEXT", "cardinality": "SINGLE"}},
		{Kind: "propertykey", Name: "source", Payload: map[string]interface{}{"name": "source", "data_type": "TEXT", "cardinality": "SINGLE"}},
		{Kind: "propertykey", Name: "source_uid", Payload: map[string]interface{}{"name": "source_uid", "data_type": "TEXT", "cardinality": "SINGLE"}},
		{Kind: "propertykey", Name: "status", Payload: map[string]interface{}{"name": "status", "data_type": "TEXT", "cardinality": "SINGLE"}},
		{Kind: "propertykey", Name: "health", Payload: map[string]interface{}{"name": "health", "data_type": "TEXT", "cardinality": "SINGLE"}},
		{Kind: "propertykey", Name: "resolution", Payload: map[string]interface{}{"name": "resolution", "data_type": "TEXT", "cardinality": "SINGLE"}},
		{Kind: "propertykey", Name: "confidence", Payload: map[string]interface{}{"name": "confidence", "data_type": "DOUBLE", "cardinality": "SINGLE"}},
		{Kind: "propertykey", Name: "first_seen_ms", Payload: map[string]interface{}{"name": "first_seen_ms", "data_type": "LONG", "cardinality": "SINGLE"}},
		{Kind: "propertykey", Name: "last_seen_ms", Payload: map[string]interface{}{"name": "last_seen_ms", "data_type": "LONG", "cardinality": "SINGLE"}},
		{Kind: "propertykey", Name: "generation", Payload: map[string]interface{}{"name": "generation", "data_type": "LONG", "cardinality": "SINGLE"}},
		{Kind: "propertykey", Name: "attrs_version", Payload: map[string]interface{}{"name": "attrs_version", "data_type": "LONG", "cardinality": "SINGLE"}},
		{Kind: "propertykey", Name: "attrs_json", Payload: map[string]interface{}{"name": "attrs_json", "data_type": "TEXT", "cardinality": "SINGLE"}},
	}
	entityProperties := make([]string, 0, 18)
	for _, item := range properties {
		entityProperties = append(entityProperties, item.Name)
	}
	edgeProperties := []string{"edge_uid", "tenant_id", "cluster_id", "status", "source", "confidence", "generation", "first_seen_ms", "last_seen_ms", "valid_from_ms", "valid_to_ms", "propagates_failure", "candidate_direction", "impact_direction", "attrs_version", "attrs_json"}
	for _, name := range edgeProperties {
		dataType := "TEXT"
		if name == "confidence" {
			dataType = "DOUBLE"
		}
		if name == "generation" || strings.HasSuffix(name, "_ms") || name == "attrs_version" {
			dataType = "LONG"
		}
		if name == "propagates_failure" {
			dataType = "BOOLEAN"
		}
		properties = append(properties, SchemaResource{Kind: "propertykey", Name: name, Payload: map[string]interface{}{"name": name, "data_type": dataType, "cardinality": "SINGLE"}})
	}
	vertex := SchemaResource{Kind: "vertexlabel", Name: "Entity", Payload: map[string]interface{}{
		"name": "Entity", "id_strategy": "CUSTOMIZE_STRING", "properties": entityProperties, "enable_label_index": true,
	}}
	resources := append(properties, vertex)
	for _, relation := range RelationTypes() {
		resources = append(resources, SchemaResource{Kind: "edgelabel", Name: relation, Payload: map[string]interface{}{
			"name": relation, "source_label": "Entity", "target_label": "Entity", "frequency": "SINGLE",
			"properties": edgeProperties, "nullable_keys": edgeProperties,
		}})
	}
	indexes := []struct {
		name      string
		fields    []string
		indexType string
	}{
		{"entityByType", []string{"entity_type"}, "SECONDARY"},
		{"entityByTenantCluster", []string{"tenant_id", "cluster_id"}, "SECONDARY"},
		{"entityByClusterNs", []string{"cluster_id", "namespace"}, "SECONDARY"},
		{"entityByStatus", []string{"status"}, "SECONDARY"},
		{"entityBySource", []string{"source"}, "SECONDARY"},
		{"entityByLastSeen", []string{"last_seen_ms"}, "RANGE"},
	}
	for _, index := range indexes {
		resources = append(resources, SchemaResource{Kind: "indexlabel", Name: index.name, Payload: map[string]interface{}{
			"name": index.name, "base_type": "VERTEX_LABEL", "base_value": "Entity", "index_type": index.indexType, "fields": index.fields,
		}})
	}
	// HugeGraph edge indexes are label-scoped. Reconciliation therefore needs
	// the same tenant/cluster/source composite index on every frozen relation
	// label to avoid a graph-wide edge scan.
	for _, relation := range RelationTypes() {
		name := "edgeByScope_" + relation
		resources = append(resources, SchemaResource{Kind: "indexlabel", Name: name, Payload: map[string]interface{}{
			"name": name, "base_type": "EDGE_LABEL", "base_value": relation, "index_type": "SECONDARY",
			"fields": []string{"tenant_id", "cluster_id", "source"},
		}})
	}
	return resources
}

func (c *HugeGraphClient) EnsureSchema(ctx context.Context) error {
	for _, resource := range SchemaResourcesV2() {
		plural := schemaResourceCollection(resource.Kind)
		data, err := c.request(ctx, http.MethodGet, "/schema/"+plural+"/"+escapeHugeGraphPathComponent(resource.Name), nil, false)
		if err == nil {
			var existing map[string]interface{}
			if decodeErr := json.Unmarshal(data, &existing); decodeErr != nil {
				return decodeErr
			}
			if !schemaResourceMatches(resource.Payload, existing) {
				if resource.Kind == "vertexlabel" || resource.Kind == "edgelabel" {
					if appendErr := c.appendLabelProperties(ctx, resource, existing); appendErr != nil {
						return appendErr
					}
					refreshed, refreshErr := c.request(ctx, http.MethodGet, "/schema/"+plural+"/"+escapeHugeGraphPathComponent(resource.Name), nil, false)
					if refreshErr != nil {
						return refreshErr
					}
					if decodeErr := json.Unmarshal(refreshed, &existing); decodeErr != nil {
						return decodeErr
					}
					if schemaResourceMatches(resource.Payload, existing) {
						continue
					}
				}
				return graphError(ErrGraphSchemaMismatch, resource.Kind+"/"+resource.Name)
			}
			continue
		}
		if !strings.Contains(err.Error(), "HTTP 404") {
			return err
		}
		if _, err := c.request(ctx, http.MethodPost, "/schema/"+plural, resource.Payload, true); err != nil {
			return err
		}
	}
	return nil
}

func (c *HugeGraphClient) appendLabelProperties(ctx context.Context, resource SchemaResource, existing map[string]interface{}) error {
	wantProperties, ok := resource.Payload["properties"].([]string)
	if !ok {
		return graphError(ErrGraphSchemaMismatch, resource.Kind+"/"+resource.Name+" properties")
	}
	gotProperties := make(map[string]struct{})
	if raw, ok := existing["properties"].([]interface{}); ok {
		for _, item := range raw {
			if name, ok := item.(string); ok {
				gotProperties[name] = struct{}{}
			}
		}
	}
	missing := make([]string, 0, len(wantProperties))
	for _, name := range wantProperties {
		if _, ok := gotProperties[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return graphError(ErrGraphSchemaMismatch, resource.Kind+"/"+resource.Name+" immutable fields")
	}
	payload := map[string]interface{}{"name": resource.Name, "properties": missing, "nullable_keys": missing}
	_, err := c.request(ctx, http.MethodPut, "/schema/"+schemaResourceCollection(resource.Kind)+"/"+escapeHugeGraphPathComponent(resource.Name)+"?action=append", payload, true)
	return err
}

func schemaResourceCollection(kind string) string {
	if kind == "propertykey" {
		return "propertykeys"
	}
	return kind + "s"
}

func schemaResourceMatches(want, got map[string]interface{}) bool {
	for key, value := range want {
		actual, ok := got[key]
		if !ok {
			return false
		}
		if key == "properties" || key == "nullable_keys" || key == "fields" {
			if !schemaStringSetMatches(value, actual) {
				return false
			}
			continue
		}
		wantBytes, _ := json.Marshal(value)
		gotBytes, _ := json.Marshal(actual)
		if string(wantBytes) != string(gotBytes) {
			return false
		}
	}
	return true
}

func schemaStringSetMatches(want, got interface{}) bool {
	toSet := func(value interface{}) (map[string]struct{}, bool) {
		set := make(map[string]struct{})
		switch items := value.(type) {
		case []string:
			for _, item := range items {
				set[item] = struct{}{}
			}
		case []interface{}:
			for _, item := range items {
				item, ok := item.(string)
				if !ok {
					return nil, false
				}
				set[item] = struct{}{}
			}
		default:
			return nil, false
		}
		return set, true
	}
	wantSet, wantOK := toSet(want)
	gotSet, gotOK := toSet(got)
	if !wantOK || !gotOK || len(wantSet) != len(gotSet) {
		return false
	}
	for item := range wantSet {
		if _, ok := gotSet[item]; !ok {
			return false
		}
	}
	return true
}

var errRawGraphLanguage = errors.New("raw graph language is not supported")
