package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// B1-02：internal query ToolRun wrapper + ToolResultEnvelope。
//
// 目的：把 /internal/v1/query/* 8 个 handler 包装为有 ToolRun 审计/幂等/Lease 边界的
// 执行面（报告 §27.22 B1-02 / 28.2：canonical internal query 形成 ToolRun 审计/幂等/Lease
// 边界，并统一 complete/partial/failed 数据质量语义；数据源错误不得伪装为空数据）。
//
// ToolResultEnvelope（统一语义）：
//   - complete / partial / failed（数据质量）；no_data 是 complete 的一种（空但成功）。
//   - truncated：服务端硬上限截断标记（B1-01：ToolResult 服务端上限 + deterministic truncation）。
//   - digest：结果摘要（SHA256），Evidence 引用防篡改。
//   - query_window_start/end：Investigation 相对时间窗在 Run 创建时冻结为绝对时间，
//     同一 tool_run_id retry 不发生窗口漂移（报告 28.2 / 21.2-21）。
// ─────────────────────────────────────────────────────────────────────────────

// MaxToolResultBytes 是 ToolResult 服务端硬上限（超过 → truncated + RESULT_TOO_LARGE）。
const MaxToolResultBytes = 2 << 20 // 2MB

// ToolResultEnvelope 是 canonical tool query 的统一返回信封。
type ToolResultEnvelope struct {
	Quality         string          `json:"quality"`          // complete | partial | failed
	Truncated       bool            `json:"truncated"`
	Count           int             `json:"count"`
	Digest          string          `json:"digest"`
	QueryWindowStart string         `json:"query_window_start,omitempty"`
	QueryWindowEnd   string         `json:"query_window_end,omitempty"`
	SourceErrors    []string        `json:"source_errors,omitempty"`
	Data            json.RawMessage `json:"data"`
}

// toolRunContext 携带一次 tool 执行的审计/幂等/Lease 上下文（来自 internalQueryRequest）。
type toolRunContext struct {
	ToolRunID      string
	IdempotencyKey string
	ExecutorID     string
	LeaseEpoch     int64
	WindowStart    *time.Time
	WindowEnd      *time.Time
	RunID          string
	TenantID       string
	ClusterID      string
}

// newToolRunFromRequest 从 internalQueryRequest + rctx 构造 toolRunContext（无 tool_run_id 则 nil）。
func newToolRunFromRequest(req *internalQueryRequest, tenantID, clusterID string) *toolRunContext {
	if req == nil || req.ToolRunID == "" {
		return nil
	}
	trc := &toolRunContext{
		ToolRunID: req.ToolRunID, IdempotencyKey: req.IdempotencyKey,
		ExecutorID: req.ExecutorID, LeaseEpoch: req.LeaseEpoch,
		RunID: "", TenantID: tenantID, ClusterID: clusterID,
	}
	if req.QueryWindowStart != "" {
		if t, err := time.Parse(time.RFC3339, req.QueryWindowStart); err == nil {
			trc.WindowStart = &t
		}
	}
	if req.QueryWindowEnd != "" {
		if t, err := time.Parse(time.RFC3339, req.QueryWindowEnd); err == nil {
			trc.WindowEnd = &t
		}
	}
	return trc
}

// startToolRun 幂等开始一个 ToolRun 记录（B1-02：query-api 作为 ToolRun owner）。
// 返回 false 表示同 idempotency_key 已存在（不重复真实查询，直接返回既有）。
func (h *Handler) startToolRun(trc *toolRunContext) bool {
	if trc == nil || h.toolDAO == nil {
		return true // 无 tool context 或 DAO 不可用 → 不包装（调用方仍执行）
	}
	idemKey := trc.IdempotencyKey
	if idemKey == "" {
		idemKey = trc.ToolRunID
	}
	now := time.Now()
	created, err := h.toolDAO.CreateWithQuality(store.AIToolRun{
		ToolRunID: trc.ToolRunID, RunID: trc.RunID, TenantID: trc.TenantID,
		ClusterID: trc.ClusterID, ToolName: "internal_query", Status: "running",
		IdempotencyKey: idemKey, ExecutorID: trc.ExecutorID,
		LeaseEpochAtStart: trc.LeaseEpoch, StartedAt: &now,
		QueryWindowStart: trc.WindowStart, QueryWindowEnd: trc.WindowEnd,
	})
	if err != nil {
		log.Printf("toolrun start create failed (fail-closed, NOT idempotent): %v", err)
		return false // 创建失败：不执行（fail-closed）
	}
	if created {
		cp.inc("tool_started")
	}
	return created
}

// finishToolRun 结束 ToolRun 并写入 data-quality / result 字段。
func (h *Handler) finishToolRun(trc *toolRunContext, status, quality string, result []byte, count int, errMsg string) {
	if trc == nil || h.toolDAO == nil {
		return
	}
	now := time.Now()
	digest := ""
	if result != nil {
		sum := sha256.Sum256(result)
		digest = hex.EncodeToString(sum[:])
	}
	_ = h.toolDAO.UpdateQuality(store.AIToolRun{
		ToolRunID: trc.ToolRunID, Status: status, Result: result, ErrorMessage: errMsg,
		CompletedAt: &now, ObservedAt: &now, ResultQuality: quality,
		ResultComplete: quality == "complete", ResultTruncated: quality == "partial" && false,
		ResultCount: int64(count), ResultDigestSHA256: digest,
		EligibleForEvidence: quality == "complete",
	})
}

// buildEnvelope 构造 ToolResultEnvelope（含 truncation/digest/window）。
func buildEnvelope(trc *toolRunContext, quality string, data []byte, errMsg string) ToolResultEnvelope {
	env := ToolResultEnvelope{Quality: quality, Data: data}
	if trc != nil && trc.WindowStart != nil {
		env.QueryWindowStart = trc.WindowStart.UTC().Format(time.RFC3339)
		env.QueryWindowEnd = trc.WindowEnd.UTC().Format(time.RFC3339)
	}
	if len(data) > MaxToolResultBytes {
		env.Truncated = true
		// deterministic truncation：截断到上限（保留前缀），digest 基于截断后内容。
		data = data[:MaxToolResultBytes]
		env.Data = data
	}
	sum := sha256.Sum256(data)
	env.Digest = hex.EncodeToString(sum[:])
	// 解析 count（若 data 是数组）。
	if len(data) > 0 && data[0] == '[' {
		var arr []json.RawMessage
		if json.Unmarshal(data, &arr) == nil {
			env.Count = len(arr)
		}
	}
	return env
}
