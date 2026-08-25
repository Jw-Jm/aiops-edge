package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/query"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
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
// B1-02：新增 ToolRun 审计/幂等/Lease 上下文（orchestrator 传入，query-api 作为 ToolRun owner）。
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
	// B1 ToolRun 上下文（可选；缺失时不做 ToolRun 审计包装）。
	ToolRunID      string `json:"tool_run_id"`
	IdempotencyKey string `json:"idempotency_key"`
	ExecutorID     string `json:"executor_id"`
	LeaseEpoch     int64  `json:"lease_epoch"`
	WorkloadKind   string `json:"workload_kind"` // investigation | chat | platform
	// P0-TOOL-02：Lease token（明文）——Tool 执行前 server-side fencing 用。
	//   缺失则不做 ToolRun 包装（无 Lease 保护的查询不写 ToolRun，避免无审计数据面访问）。
	LeaseToken string `json:"lease_token"`
	// P0-TOOL-01：真实 run_id（Investigation Run 的 UUID）。缺失 → fail-closed 拒绝执行
	// （不得写 run_id='' 的孤儿 ToolRun）。
	RunID            string `json:"run_id"`
	QueryWindowStart string `json:"query_window_start"` // RFC3339 绝对时间（Investigation 创建时冻结）
	QueryWindowEnd   string `json:"query_window_end"`
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
	if err := checkWorkloadKindMatch(rctx.WorkloadKind, req.WorkloadKind); err != nil {
		return nil, nil, err
	}
	if req.WorkloadKind == "investigation" && req.RunID != rctx.RunID {
		return nil, nil, &internalQueryError{Code: contract.ErrorCodeContextScopeMismatch, Message: "run scope mismatch"}
	}
	return rctx, &req, nil
}

// checkWorkloadKindMatch keeps the workload boundary fail-closed. In
// particular, a body-only investigation declaration is not trusted, and a
// signed investigation cannot be omitted or downgraded by the body.
func checkWorkloadKindMatch(signed, body string) error {
	if body == "investigation" && signed != "investigation" {
		return &internalQueryError{Code: contract.ErrorCodeContextScopeMismatch, Message: "workload kind mismatch"}
	}
	if signed == "investigation" && body != "investigation" {
		return &internalQueryError{Code: contract.ErrorCodeContextScopeMismatch, Message: "workload kind mismatch"}
	}
	if body != "" && signed != "" && body != signed {
		return &internalQueryError{Code: contract.ErrorCodeContextScopeMismatch, Message: "workload kind mismatch"}
	}
	return nil
}

// beginToolRun 包装 internal query 的 ToolRun 开始（B1-02）。返回 toolRunContext（nil=不包装）。
// 幂等命中（同 idempotency_key 已存在）→ 返回 trc + idempotent=true；running/terminal
// 均只重放持久化记录，不再次访问数据源。
func (h *Handler) beginToolRun(req *internalQueryRequest, tenantID, clusterID string) (*toolRunContext, bool, error) {
	if err := validateToolRunRequest(req); err != nil {
		return nil, false, err
	}
	trc := newToolRunFromRequest(req, tenantID, clusterID)
	if trc == nil {
		if req != nil && req.WorkloadKind == "investigation" {
			return nil, false, &internalQueryError{Code: contract.ErrorCodeValidationFailed, Message: "investigation ToolRun context required"}
		}
		return nil, false, nil
	}
	if h.toolDAO == nil {
		if req.WorkloadKind == "investigation" {
			return nil, false, &internalQueryError{Code: contract.ErrorCodeToolUnavailable, Message: "ToolRun persistence unavailable"}
		}
		return trc, false, nil
	}
	// 幂等命中（P0-TOOL-04）：同 (run_id, idempotency_key, args_hash) 已有 ToolRun →
	// 不重复真实查询，返回既有（running 也只返回 202）。同 key 不同 args → 409。
	if trc.IdempotencyKey != "" {
		exists, err := h.toolDAO.GetByIdemKey(trc.RunID, trc.IdempotencyKey)
		if err == nil && exists != nil {
			if exists.ArgsHash != "" && exists.ArgsHash != trc.ArgsHash {
				return nil, false, &toolIdempotencyReusedError{}
			}
			// Both terminal and running duplicates are replayed without any
			// datasource I/O.  Running callers receive a 202 envelope and may
			// poll/retry the same idempotency key.
			trc.Existing = exists
			return trc, true, nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, false, &internalQueryError{Code: contract.ErrorCodeToolUnavailable, Message: "ToolRun idempotency lookup failed"}
		}
	}
	// 创建 ToolRun 记录；失败（非幂等冲突）→ 不执行（fail-closed，避免无审计的真实查询）。
	// 注意：只有"已存在完成结果"才算 idempotent；INSERT 失败绝不能当作幂等（否则静默跳过真实查询）。
	created, createErr := h.startToolRun(trc)
	if createErr != nil {
		return nil, false, &internalQueryError{Code: contract.ErrorCodeToolUnavailable, Message: "ToolRun persistence unavailable"}
	}
	if !created {
		// A concurrent insert won the idempotency race.  Fetch the durable
		// record and replay it instead of executing an unwrapped query.
		exists, lookupErr := h.toolDAO.GetByIdemKey(trc.RunID, trc.IdempotencyKey)
		if lookupErr != nil || exists == nil {
			return nil, false, &internalQueryError{Code: contract.ErrorCodeToolUnavailable, Message: "ToolRun idempotency record unavailable"}
		}
		if exists.ArgsHash != "" && exists.ArgsHash != trc.ArgsHash {
			return nil, false, &toolIdempotencyReusedError{}
		}
		trc.Existing = exists
		return trc, true, nil
	}
	// P0-TOOL-02：pre-I/O server-side Lease fencing——任何 datasource I/O 前校验 Run 非终态
	// + owner/epoch/token 匹配 + Lease 未过期。失败 → 返回 fencing error（调用方不执行，
	// 防迟到/过期 executor 在取消后仍访问数据面）。
	conn := store.GetDB()
	if conn != nil {
		if h.leaseDAO != nil {
			tx, txErr := conn.Begin()
			if txErr != nil {
				return nil, false, txErr
			}
			fenceErr := h.leaseDAO.FenceToolExecutionTx(tx, trc.RunID, trc.ExecutorID, trc.LeaseEpoch, hashToken(trc.LeaseToken))
			if txErr2 := tx.Rollback(); txErr2 != nil {
				return nil, false, txErr2
			}
			if fenceErr != nil {
				return nil, false, fenceErr
			}
		}
	}
	return trc, false, nil
}

func (h *Handler) respondToolReplay(w http.ResponseWriter, trc *toolRunContext) {
	status := http.StatusOK
	if trc == nil || trc.Existing == nil || trc.Existing.Status == "running" {
		status = http.StatusAccepted
	}
	respondJSON(w, status, toolReplayEnvelope(trc))
}

// endToolRun 包装 internal query 的 ToolRun 结束并返回 ToolResultEnvelope。
func (h *Handler) endToolRun(trc *toolRunContext, quality string, data []byte, errMsg string) ToolResultEnvelope {
	if trc != nil {
		h.finishToolRun(trc, qualityStatus(quality), quality, data, len(data), errMsg)
	}
	return buildEnvelope(trc, quality, data, errMsg)
}

func qualityStatus(q string) string {
	switch q {
	case "complete":
		return "success"
	case "partial":
		return "partial"
	default:
		return "failed"
	}
}

// execToolQuery 是 internal query 的统一 ToolRun 包装执行器。
// 调用方传入 exec（返回数据字节 + error；err==nil 视为 complete），本函数负责
// beginToolRun / finishToolRun / 幂等命中 / ToolResultEnvelope 响应。
func (h *Handler) execToolQuery(w http.ResponseWriter, rctx *internalQueryCtx, req *internalQueryRequest, exec func() ([]byte, error)) {
	trc, idempotent, err := h.beginToolRun(req, rctx.TenantID, rctx.ClusterID)
	if idempotent {
		h.respondToolReplay(w, trc)
		return
	}
	if err != nil {
		// P0-TOOL-02：pre-I/O fencing 失败（Lease 丢失/过期/epoch 不匹配）→ 不执行数据面访问。
		if errors.Is(err, store.ErrLeaseFencing) || errors.Is(err, store.ErrLeaseLost) {
			cp.inc("tool_started") // 记录一次被拒的工具尝试（fail-closed）
			respondJSON(w, http.StatusConflict, map[string]interface{}{"error": contract.ErrorCodeToolLeaseLost})
			return
		}
		// P0-TOOL-04：同 idempotency_key 但 args_hash 不同 → 409 IDEMPOTENCY_KEY_REUSED。
		var tkr *toolIdempotencyReusedError
		if errors.As(err, &tkr) {
			respondJSON(w, http.StatusConflict, map[string]interface{}{"error": "IDEMPOTENCY_KEY_REUSED"})
			return
		}
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "toolrun_fencing_failed"})
		return
	}
	data, err := exec()
	if err != nil {
		if trc != nil {
			h.finishToolRun(trc, "failed", "failed", nil, 0, err.Error())
		}
		respondQueryError(w, err)
		return
	}
	env := h.endToolRun(trc, "complete", data, "")
	respondJSON(w, 200, env)
}

// InternalQueryMetrics handles POST /internal/v1/query/metrics → observability.metrics.read。
func (h *Handler) InternalQueryMetrics(w http.ResponseWriter, r *http.Request) {
	rctx, req, err := decodeInternalRequest(r, "observability.metrics.read")
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	trc, idempotent, beginErr := h.beginToolRun(req, rctx.TenantID, rctx.ClusterID)
	if beginErr != nil {
		respondInternalQueryError(w, beginErr)
		return
	}
	if idempotent {
		h.respondToolReplay(w, trc)
		return
	}
	pts, err := h.metricsRepo.ServiceRED(r.Context(), query.Scope{
		TenantID: rctx.TenantID, ClusterID: rctx.ClusterID, Services: req.Services,
	}, req.Service, req.Minutes)
	if err != nil {
		if trc != nil {
			h.finishToolRun(trc, "failed", "failed", nil, 0, err.Error())
		}
		respondQueryError(w, err)
		return
	}
	data, _ := json.Marshal(map[string]interface{}{"points": pts, "total": len(pts)})
	env := h.endToolRun(trc, "complete", data, "")
	respondJSON(w, 200, env)
}

// InternalQueryLogs handles POST /internal/v1/query/logs → observability.logs.read。
func (h *Handler) InternalQueryLogs(w http.ResponseWriter, r *http.Request) {
	rctx, req, err := decodeInternalRequest(r, "observability.logs.read")
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	h.execToolQuery(w, rctx, req, func() ([]byte, error) {
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
			return nil, err
		}
		return json.Marshal(map[string]interface{}{"logs": records, "total": len(records)})
	})
}

// InternalQueryTraces handles POST /internal/v1/query/traces → observability.traces.read。
func (h *Handler) InternalQueryTraces(w http.ResponseWriter, r *http.Request) {
	rctx, req, err := decodeInternalRequest(r, "observability.traces.read")
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	h.execToolQuery(w, rctx, req, func() ([]byte, error) {
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
			return nil, err
		}
		return json.Marshal(map[string]interface{}{"traces": traces, "total": len(traces)})
	})
}

// InternalQueryAlerts handles POST /internal/v1/query/alerts → observability.alerts.read。
func (h *Handler) InternalQueryAlerts(w http.ResponseWriter, r *http.Request) {
	rctx, req, err := decodeInternalRequest(r, "observability.alerts.read")
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	h.execToolQuery(w, rctx, req, func() ([]byte, error) {
		limit := req.Limit
		if limit <= 0 {
			limit = 50
		}
		events, err := h.alertRepo.ListEvents(r.Context(), req.Service, limit, req.Offset)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]interface{}{"alerts": events, "total": len(events)})
	})
}

// InternalQueryTopology handles POST /internal/v1/query/topology → observability.topology.read。
func (h *Handler) InternalQueryTopology(w http.ResponseWriter, r *http.Request) {
	rctx, req, err := decodeInternalRequest(r, "observability.topology.read")
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	h.execToolQuery(w, rctx, req, func() ([]byte, error) {
		scope := query.TopologyScope{TenantID: rctx.TenantID, ClusterID: rctx.ClusterID, Services: req.Services}
		minutes := req.Minutes
		if minutes <= 0 {
			minutes = 60
		}
		nodes, nerr := h.topoRepo.GlobalNodes(r.Context(), scope, minutes)
		if nerr != nil {
			return nil, nerr
		}
		edges, eerr := h.topoRepo.GlobalEdges(r.Context(), scope, minutes)
		if eerr != nil {
			return nil, eerr
		}
		return json.Marshal(map[string]interface{}{"nodes": nodes, "edges": edges})
	})
}

// InternalQueryTopologyMiddleware handles POST /internal/v1/query/topology/middleware.
func (h *Handler) InternalQueryTopologyMiddleware(w http.ResponseWriter, r *http.Request) {
	rctx, req, err := decodeInternalRequest(r, "observability.topology.read")
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	h.execToolQuery(w, rctx, req, func() ([]byte, error) {
		rows, err := h.topoRepo.MiddlewareDependencies(r.Context(), query.TopologyScope{
			TenantID: rctx.TenantID, ClusterID: rctx.ClusterID,
		})
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]interface{}{"middleware": rows, "total": len(rows)})
	})
}

// InternalQueryKubernetes handles POST /internal/v1/query/kubernetes → kubernetes.resources.read。
func (h *Handler) InternalQueryKubernetes(w http.ResponseWriter, r *http.Request) {
	rctx, req, err := decodeInternalRequest(r, "kubernetes.resources.read")
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	trc, idempotent, beginErr := h.beginToolRun(req, rctx.TenantID, rctx.ClusterID)
	if beginErr != nil {
		respondInternalQueryError(w, beginErr)
		return
	}
	if idempotent {
		h.respondToolReplay(w, trc)
		return
	}
	// 请求可选 namespace（默认 all）用于 Pod 过滤
	namespace := "all"
	if req != nil && req.Namespace != "" {
		namespace = req.Namespace
	}
	scope := query.KubernetesScope{TenantID: rctx.TenantID, ClusterID: rctx.ClusterID}

	nodeDetails := []map[string]interface{}{}
	pods := []query.KubePod{}
	nodes := []string{}

	// A0-05（F-19）：K8s 子查询错误不得吞成 200 空数组（silent-empty）。
	// 聚合三个子查询，任一失败 → partial（带部分成功数据 + 明确 errors）；全部失败 → unavailable。
	partial := false
	errs := []string{}
	markErr := func(part string, err error) {
		partial = true
		errs = append(errs, part+": "+err.Error())
	}

	if nd, err := h.kubeRepo.ListNodeDetails(r.Context(), scope, rctx.ClusterID); err != nil {
		markErr("node_details", err)
	} else {
		nodeDetails = nd
	}

	if p, err := h.kubeRepo.ListPods(r.Context(), scope, rctx.ClusterID, namespace); err != nil {
		markErr("pods", err)
	} else {
		pods = p
	}

	if n, err := h.kubeRepo.ListNodeNames(r.Context(), scope, rctx.ClusterID); err != nil {
		markErr("nodes", err)
	} else {
		nodes = n
	}

	// 全部子查询失败 → 不伪装成"没有 K8s 数据"，返回 unavailable（fail-closed）。
	if partial && len(errs) == 3 {
		if trc != nil {
			h.finishToolRun(trc, "failed", "failed", nil, 0, errs[0])
		}
		respondInternalQueryError(w, &internalQueryError{
			Code: contract.ErrorCodeBackendUnavailable, Message: "kubernetes backend unavailable: " + errs[0],
		})
		return
	}

	resp := map[string]interface{}{
		"nodes": nodes, "node_details": nodeDetails, "pods": pods,
		"total_nodes": len(nodes), "total_pods": len(pods),
	}
	quality := "complete"
	if partial {
		resp["partial"] = true
		resp["errors"] = errs
		quality = "partial"
	}
	data, _ := json.Marshal(resp)
	env := h.endToolRun(trc, quality, data, "")
	respondJSON(w, 200, env)
}

// InternalQueryChanges handles POST /internal/v1/query/changes → changes.read。
func (h *Handler) InternalQueryChanges(w http.ResponseWriter, r *http.Request) {
	rctx, req, err := decodeInternalRequest(r, "changes.read")
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	h.execToolQuery(w, rctx, req, func() ([]byte, error) {
		changes, err := h.changeRepo.List(r.Context(), query.ChangeScope{
			TenantID: rctx.TenantID, ClusterID: rctx.ClusterID,
		}, req.Service, req.Since)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]interface{}{"changes": changes, "total": len(changes)})
	})
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
	h.execToolQuery(w, rctx, req, func() ([]byte, error) {
		hits, err := h.knowledgeRepo.Search(r.Context(), query.KnowledgeScope{
			TenantID: rctx.TenantID, ClusterID: rctx.ClusterID,
		}, req.Query, req.TopK)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]interface{}{"results": hits, "total": len(hits)})
	})
}

// decodeBody 解析请求体 JSON。
func decodeBody(r *http.Request, target interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(target)
}
