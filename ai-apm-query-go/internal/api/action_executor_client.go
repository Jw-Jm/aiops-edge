package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	trustedauth "github.com/observability-platform/ai-apm-query-go/internal/auth"
	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// Stage D 接线（报告 §29）：query-api 是 ai-action-executor 的唯一调用方。
//
// 安全模型：
//   - executor 拒绝未签名的执行上下文（X-Executor-Signature = Ed25519 over body
//     SHA256，公钥 base64）。query-api 用自身 Ed25519 私钥
//     （QUERY_TO_ORCHESTRATOR_SIGNING_KEY）签发，executor 持对应公钥
//     （EXECUTOR_VERIFY_KEYS）验签。复用既有密钥对，不引入新密钥。
//   - query-api 只在 action 已 approved（ai_approval_decisions 存在 approved 记录）
//     时签发并转发。
//   - 生产 EXECUTION_MODE=disabled 时 executor 返回 403 → query-api 把
//     execution_status=rejected + EXECUTOR_REJECTED 持久化（不伪装成功）。
//   - execution_unknown（外部 mutation 已发生但响应丢失）→ query-api 调
//     /v1/executor/reconcile 判定，不盲目 retry（reconcile-before-retry）。
// ─────────────────────────────────────────────────────────────────────────────

var (
	actionExecutorMu sync.RWMutex
	actionExecutor   *ActionExecutionClient
)

// ActionExecutionClient 是 query-api → ai-action-executor 的 HTTP 客户端。
type ActionExecutionClient struct {
	baseURL    string // executor base URL（如 http://ai-action-executor:8080）
	httpClient *http.Client
	privateKey ed25519.PrivateKey // query-api Ed25519 私钥（签发 signed context）
	token      string             // 可选方向性 service token（EXECUTOR_TOKEN 匹配）
}

// ConfigureActionExecutionClient 注入 executor 客户端（env: AI_ACTION_EXECUTOR_URL +
// QUERY_TO_ORCHESTRATOR_SIGNING_KEY）。未配置时执行端点返回 EXECUTOR_UNAVAILABLE
// fail-closed（不产生未签名/不可达的静默执行）。
func ConfigureActionExecutionClient(baseURL, encodedPrivateKey, token string) error {
	if strings.TrimSpace(baseURL) == "" {
		return errors.New("AI_ACTION_EXECUTOR_URL is empty")
	}
	priv, err := trustedauth.DecodePrivateKey(strings.TrimSpace(encodedPrivateKey))
	if err != nil {
		return fmt.Errorf("decode executor signing key: %w", err)
	}
	client := &ActionExecutionClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: 20 * time.Second},
		privateKey: priv,
		token:      token,
	}
	actionExecutorMu.Lock()
	actionExecutor = client
	actionExecutorMu.Unlock()
	return nil
}

func currentActionExecutor() *ActionExecutionClient {
	actionExecutorMu.RLock()
	defer actionExecutorMu.RUnlock()
	return actionExecutor
}

// signBody 用 Ed25519 对 body 的 SHA256 签名，返回 hex 签名（executor verifySignedContext 约定）。
func (c *ActionExecutionClient) signBody(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(ed25519.Sign(c.privateKey, digest[:]))
}

// Execute 转发一个已批准的 action 到 executor，并返回其 ActionResult。
// 返回 (result, executorReached, err)：
//   - executorReached=false 表示 executor 不可达（网络/未配置）→ EXECUTOR_UNAVAILABLE。
//   - 否则 result 携带 executor 返回的 status（success/failed/execution_unknown/rejected）。
func (c *ActionExecutionClient) Execute(ctx contract.ActionExecutionContext) (contract.ActionResult, bool, error) {
	var zero contract.ActionResult
	body, err := json.Marshal(ctx)
	if err != nil {
		return zero, false, fmt.Errorf("marshal action context: %w", err)
	}
	url := c.baseURL + "/v1/executor/execute"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return zero, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Executor-Signature", c.signBody(body))
	if c.token != "" {
		req.Header.Set("X-Executor-Token", c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return zero, false, fmt.Errorf("executor unreachable: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var res contract.ActionResult
	if err := json.Unmarshal(respBody, &res); err != nil {
		res = contract.ActionResult{
			ActionID: ctx.ActionID, Status: httpStatusToExecutionStatus(resp.StatusCode),
			Message: "executor returned non-JSON: " + strings.TrimSpace(string(respBody)),
		}
	}
	return res, true, nil
}

// Reconcile 在 execution_unknown 后调用 executor 判定目标实际状态（不盲目 retry）。
func (c *ActionExecutionClient) Reconcile(actionID, targetUID, expectedSpec string) (contract.ActionResult, bool, error) {
	var zero contract.ActionResult
	payload := map[string]string{
		"action_id":     actionID,
		"target_uid":    targetUID,
		"expected_spec": expectedSpec,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return zero, false, err
	}
	url := c.baseURL + "/v1/executor/reconcile"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return zero, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Executor-Signature", c.signBody(body))
	if c.token != "" {
		req.Header.Set("X-Executor-Token", c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return zero, false, fmt.Errorf("executor unreachable: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var res contract.ActionResult
	if err := json.Unmarshal(respBody, &res); err != nil {
		res = contract.ActionResult{ActionID: actionID, Status: "reconciled"}
	}
	return res, true, nil
}

// httpStatusToExecutionStatus 把 executor 的非 JSON/非 2xx 响应映射为稳定执行状态。
func httpStatusToExecutionStatus(status int) string {
	switch {
	case status == http.StatusForbidden:
		return "rejected" // EXECUTION_MODE=disabled
	case status == http.StatusServiceUnavailable:
		return "rejected" // approved 但 verify key 未配置
	case status == http.StatusConflict:
		return "rejected" // TOCTOU drift
	default:
		return "failed"
	}
}

// executeApprovedAction 实现 Stage D 执行闭环（handler 调用）：
//  1. 校验 action 已 approved（ai_approval_decisions 有 approved 记录）。
//  2. durable idempotency：execution_status 已 terminal（success/failed/rejected/
//     rollback_required）→ 直接返回已记录结果，不重复执行。
//  3. 构造 ActionExecutionContext + 签发 signed context → POST executor。
//  4. 按 executor 结果持久化到 ai_actions（UpdateExecution）。
//  5. execution_unknown → 调 reconcile 判定后再落终态。
func (h *Handler) executeApprovedAction(action *store.AIAction, approval *store.AIApprovalDecision) (contract.ActionResult, error) {
	client := currentActionExecutor()
	if client == nil {
		return contract.ActionResult{}, errors.New("executor client not configured")
	}
	if action.DryRun {
		return contract.ActionResult{}, errors.New("dry_run action must not be executed via executor")
	}
	if isTerminalExecutionStatus(action.ExecutionStatus) {
		// durable idempotency：已终止，返回已记录结果。
		var prev contract.ActionResult
		if len(action.Result) > 0 {
			_ = json.Unmarshal(action.Result, &prev)
		}
		return prev, nil
	}
	targetSpec, _ := json.Marshal(action.Params)
	ctx := contract.ActionExecutionContext{
		ActionID:        action.ActionID,
		ActionHash:      action.ActionHash,
		ApprovalID:      approval.ApprovalID,
		TargetUID:       action.TargetUID,
		TargetName:      action.TargetName,
		ResourceVersion: action.ResourceVersion,
		ClusterID:       action.ClusterID,
		Namespace:       action.Namespace,
		Operation:       action.Operation,
		TargetSpec:      targetSpec,
		CredentialRef:   "query-api:signed",
		ApprovedAt:      formatApprovedAt(approval.DecidedAt),
		ExecutedBy:      "query-api",
	}
	if err := ctx.Validate(); err != nil {
		return contract.ActionResult{}, err
	}
	if h.actionDAO == nil {
		return contract.ActionResult{}, errors.New("action persistence unavailable")
	}
	// Persist the attempt before crossing the mutation boundary. A missing or
	// duplicate attempt is a hard stop: the executor must never be called for an
	// action that cannot be reconciled after a response loss.
	attemptID := newActionAttemptID()
	startedAt := time.Now()
	if h.attemptDAO != nil {
		created, createErr := h.attemptDAO.Create(store.AIActionAttempt{
			AttemptID: attemptID, ActionID: action.ActionID, RunID: action.RunID,
			TenantID: action.TenantID, ClusterID: action.ClusterID,
			IdempotencyKey: action.IdempotencyKey, ActionHash: action.ActionHash,
			Status: "running", ExecutorID: "ai-action-executor", RequestJSON: targetSpec,
			StartedAt: &startedAt, CreatedAt: startedAt,
		})
		if createErr != nil {
			return contract.ActionResult{}, fmt.Errorf("action attempt persistence failed: %w", createErr)
		}
		if !created {
			return contract.ActionResult{}, errors.New("action attempt already exists; reconcile before retry")
		}
	}
	recordAttempt := func(status string, result []byte, errorCode string) error {
		if h.attemptDAO == nil {
			return nil
		}
		finished := time.Now()
		return h.attemptDAO.Update(attemptID, status, result, errorCode, &finished)
	}
	res, reached, err := client.Execute(ctx)
	if err != nil {
		// executor 不可达 → 持久化 execution_unknown（不盲目重试）。
		if attemptErr := recordAttempt("execution_unknown", nil, contract.ErrorCodeExecutorUnavailable); attemptErr != nil {
			return contract.ActionResult{}, attemptErr
		}
		if updateErr := h.actionDAO.UpdateExecution(action.ActionID, "execution_unknown", nil, contract.ErrorCodeExecutorUnavailable); updateErr != nil {
			return contract.ActionResult{}, updateErr
		}
		return contract.ActionResult{}, err
	}
	if !reached {
		if attemptErr := recordAttempt("execution_unknown", nil, contract.ErrorCodeExecutorUnavailable); attemptErr != nil {
			return contract.ActionResult{}, attemptErr
		}
		if updateErr := h.actionDAO.UpdateExecution(action.ActionID, "execution_unknown", nil, contract.ErrorCodeExecutorUnavailable); updateErr != nil {
			return contract.ActionResult{}, updateErr
		}
		return contract.ActionResult{ActionID: action.ActionID, Status: "execution_unknown"}, nil
	}
	resultJSON, _ := json.Marshal(res)
	switch res.Status {
	case "success":
		if attemptErr := recordAttempt("success", resultJSON, ""); attemptErr != nil {
			return res, attemptErr
		}
		if updateErr := h.actionDAO.UpdateExecution(action.ActionID, "success", resultJSON, ""); updateErr != nil {
			return res, updateErr
		}
	case "rejected":
		if attemptErr := recordAttempt("rejected", resultJSON, contract.ErrorCodeExecutorRejected); attemptErr != nil {
			return res, attemptErr
		}
		if updateErr := h.actionDAO.UpdateExecution(action.ActionID, "rejected", resultJSON, contract.ErrorCodeExecutorRejected); updateErr != nil {
			return res, updateErr
		}
	case "failed", "rollback_required":
		if attemptErr := recordAttempt(res.Status, resultJSON, res.Message); attemptErr != nil {
			return res, attemptErr
		}
		if updateErr := h.actionDAO.UpdateExecution(action.ActionID, res.Status, resultJSON, res.Message); updateErr != nil {
			return res, updateErr
		}
	case "execution_unknown":
		// reconcile-before-retry：先调 executor 判定目标实际状态，不盲目 retry。
		// Reconcile against the original immutable action specification.  The
		// result field is the executor response and cannot describe the expected
		// object state after a timeout.
		rec, _, _ := client.Reconcile(action.ActionID, action.TargetUID, string(action.Params))
		recResultJSON, _ := json.Marshal(rec)
		if attemptErr := recordAttempt("execution_unknown", recResultJSON, contract.ErrorCodeExecutionUnknown); attemptErr != nil {
			return res, attemptErr
		}
		if updateErr := h.actionDAO.UpdateExecution(action.ActionID, "execution_unknown", recResultJSON, contract.ErrorCodeExecutionUnknown); updateErr != nil {
			return res, updateErr
		}
	default:
		if attemptErr := recordAttempt("failed", resultJSON, res.Message); attemptErr != nil {
			return res, attemptErr
		}
		if updateErr := h.actionDAO.UpdateExecution(action.ActionID, "failed", resultJSON, res.Message); updateErr != nil {
			return res, updateErr
		}
	}
	return res, nil
}

func newActionAttemptID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("attempt-%d", time.Now().UnixNano())
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

func formatApprovedAt(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func isTerminalExecutionStatus(s string) bool {
	switch s {
	case "success", "failed", "rejected", "rollback_required":
		return true
	}
	return false
}
