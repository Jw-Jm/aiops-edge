package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// P10 (V9.3 Phase 10) — durable outbox dispatcher（可靠派发 RunInvocation）。
//
// query-api 公共创建 Run 后写 ai_run_outbox（pending）。dispatcher 周期扫描 pending →
// Claim（原子 + lease）→ 用 RunInvocationIssuer 签发可信 RunInvocationContext →
// POST 给 orchestrator /internal/v1/run-invocations → 200 → Deliver。
// 失败/超时保留 pending，指数退避（dispatch_count → next_retry_at）；响应丢失后
// 重试用同 invocation_id，orchestrator 侧幂等返回首次结果。
// orchestrator 长时间不可用时 Run 状态不推进（不伪装成功）。
// ─────────────────────────────────────────────────────────────────────────────

const (
	dispatchPollInterval = time.Second
	dispatchHTTPTimeout  = 10 * time.Second
	dispatchBatchSize    = 50
	// dispatchLease 是派发 in-flight 的租约时长；超过视为崩溃，ScanPending 回收重派发。
	dispatchLease = 30 * time.Second
	// systemDispatchPrincipalID 是 query-api outbox dispatcher 派发 RunInvocation 时
	// 使用的系统服务身份（P0-2：已授权 Run 的可信系统握手，不以原用户身份重授权）。
	systemDispatchPrincipalID = "f4a4b8c2-3d5e-4f6a-8b9c-0d1e2f3a4b5c"
)

// RunDispatchLoop 扫描 outbox 并派发，直到 ctx 取消。供 main 以 goroutine 启动。
func (h *Handler) RunDispatchLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if h.outboxDAO != nil && h.runDAO != nil {
			h.dispatchPending()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(dispatchPollInterval):
		}
	}
}

// dispatchPending 处理一批 pending 派发。
func (h *Handler) dispatchPending() {
	pending, err := h.outboxDAO.ScanPending(dispatchBatchSize)
	if err != nil || len(pending) == 0 {
		return
	}
	for _, o := range pending {
		h.dispatchOne(o)
	}
}

// dispatchOne 派发单条 outbox 记录（P0-1：把持久化 run_id/request_id/invocation_id 与
// 业务 body 传给 orchestrator，便于其创建/关联 Run；Claim 带 lease 防崩溃 in-flight 重复派发）。
func (h *Handler) dispatchOne(o store.AIRunOutbox) {
	claimed, err := h.outboxDAO.Claim(o.InvocationID, dispatchLease)
	if err != nil || !claimed {
		return
	}
	run, err := h.runDAO.Get(o.RunID)
	if err != nil || run == nil {
		_ = h.outboxDAO.Retry(o.InvocationID, time.Now().Add(backoff(o.DispatchCount)))
		return
	}
	issuer := currentRunInvocationIssuer()
	if issuer == nil {
		_ = h.outboxDAO.Retry(o.InvocationID, time.Now().Add(backoff(o.DispatchCount)))
		return
	}
	clusterScope := []string{}
	if run.PrimaryClusterID != "" {
		clusterScope = []string{run.PrimaryClusterID}
	}
	// P0-2：派发用 **system principal**（orchestrator 服务身份）签发 RunInvocation——Run 已在
	// query-api 公共层由原用户 ai.investigate 授权创建，这里是已授权 Run 的可信系统握手，
	// 不再以原用户身份重新授权（否则未配服务角色的用户会被 orchestrator ingress 403）。
	ctxStr, err := issuer.SignRunInvocation(
		"system", systemDispatchPrincipalID, "", run.TenantID, "run",
		clusterScope, time.Now(),
	)
	if err != nil {
		_ = h.outboxDAO.Retry(o.InvocationID, time.Now().Add(backoff(o.DispatchCount)))
		return
	}
	// 派发 body 携带持久化 Run 身份 + 业务字段，使 orchestrator 能关联已授权 Run（P0-1）。
	// original_principal_type/principal_id 保留原用户身份供 orchestrator 关联业务。
	body := map[string]interface{}{
		"invocation_id":         o.InvocationID,
		"run_id":                run.RunID,
		"request_id":            run.RequestID,
		"tenant_id":             run.TenantID,
		"cluster_id":            run.PrimaryClusterID,
		"intent":                run.Intent,
		"action_mode":           run.ActionMode,
		"principal_type":        "system",
		"principal_id":          systemDispatchPrincipalID,
		"original_principal_type": run.PrincipalType,
		"original_principal_id": run.Principal,
	}
	if err := h.postRunInvocation(ctxStr, issuer.ServiceToken(), body); err != nil {
		_ = h.outboxDAO.Retry(o.InvocationID, time.Now().Add(backoff(o.DispatchCount)))
		return
	}
	_ = h.outboxDAO.Deliver(o.InvocationID)
}

// postRunInvocation 向 orchestrator /internal/v1/run-invocations 派发（带持久化 Run body）。
func (h *Handler) postRunInvocation(trustedContext, serviceToken string, body map[string]interface{}) error {
	url := orchestratorBase() + "/internal/v1/run-invocations"
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: dispatchHTTPTimeout}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("X-Internal-Token", serviceToken)
	req.Header.Set("X-Trusted-Request-Context", trustedContext)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("dispatch to %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return &httpStatusError{code: resp.StatusCode}
	}
	return nil
}

// backoff 按派发次数计算指数退避。
func backoff(dispatchCount int64) time.Duration {
	if dispatchCount < 0 {
		dispatchCount = 0
	}
	d := time.Duration(1 << uint(dispatchCount))
	if d > 60 {
		d = 60
	}
	return d * time.Second
}

type httpStatusError struct {
	code int
}

func (e *httpStatusError) Error() string {
	return "orchestrator returned status " + strconv.Itoa(e.code)
}
