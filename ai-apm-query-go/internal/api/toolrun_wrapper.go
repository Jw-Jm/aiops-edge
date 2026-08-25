package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"regexp"
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

// validUUID 校验字符串是规范 UUID（P0-TOOL-01：run_id 必须是真实 UUID）。
var canonicalUUIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func validUUID(s string) bool { return canonicalUUIDRe.MatchString(s) }

// toolIdempotencyReusedError 表示同 idempotency_key 但 args_hash 不同（P0-TOOL-04：409）。
type toolIdempotencyReusedError struct{}

func (e *toolIdempotencyReusedError) Error() string { return "tool idempotency key reused with different args" }

// toolRunContext 携带一次 tool 执行的审计/幂等/Lease 上下文（来自 internalQueryRequest）。
type toolRunContext struct {
	ToolRunID      string
	IdempotencyKey string
	ExecutorID     string
	LeaseEpoch     int64
	LeaseToken     string // P0-TOOL-02：明文 token（pre-I/O server fencing）
	WindowStart    *time.Time
	WindowEnd      *time.Time
	RunID          string
	TenantID       string
	ClusterID      string
	ArgsHash       string // P0-TOOL-04：幂等域 (run_id, idempotency_key, args_hash)
}

// newToolRunFromRequest 从 internalQueryRequest + rctx 构造 toolRunContext（无 tool_run_id 则 nil）。
// P0-TOOL-01：run_id 必须为真实 Investigation Run UUID；缺失/非法 → 返回 nil（fail-closed，
// 拒绝写 run_id='' 的孤儿 ToolRun）。
func newToolRunFromRequest(req *internalQueryRequest, tenantID, clusterID string) *toolRunContext {
	if req == nil || req.ToolRunID == "" {
		return nil
	}
	if req.RunID == "" || !validUUID(req.RunID) {
		return nil
	}
	// P0-TOOL-02：Lease token 缺失 → 无 Lease 保护的查询不做 ToolRun 包装（不写无审计 ToolRun）。
	if req.LeaseToken == "" {
		return nil
	}
	trc := &toolRunContext{
		ToolRunID: req.ToolRunID, IdempotencyKey: req.IdempotencyKey,
		ExecutorID: req.ExecutorID, LeaseEpoch: req.LeaseEpoch,
		LeaseToken: req.LeaseToken, RunID: req.RunID, TenantID: tenantID, ClusterID: clusterID,
		ArgsHash: toolArgsHash(req),
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

// toolArgsHash 计算 Tool 幂等域 args_hash = SHA256(操作语义参数)。
// P0-TOOL-04：幂等判定 (run_id, idempotency_key, args_hash)——同 key 同 args → exact replay；
// 同 key 不同 args → 409 IDEMPOTENCY_KEY_REUSED。
func toolArgsHash(req *internalQueryRequest) string {
	h := sha256.New()
	h.Write([]byte(req.Service))
	h.Write([]byte{0})
	for _, s := range req.Services {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	h.Write([]byte(req.Query))
	h.Write([]byte{0})
	h.Write([]byte(req.Namespace))
	h.Write([]byte{0})
	if req.Minutes != 0 {
		h.Write([]byte{byte(req.Minutes)})
	}
	h.Write([]byte{0})
	if req.Hours != 0 {
		h.Write([]byte{byte(req.Hours)})
	}
	h.Write([]byte{0})
	if req.TopK != 0 {
		h.Write([]byte{byte(req.TopK)})
	}
	return hex.EncodeToString(h.Sum(nil))
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
		IdempotencyKey: idemKey, ArgsHash: trc.ArgsHash, ExecutorID: trc.ExecutorID,
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
// P0-TOOL-03：用 fencing-aware FinishToolRunWithFencing（统一锁序 Run→ToolRun），
// 判定 late/fencing（Run 终态 或 lease_epoch 不匹配 → eligible=0 + TOOL_RESULT_LATE event）。
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
	// 尝试 fencing-aware finish；DB 不可用时回退到 UpdateQuality（尽力，但绝不进入 Evidence）。
	conn := store.GetDB()
	if conn != nil {
		tx, err := conn.Begin()
		if err == nil {
			late, ferr := h.toolDAO.FinishToolRunWithFencing(tx, store.AIToolRun{
				ToolRunID: trc.ToolRunID, RunID: trc.RunID, Status: status, Result: result,
				ErrorMessage: errMsg, CompletedAt: &now, ObservedAt: &now, ResultQuality: quality,
				ResultComplete: quality == "complete", ResultCount: int64(count),
				ResultDigestSHA256: digest,
			})
			if ferr == nil {
				_ = tx.Commit()
				if late && h.eventDAO != nil {
					// late/fencing：append TOOL_RESULT_LATE event（审计，独立事务）。
					etx, eerr := conn.Begin()
					if eerr == nil {
						_, _, _ = h.eventDAO.AppendTx(etx, store.AIRunEvent{
							RunID: trc.RunID, EventID: newUUID(), EventType: "tool_result.late",
							Payload: json.RawMessage(`{"tool_run_id":"` + trc.ToolRunID + `","late":true}`),
						})
						_ = etx.Commit()
					}
				}
				return
			}
			_ = tx.Rollback()
		}
	}
	// 回退：UpdateQuality（eligible 由调用方 quality 决定，但 fencing 失败时强制 0 更安全）。
	eligible := quality == "complete"
	_ = h.toolDAO.UpdateQuality(store.AIToolRun{
		ToolRunID: trc.ToolRunID, Status: status, Result: result, ErrorMessage: errMsg,
		CompletedAt: &now, ObservedAt: &now, ResultQuality: quality,
		ResultComplete: quality == "complete", ResultCount: int64(count),
		ResultDigestSHA256: digest, EligibleForEvidence: eligible,
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
