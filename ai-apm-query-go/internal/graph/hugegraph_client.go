package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HugeGraphClient struct {
	baseURL       string
	graphspaceURL string
	graphName     string
	username      string
	password      string
	readClient    *http.Client
	writeClient   *http.Client
}

// HugeGraph checks existing edge ids with a single `id in [...]` query during
// batch upsert.  Its backend rejects that generated query above 16 KiB. Keep
// the logical reconcile batch limit at 500, but split the physical REST batch
// by a conservative id-query budget so long, namespaced UIDs remain valid.
const hugeGraphEdgeIDQueryBudget = 12000

func NewHugeGraphClient(rawURL, graphspace, graph, username, password string, readTimeout, writeTimeout time.Duration) (*HugeGraphClient, error) {
	if strings.TrimSpace(rawURL) == "" || strings.TrimSpace(graphspace) == "" || strings.TrimSpace(graph) == "" {
		return nil, errors.New("HugeGraph URL, graphspace and graph are required")
	}
	if readTimeout <= 0 {
		readTimeout = 1500 * time.Millisecond
	}
	if writeTimeout <= 0 {
		writeTimeout = 3 * time.Second
	}
	graphspaceBase := strings.TrimRight(rawURL, "/") + "/graphspaces/" + url.PathEscape(graphspace)
	base := graphspaceBase + "/graphs/" + url.PathEscape(graph)
	return &HugeGraphClient{
		baseURL: base, graphspaceURL: graphspaceBase, graphName: graph, username: username, password: password,
		readClient: &http.Client{Timeout: readTimeout}, writeClient: &http.Client{Timeout: writeTimeout},
	}, nil
}

func (c *HugeGraphClient) request(ctx context.Context, method, relativePath string, payload interface{}, write bool) ([]byte, error) {
	return c.requestURL(ctx, c.baseURL+relativePath, method, payload, write)
}

func (c *HugeGraphClient) requestURL(ctx context.Context, targetURL, method string, payload interface{}, write bool) ([]byte, error) {
	if c == nil {
		return nil, errors.New("HugeGraph client is nil")
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, body)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	client := c.readClient
	if write {
		client = c.writeClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("HugeGraph HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

// EnsureGraph makes the fixed DEFAULT/aiops graph explicit and idempotent.
// HugeGraph 1.7.0 ships the standalone graph named "hugegraph" by default;
// the production contract uses a separate aiops graph, so it must be created
// before schema migration. Dynamic graph creation is an administrator-only
// operation and the migrator is the sole caller.
func (c *HugeGraphClient) EnsureGraph(ctx context.Context) error {
	if c == nil {
		return errors.New("HugeGraph client is nil")
	}
	listData, err := c.requestURL(ctx, c.graphspaceURL+"/graphs", http.MethodGet, nil, false)
	if err != nil {
		return err
	}
	var listed struct {
		Graphs []string `json:"graphs"`
	}
	if err := json.Unmarshal(listData, &listed); err != nil {
		return err
	}
	for _, name := range listed.Graphs {
		if name != c.graphName {
			continue
		}
		data, getErr := c.request(ctx, http.MethodGet, "", nil, false)
		if getErr != nil {
			return getErr
		}
		var graph map[string]interface{}
		if decodeErr := json.Unmarshal(data, &graph); decodeErr != nil {
			return decodeErr
		}
		if backend, ok := graph["backend"].(string); ok && backend != "rocksdb" {
			return graphError(ErrGraphSchemaMismatch, "graph backend must be rocksdb")
		}
		return nil
	}
	payload := map[string]interface{}{
		"gremlin.graph": "org.apache.hugegraph.auth.HugeFactoryAuthProxy",
		"backend":       "rocksdb",
		"serializer":    "binary",
		"store":         c.graphName,
		// The default standalone graph owns /data and /wal. Keep the named
		// aiops graph below the same PVC root so the two RocksDB instances do
		// not open the same column-family files.
		"rocksdb.data_path": "/var/lib/hugegraph/data/aiops",
		"rocksdb.wal_path":  "/var/lib/hugegraph/wal/aiops",
	}
	_, err = c.requestURL(ctx, c.graphspaceURL+"/graphs/"+escapeHugeGraphPathComponent(c.graphName), http.MethodPost, payload, true)
	if err != nil && !strings.Contains(err.Error(), "HTTP 409") {
		return err
	}
	return nil
}

func (c *HugeGraphClient) GetVertex(ctx context.Context, entityUID string) (map[string]interface{}, error) {
	data, err := c.request(ctx, http.MethodGet, "/graph/vertices/"+quoteHugeGraphStringID(entityUID), nil, false)
	if err != nil {
		return nil, err
	}
	var vertex map[string]interface{}
	if err := json.Unmarshal(data, &vertex); err != nil {
		return nil, err
	}
	return vertex, nil
}

func (c *HugeGraphClient) PutVerticesBatch(ctx context.Context, entities []Entity) error {
	payload := make([]map[string]interface{}, 0, len(entities))
	for _, entity := range entities {
		properties := map[string]interface{}{
			"entity_uid": entity.EntityUID, "entity_type": entity.EntityType, "tenant_id": entity.TenantID,
			"cluster_id": entity.ClusterID, "namespace": entity.Namespace, "name": entity.Name,
			"name_key": entity.NameKey, "source": entity.Source, "source_uid": entity.SourceUID,
			"status": entity.Status, "health": entity.Health, "resolution": entity.Resolution,
			"confidence": entity.Confidence, "first_seen_ms": entity.FirstSeenMS, "last_seen_ms": entity.LastSeenMS,
			"generation": entity.Generation, "attrs_version": entity.AttrsVersion, "attrs_json": attrsJSON(entity.Attrs),
		}
		payload = append(payload, map[string]interface{}{"id": entity.EntityUID, "label": "Entity", "properties": properties})
	}
	_, err := c.request(ctx, http.MethodPut,
		"/graph/vertices/batch?create_if_not_exist=true&update_strategies=OVERRIDE",
		map[string]interface{}{
			"vertices":            payload,
			"create_if_not_exist": true,
			"update_strategies":   hugeGraphOverrideStrategies("entity_uid", "entity_type", "tenant_id", "cluster_id", "namespace", "name", "name_key", "source", "source_uid", "status", "health", "resolution", "confidence", "first_seen_ms", "last_seen_ms", "generation", "attrs_version", "attrs_json"),
		}, true)
	return err
}

// ListVertices is a typed repository-supporting read used only by generation
// reconciliation.  Callers cannot provide Gremlin or an arbitrary path.
func (c *HugeGraphClient) ListVertices(ctx context.Context) ([]map[string]interface{}, error) {
	data, err := c.request(ctx, http.MethodGet, "/graph/vertices?label=Entity&limit=100000", nil, false)
	if err != nil {
		return nil, err
	}
	var envelope map[string]interface{}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	for _, key := range []string{"vertices", "items"} {
		if raw, ok := envelope[key].([]interface{}); ok {
			items := make([]map[string]interface{}, 0, len(raw))
			for _, item := range raw {
				if value, ok := item.(map[string]interface{}); ok {
					items = append(items, value)
				}
			}
			return items, nil
		}
	}
	return []map[string]interface{}{}, nil
}

func (c *HugeGraphClient) ListEdges(ctx context.Context) ([]map[string]interface{}, error) {
	data, err := c.request(ctx, http.MethodGet, "/graph/edges?limit=100000", nil, false)
	if err != nil {
		return nil, err
	}
	var envelope map[string]interface{}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	for _, key := range []string{"edges", "items"} {
		if raw, ok := envelope[key].([]interface{}); ok {
			items := make([]map[string]interface{}, 0, len(raw))
			for _, item := range raw {
				if value, ok := item.(map[string]interface{}); ok {
					items = append(items, value)
				}
			}
			return items, nil
		}
	}
	return []map[string]interface{}{}, nil
}

func (c *HugeGraphClient) PutEdgesBatch(ctx context.Context, edges []Edge) error {
	for _, batch := range splitHugeGraphEdgeBatches(edges) {
		if err := c.putEdgesBatch(ctx, batch); err != nil {
			return err
		}
	}
	return nil
}

func (c *HugeGraphClient) putEdgesBatch(ctx context.Context, edges []Edge) error {
	payload := make([]map[string]interface{}, 0, len(edges))
	for _, edge := range edges {
		payload = append(payload, map[string]interface{}{
			"label": edge.RelationType, "outV": edge.SourceUID, "inV": edge.TargetUID,
			"outVLabel": "Entity", "inVLabel": "Entity", "properties": map[string]interface{}{
				"edge_uid": edge.EdgeUID, "tenant_id": edge.TenantID, "cluster_id": edge.ClusterID,
				"status": edge.Status, "source": edge.Source, "confidence": edge.Confidence,
				"generation": edge.Generation, "first_seen_ms": edge.FirstSeenMS, "last_seen_ms": edge.LastSeenMS,
				"valid_from_ms": edge.ValidFromMS, "valid_to_ms": edge.ValidToMS,
				"propagates_failure": edge.PropagatesFailure, "candidate_direction": edge.CandidateDirection,
				"impact_direction": edge.ImpactDirection, "attrs_version": edge.AttrsVersion, "attrs_json": attrsJSON(edge.Attrs),
			},
		})
	}
	_, err := c.request(ctx, http.MethodPut,
		"/graph/edges/batch?create_if_not_exist=true&update_strategies=OVERRIDE&check_vertex=true",
		map[string]interface{}{
			"edges":               payload,
			"create_if_not_exist": true,
			"update_strategies":   hugeGraphOverrideStrategies("edge_uid", "tenant_id", "cluster_id", "status", "source", "confidence", "generation", "first_seen_ms", "last_seen_ms", "valid_from_ms", "valid_to_ms", "propagates_failure", "candidate_direction", "impact_direction", "attrs_version", "attrs_json"),
			"check_vertex":        true,
		}, true)
	return err
}

func splitHugeGraphEdgeBatches(edges []Edge) [][]Edge {
	if len(edges) == 0 {
		return nil
	}
	result := make([][]Edge, 0, (len(edges)+49)/50)
	start, budget := 0, 0
	for index, edge := range edges {
		edgeBudget := len(edge.SourceUID) + len(edge.TargetUID) + len(edge.RelationType) + 128
		if index > start && budget+edgeBudget > hugeGraphEdgeIDQueryBudget {
			result = append(result, edges[start:index])
			start, budget = index, 0
		}
		budget += edgeBudget
	}
	return append(result, edges[start:])
}

func hugeGraphOverrideStrategies(properties ...string) map[string]string {
	strategies := make(map[string]string, len(properties))
	for _, property := range properties {
		strategies[property] = "OVERRIDE"
	}
	return strategies
}

func attrsJSON(attrs map[string]interface{}) string {
	if len(attrs) == 0 {
		return "{}"
	}
	data, err := json.Marshal(attrs)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func (c *HugeGraphClient) DeleteVertex(ctx context.Context, entityUID string) error {
	_, err := c.request(ctx, http.MethodDelete, "/graph/vertices/"+quoteHugeGraphStringID(entityUID), nil, true)
	return err
}

func (c *HugeGraphClient) GetEdge(ctx context.Context, edgeUID string) (map[string]interface{}, error) {
	data, err := c.request(ctx, http.MethodGet, "/graph/edges/"+escapeHugeGraphPathComponent(edgeUID), nil, false)
	if err != nil {
		return nil, err
	}
	var edge map[string]interface{}
	if err := json.Unmarshal(data, &edge); err != nil {
		return nil, err
	}
	return edge, nil
}

func (c *HugeGraphClient) DeleteEdge(ctx context.Context, edgeUID string) error {
	_, err := c.request(ctx, http.MethodDelete, "/graph/edges/"+escapeHugeGraphPathComponent(edgeUID), nil, true)
	return err
}

func escapeHugeGraphPathComponent(value string) string {
	// HugeGraph's path component treats ':' as a delimiter even though RFC 3986
	// permits it in a path segment, so escape it explicitly for every identifier
	// that is not a CUSTOMIZE_STRING vertex ID.
	return strings.ReplaceAll(url.PathEscape(value), ":", "%3A")
}

func quoteHugeGraphStringID(entityUID string) string {
	// HugeGraph 1.7 requires CUSTOMIZE_STRING vertex IDs to be represented as a
	// quoted string in REST paths: /vertices/"entity_uid". Encode both quotes
	// and colons instead of relying on the server to infer the ID type.
	return escapeHugeGraphPathComponent(`"` + entityUID + `"`)
}

type KNeighborRequest struct {
	Source     string   `json:"source"`
	Direction  string   `json:"direction,omitempty"`
	MaxDepth   int      `json:"max_depth"`
	Limit      int      `json:"limit"`
	Capacity   int      `json:"capacity"`
	Nearest    bool     `json:"nearest"`
	WithVertex bool     `json:"with_vertex"`
	WithPath   bool     `json:"with_path"`
	WithEdge   bool     `json:"with_edge"`
	EdgeLabels []string `json:"edge_labels,omitempty"`
}

func (c *HugeGraphClient) KNeighbor(ctx context.Context, request KNeighborRequest) (map[string]interface{}, error) {
	data, err := c.request(ctx, http.MethodPost, "/traversers/kneighbor", request, false)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *HugeGraphClient) ShortestPath(ctx context.Context, source, target string, maxDepth int, edgeLabels []string) (map[string]interface{}, error) {
	payload := map[string]interface{}{"source": source, "target": target, "max_depth": maxDepth, "edge_labels": edgeLabels}
	data, err := c.request(ctx, http.MethodPost, "/traversers/shortestpath", payload, false)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *HugeGraphClient) RawQuery(context.Context, string) (map[string]interface{}, error) {
	return nil, graphError(ErrGraphFeatureUnavailable, "raw Gremlin/Cypher is not an allowed repository operation")
}
