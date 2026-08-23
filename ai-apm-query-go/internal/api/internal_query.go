package api

import (
	"encoding/json"
	"net/http"

	"github.com/observability-platform/ai-apm-query-go/internal/query"
)

// ─────────────────────────────────────────────────────────────────────────────
// P6.2e Canonical Internal Query API（Phase 6）。
//
// 8 个 /internal/v1/query/* 端点共用 strict internal envelope（TrustedRequestContext
// ONLY，无 JWT fallback），全部直接复用现有 typed repositories，禁止第二套 SQL/query logic。
//
// 所有 handler 统一流程：
//   authorizeInternalQuery → decode body → checkScopeMatch → repository → QueryError semantics
//
// handler 命名统一 InternalQueryXxx，与 /api/v1/* 公共 handler（QueryXxx）区分，
// 避免与既有 public QueryMetrics/QueryLogs 冲突。
// ─────────────────────────────────────────────────────────────────────────────

// internalQueryRequest 是 canonical internal query 的通用请求体。
// tenant/cluster 由 TrustedRequestContext 服务端注入并强制一致；body 不得覆盖。
type internalQueryRequest struct {
	TenantID  string   `json:"tenant_id"`
	ClusterID string   `json:"cluster_id"`
	Service   string   `json:"service"`
	Services  []string `json:"services"`
	Query     string   `json:"query"`
	Since     string   `json:"since"`
	Minutes   int      `json:"minutes"`
	Hours     int      `json:"hours"`
	Namespace string   `json:"namespace"`
	Limit     int      `json:"limit"`
	Offset    int      `json:"offset"`
	TopK      int      `json:"top_k"`
}

// decodeInternalRequest 解析请求体并校验可信作用域与 body 一致性。
func decodeInternalRequest(r *http.Request, capability string) (*internalQueryCtx, *internalQueryRequest, error) {
	rctx, err := authorizeInternalQuery(r, capability)
	if err != nil {
		return nil, nil, err
	}
	var req internalQueryRequest
	if err := decodeBody(r, &req); err != nil {
		return nil, nil, &internalQueryError{Code: "VALIDATION_FAILED", Message: "invalid request body"}
	}
	if err := checkScopeMatch(rctx, req.TenantID, req.ClusterID); err != nil {
		return nil, nil, err
	}
	return rctx, &req, nil
}

// InternalQueryMetrics handles POST /internal/v1/query/metrics → observability.metrics.read。
func (h *Handler) InternalQueryMetrics(w http.ResponseWriter, r *http.Request) {
	rctx, req, err := decodeInternalRequest(r, "observability.metrics.read")
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	pts, err := h.metricsRepo.ServiceRED(r.Context(), query.Scope{
		TenantID: rctx.TenantID, ClusterID: rctx.ClusterID, Services: req.Services,
	}, req.Service, req.Minutes)
	if err != nil {
		respondQueryError(w, err)
		return
	}
	respondJSON(w, 200, map[string]interface{}{"points": pts, "total": len(pts)})
}

// InternalQueryLogs handles POST /internal/v1/query/logs → observability.logs.read。
func (h *Handler) InternalQueryLogs(w http.ResponseWriter, r *http.Request) {
	rctx, req, err := decodeInternalRequest(r, "observability.logs.read")
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	records, err := h.logRepo.SearchRawLogs(r.Context(), query.LogQuery{
		TenantID:   rctx.TenantID,
		ClusterID:  rctx.ClusterID,
		ResourceID: req.Namespace,
		Service:    req.Service,
		Query:      req.Query,
		Services:   req.Services,
		Minutes:    req.Minutes,
	})
	if err != nil {
		respondQueryError(w, err)
		return
	}
	respondJSON(w, 200, map[string]interface{}{"logs": records, "total": len(records)})
}

// InternalQueryTraces handles POST /internal/v1/query/traces → observability.traces.read。
func (h *Handler) InternalQueryTraces(w http.ResponseWriter, r *http.Request) {
	rctx, req, err := decodeInternalRequest(r, "observability.traces.read")
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	limit, offset := req.Limit, req.Offset
	if limit <= 0 {
		limit = 20
	}
	traces, err := h.traceRepo.FindTraces(r.Context(), query.TraceQuery{
		TenantID:  rctx.TenantID,
		ClusterID: rctx.ClusterID,
		Service:   req.Service,
		Services:  req.Services,
		Keyword:   req.Query,
		Hours:     req.Hours,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		respondQueryError(w, err)
		return
	}
	respondJSON(w, 200, map[string]interface{}{"traces": traces, "total": len(traces)})
}

// InternalQueryAlerts handles POST /internal/v1/query/alerts → observability.alerts.read。
func (h *Handler) InternalQueryAlerts(w http.ResponseWriter, r *http.Request) {
	_, req, err := decodeInternalRequest(r, "observability.alerts.read")
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	events, err := h.alertRepo.ListEvents(r.Context(), req.Service, limit, req.Offset)
	if err != nil {
		respondQueryError(w, err)
		return
	}
	respondJSON(w, 200, map[string]interface{}{"alerts": events, "total": len(events)})
}

// InternalQueryTopology handles POST /internal/v1/query/topology → observability.topology.read。
func (h *Handler) InternalQueryTopology(w http.ResponseWriter, r *http.Request) {
	rctx, req, err := decodeInternalRequest(r, "observability.topology.read")
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	scope := query.TopologyScope{TenantID: rctx.TenantID, ClusterID: rctx.ClusterID, Services: req.Services}
	minutes := req.Minutes
	if minutes <= 0 {
		minutes = 60
	}
	nodes, nerr := h.topoRepo.GlobalNodes(r.Context(), scope, minutes)
	if nerr != nil {
		respondQueryError(w, nerr)
		return
	}
	edges, eerr := h.topoRepo.GlobalEdges(r.Context(), scope, minutes)
	if eerr != nil {
		respondQueryError(w, eerr)
		return
	}
	respondJSON(w, 200, map[string]interface{}{"nodes": nodes, "edges": edges})
}

// InternalQueryKubernetes handles POST /internal/v1/query/kubernetes → kubernetes.resources.read。
func (h *Handler) InternalQueryKubernetes(w http.ResponseWriter, r *http.Request) {
	rctx, req, err := decodeInternalRequest(r, "kubernetes.resources.read")
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	// 请求可选 namespace（默认 all）用于 Pod 过滤
	namespace := "all"
	if req != nil && req.Namespace != "" {
		namespace = req.Namespace
	}
	scope := query.KubernetesScope{TenantID: rctx.TenantID, ClusterID: rctx.ClusterID}

	nodeDetails := []map[string]interface{}{}
	if nd, err := h.kubeRepo.ListNodeDetails(r.Context(), scope, rctx.ClusterID); err == nil {
		nodeDetails = nd
	}

	pods := []query.KubePod{}
	if p, err := h.kubeRepo.ListPods(r.Context(), scope, rctx.ClusterID, namespace); err == nil {
		pods = p
	}

	nodes := []string{}
	if n, err := h.kubeRepo.ListNodeNames(r.Context(), scope, rctx.ClusterID); err == nil {
		nodes = n
	}

	respondJSON(w, 200, map[string]interface{}{
		"nodes": nodes, "node_details": nodeDetails, "pods": pods,
		"total_nodes": len(nodes), "total_pods": len(pods),
	})
}

// InternalQueryChanges handles POST /internal/v1/query/changes → changes.read。
func (h *Handler) InternalQueryChanges(w http.ResponseWriter, r *http.Request) {
	rctx, req, err := decodeInternalRequest(r, "changes.read")
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	changes, err := h.changeRepo.List(r.Context(), query.ChangeScope{
		TenantID: rctx.TenantID, ClusterID: rctx.ClusterID,
	}, req.Service, req.Since)
	if err != nil {
		respondQueryError(w, err)
		return
	}
	respondJSON(w, 200, map[string]interface{}{"changes": changes, "total": len(changes)})
}

// InternalQueryKnowledge handles POST /internal/v1/query/knowledge → knowledge.search。
func (h *Handler) InternalQueryKnowledge(w http.ResponseWriter, r *http.Request) {
	rctx, req, err := decodeInternalRequest(r, "knowledge.search")
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	if req.Query == "" {
		respondInternalQueryError(w, &internalQueryError{Code: "VALIDATION_FAILED", Message: "query required"})
		return
	}
	if h.knowledgeRepo == nil {
		respondQueryError(w, query.Unavailable("knowledge: repository not configured"))
		return
	}
	hits, err := h.knowledgeRepo.Search(r.Context(), query.KnowledgeScope{
		TenantID: rctx.TenantID, ClusterID: rctx.ClusterID,
	}, req.Query, req.TopK)
	if err != nil {
		respondQueryError(w, err)
		return
	}
	respondJSON(w, 200, map[string]interface{}{"results": hits, "total": len(hits)})
}

// decodeBody 解析请求体 JSON。
func decodeBody(r *http.Request, target interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(target)
}
