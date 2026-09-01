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
	"strconv"
	"strings"
	"time"
)

type HugeGraphClient struct {
	baseURL           string
	graphspaceURL     string
	graphName         string
	username          string
	password          string
	readClient        *http.Client
	writeClient       *http.Client
	maintenanceClient *http.Client
}

// HugeGraph expands edge upserts into an `id in [...]` lookup and rejects
// queries above 16 KiB. Keep the logical projector/load-test batch at 500, but
// split the physical REST payload just below that server-side limit. The
// previous 12 KiB budget was unnecessarily conservative and made the 1M-edge
// gate much slower than required.
const hugeGraphEdgeIDQueryBudget = 15000

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
	maintenanceTimeout := 30 * time.Second
	if writeTimeout > maintenanceTimeout {
		maintenanceTimeout = writeTimeout
	}
	graphspaceBase := strings.TrimRight(rawURL, "/") + "/graphspaces/" + url.PathEscape(graphspace)
	base := graphspaceBase + "/graphs/" + url.PathEscape(graph)
	return &HugeGraphClient{
		baseURL: base, graphspaceURL: graphspaceBase, graphName: graph, username: username, password: password,
		readClient: &http.Client{Timeout: readTimeout}, writeClient: &http.Client{Timeout: writeTimeout},
		maintenanceClient: &http.Client{Timeout: maintenanceTimeout},
	}, nil
}

func (c *HugeGraphClient) request(ctx context.Context, method, relativePath string, payload interface{}, write bool) ([]byte, error) {
	return c.requestURL(ctx, c.baseURL+relativePath, method, payload, write)
}

func (c *HugeGraphClient) requestURL(ctx context.Context, targetURL, method string, payload interface{}, write bool) ([]byte, error) {
	return c.requestURLWithClient(ctx, targetURL, method, payload, write, false)
}

func (c *HugeGraphClient) requestURLWithClient(ctx context.Context, targetURL, method string, payload interface{}, write, maintenance bool) ([]byte, error) {
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
	} else if maintenance {
		client = c.maintenanceClient
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
		return c.VerifyGraph(ctx)
	}
	payload := map[string]interface{}{
		"gremlin.graph": "org.apache.hugegraph.auth.HugeFactoryAuthProxy",
		"backend":       "rocksdb",
		"serializer":    "binary",
		"store":         c.graphName,
		// The default standalone graph owns /data and /wal. Keep the named
		// aiops graph below the same PVC root so the two RocksDB instances do
		// not open the same column-family files.
		"rocksdb.data_path": "/var/lib/hugegraph/data/" + c.graphName,
		"rocksdb.wal_path":  "/var/lib/hugegraph/wal/" + c.graphName,
	}
	_, err = c.requestURL(ctx, c.graphspaceURL+"/graphs/"+escapeHugeGraphPathComponent(c.graphName), http.MethodPost, payload, true)
	if err != nil && !strings.Contains(err.Error(), "HTTP 409") {
		return err
	}
	return c.VerifyGraph(ctx)
}

// VerifyGraph confirms that the configured named graph is readable and uses
// the required RocksDB backend before schema or projection writes proceed.
func (c *HugeGraphClient) VerifyGraph(ctx context.Context) error {
	data, err := c.request(ctx, http.MethodGet, "", nil, false)
	if err != nil {
		return err
	}
	var graph map[string]interface{}
	if err := json.Unmarshal(data, &graph); err != nil {
		return err
	}
	if backend, ok := graph["backend"].(string); ok && backend != "rocksdb" {
		return graphError(ErrGraphSchemaMismatch, "graph backend must be rocksdb")
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

// ListVerticesForScope is a typed, bounded read used only by generation
// reconciliation. The server-side scope predicates use the existing
// composite/source indexes and offset pagination; callers cannot provide
// Gremlin or an arbitrary path. Maintenance reads use a longer timeout than
// interactive graph reads because a production scope may be large.
func (c *HugeGraphClient) ListVerticesForScope(ctx context.Context, source, tenantID, clusterID string) ([]map[string]interface{}, error) {
	return c.listVerticesPage(ctx, map[string]string{"source": source, "tenant_id": tenantID, "cluster_id": clusterID})
}

func (c *HugeGraphClient) listVerticesPage(ctx context.Context, properties map[string]string) ([]map[string]interface{}, error) {
	const pageSize = 5000
	items := make([]map[string]interface{}, 0)
	encodedProperties, err := json.Marshal(properties)
	if err != nil {
		return nil, err
	}
	for offset := 0; ; offset += pageSize {
		params := url.Values{}
		params.Set("label", "Entity")
		params.Set("properties", string(encodedProperties))
		params.Set("offset", strconv.Itoa(offset))
		params.Set("limit", strconv.Itoa(pageSize))
		data, err := c.requestURLWithClient(ctx, c.baseURL+"/graph/vertices?"+params.Encode(), http.MethodGet, nil, false, true)
		if err != nil {
			return nil, err
		}
		page, err := graphPageItems(data, "vertices")
		if err != nil {
			return nil, err
		}
		items = append(items, page...)
		if len(page) < pageSize {
			return items, nil
		}
	}
}

func graphPageItems(data []byte, key string) ([]map[string]interface{}, error) {
	var envelope map[string]interface{}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	for _, itemKey := range []string{key, "items"} {
		if raw, ok := envelope[itemKey].([]interface{}); ok {
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

// ListEdgesForScope reads each frozen edge label independently. HugeGraph
// edge property indexes are label-scoped, so this retains the scope predicate
// without a graph-wide edge scan.
func (c *HugeGraphClient) ListEdgesForScope(ctx context.Context, source, tenantID, clusterID string) ([]map[string]interface{}, error) {
	const pageSize = 5000
	items := make([]map[string]interface{}, 0)
	encodedProperties, err := json.Marshal(map[string]string{"source": source, "tenant_id": tenantID, "cluster_id": clusterID})
	if err != nil {
		return nil, err
	}
	for _, label := range RelationTypes() {
		for offset := 0; ; offset += pageSize {
			params := url.Values{}
			params.Set("label", label)
			params.Set("properties", string(encodedProperties))
			params.Set("offset", strconv.Itoa(offset))
			params.Set("limit", strconv.Itoa(pageSize))
			data, err := c.requestURLWithClient(ctx, c.baseURL+"/graph/edges?"+params.Encode(), http.MethodGet, nil, false, true)
			if err != nil {
				return nil, err
			}
			page, err := graphPageItems(data, "edges")
			if err != nil {
				return nil, err
			}
			items = append(items, page...)
			if len(page) < pageSize {
				break
			}
		}
	}
	return items, nil
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
	// HugeGraph 1.7's advanced K-neighbor endpoint accepts the traversal
	// policy under `steps`; sending the internal request struct directly would
	// emit unsupported top-level fields (for example `direction`).
	edgeSteps := make([]map[string]interface{}, 0, len(request.EdgeLabels))
	for _, label := range request.EdgeLabels {
		if strings.TrimSpace(label) == "" {
			continue
		}
		edgeSteps = append(edgeSteps, map[string]interface{}{"label": label})
	}
	steps := map[string]interface{}{
		"direction":    normalizedDirection(request.Direction),
		"edge_steps":   edgeSteps,
		"vertex_steps": []map[string]interface{}{{"label": "Entity"}},
		"max_degree":   request.Capacity,
	}
	payload := map[string]interface{}{
		"source":      request.Source,
		"steps":       steps,
		"max_depth":   request.MaxDepth,
		"limit":       request.Limit,
		"with_vertex": request.WithVertex,
		"with_path":   request.WithPath,
		"with_edge":   request.WithEdge,
	}
	data, err := c.request(ctx, http.MethodPost, "/traversers/kneighbor", payload, false)
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
	query := url.Values{}
	query.Set("source", quotedHugeGraphQueryID(source))
	query.Set("target", quotedHugeGraphQueryID(target))
	query.Set("direction", "BOTH")
	query.Set("max_depth", strconv.Itoa(maxDepth))
	query.Set("max_degree", strconv.Itoa(InternalGraphQueryLimits().MaxVertices))
	query.Set("capacity", strconv.Itoa(InternalGraphQueryLimits().Capacity))
	if len(edgeLabels) > 0 {
		query.Set("label", edgeLabels[0])
	}
	data, err := c.requestURL(ctx, c.baseURL+"/traversers/shortestpath?"+query.Encode(), http.MethodGet, nil, false)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func quotedHugeGraphQueryID(entityUID string) string {
	return `"` + entityUID + `"`
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// EdgesBetween returns only the bounded set of edges connecting source and
// target. The shortest-path REST API returns vertex IDs only, so the
// repository uses this indexed vertex query to reconstruct a typed subgraph
// without scanning all graph edges.
func (c *HugeGraphClient) EdgesBetween(ctx context.Context, source, target string, edgeLabels []string) ([]map[string]interface{}, error) {
	query := url.Values{}
	query.Set("vertex_id", quotedHugeGraphQueryID(source))
	query.Set("direction", "BOTH")
	query.Set("limit", strconv.Itoa(InternalGraphQueryLimits().MaxEdges))
	if len(edgeLabels) == 1 {
		query.Set("label", edgeLabels[0])
	}
	data, err := c.requestURL(ctx, c.baseURL+"/graph/edges?"+query.Encode(), http.MethodGet, nil, false)
	if err != nil {
		return nil, err
	}
	var envelope map[string]interface{}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	result := make([]map[string]interface{}, 0)
	for _, item := range interfaceSlice(envelope["edges"]) {
		edge, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		edgeSource := firstString(edge, "outV", "source", "source_uid")
		edgeTarget := firstString(edge, "inV", "target", "target_uid")
		if !((edgeSource == source && edgeTarget == target) || (edgeSource == target && edgeTarget == source)) {
			continue
		}
		if len(edgeLabels) > 1 {
			label := firstString(edge, "label", "relation_type")
			if !containsString(edgeLabels, label) {
				continue
			}
		}
		result = append(result, edge)
	}
	return result, nil
}

func (c *HugeGraphClient) RawQuery(context.Context, string) (map[string]interface{}, error) {
	return nil, graphError(ErrGraphFeatureUnavailable, "raw Gremlin/Cypher is not an allowed repository operation")
}
