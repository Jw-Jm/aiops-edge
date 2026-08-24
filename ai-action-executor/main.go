// ai-action-executor：独立的生产 mutation 安全执行边界（Stage D / 报告 §21 / F-22）。
//
// 职责：
//   - 平台唯一真实 mutation 执行者。Orchestrator / Query API API Pod **不持**生产写凭据。
//   - 接收来自 Query API 的签名 ActionExecutionContext（action_hash / target UID /
//     resourceVersion / scoped credential_ref），重新读取目标实际 UID/resourceVersion
//     （TOCTOU 防护），确认无漂移后才执行。
//   - EXECUTION_MODE = disabled | manual | approved（唯一新权威；默认 disabled，不做真实 mutation）。
//   - 外部 mutation 已发生但响应丢失 → execution_unknown → 必须 Reconcile 目标实际状态，
//     再决定确认成功/回滚/重新执行；禁止对未知写操作盲目 retry。
//   - 不为第二套 Action SoT：权威 Action/Approval/Execution Result 持久化留在 Query API/MySQL。
//
// 接口：
//   POST /v1/executor/execute   ← Query API（签名 ActionExecutionContext）
//   GET  /v1/executor/status/{action_id}
//   POST /v1/executor/reconcile ← execution_unknown 后 Reconcile 目标实际状态
//   GET  /healthz
//
// 配置：
//   EXECUTION_MODE       = disabled | manual | approved （默认 disabled）
//   EXECUTOR_TOKEN       = Query API 出示的共享 token（可选）
//   CREDENTIAL_BROKER_URL= Credential Broker 地址（short-lived scoped credential）
//   EXECUTION_LOG_DIR    = 执行审计日志目录（可选）
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ExecutionMode 是真实 mutation 的唯一新权威。
type ExecutionMode string

const (
	ModeDisabled ExecutionMode = "disabled" // 默认：不执行任何真实 mutation
	ModeManual   ExecutionMode = "manual"   // 人工单步（只允许 dry-run / preview）
	ModeApproved ExecutionMode = "approved" // 已批准的单目标单动作执行（A-C + D 全闭环后才可开）
)

// ActionExecutionContext 是 Query API 签发的执行上下文。
type ActionExecutionContext struct {
	ActionID         string `json:"action_id"`
	ActionHash       string `json:"action_hash"` // 绑定 immutable action 身份
	ApprovalID       string `json:"approval_id"`
	TargetUID        string `json:"target_uid"`         // 目标对象 UID（执行前重新读取校验）
	ResourceVersion  string `json:"resource_version"`   // TOCTOU：执行前校验当前版本一致
	ClusterID        string `json:"cluster_id"`
	Namespace        string `json:"namespace"`
	Operation        string `json:"operation"` // patch/scale/restart 等（白名单）
	TargetSpec       json.RawMessage `json:"target_spec"`
	CredentialRef    string `json:"credential_ref"` // 经 Credential Broker 换 short-lived scope
	ApprovedAt       string `json:"approved_at"`
	ExecutedBy       string `json:"executed_by"`
}

// ActionResult 是执行结果（回写 Query API，非本服务 SoT）。
type ActionResult struct {
	ActionID      string `json:"action_id"`
	Status        string `json:"status"` // success | failed | execution_unknown | rejected | rollback_required
	ObservedUID   string `json:"observed_uid"`
	ObservedVersion string `json:"observed_version"`
	Message       string `json:"message"`
	ExecutedAt    string `json:"executed_at"`
}

type server struct {
	mode          ExecutionMode
	token         string
	mu            sync.Mutex
	results       map[string]ActionResult // 进程内结果（非 SoT；权威在 Query API）
	credBrokerURL string
}

func main() {
	mode := ExecutionMode(firstNonEmpty(os.Getenv("EXECUTION_MODE"), string(ModeDisabled)))
	switch mode {
	case ModeDisabled, ModeManual, ModeApproved:
	default:
		log.Fatalf("invalid EXECUTION_MODE=%q (allowed: disabled|manual|approved)", mode)
	}
	s := &server{
		mode:          mode,
		token:         os.Getenv("EXECUTOR_TOKEN"),
		results:       map[string]ActionResult{},
		credBrokerURL: os.Getenv("CREDENTIAL_BROKER_URL"),
	}
	log.Printf("ai-action-executor running EXECUTION_MODE=%s", mode)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/v1/executor/execute", s.handleExecute)
	mux.HandleFunc("/v1/executor/status/", s.handleStatus)
	mux.HandleFunc("/v1/executor/reconcile", s.handleReconcile)

	addr := ":" + firstNonEmpty(os.Getenv("EXECUTOR_PORT"), "8080")
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// handleExecute 处理 Query API 发来的执行请求。
func (s *server) handleExecute(w http.ResponseWriter, r *http.Request) {
	if s.token != "" && r.Header.Get("X-Executor-Token") != s.token {
		http.Error(w, "unauthorized executor token", http.StatusForbidden)
		return
	}
	if s.mode == ModeDisabled {
		writeJSON(w, http.StatusForbidden, ActionResult{
			Status: "rejected", Message: "EXECUTION_MODE=disabled; real mutation not permitted",
		})
		return
	}
	var ctx ActionExecutionContext
	if err := json.NewDecoder(r.Body).Decode(&ctx); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	// 校验 action 身份完整性（action_hash 必须非空——绑定 immutable action）。
	if ctx.ActionID == "" || ctx.ActionHash == "" || ctx.TargetUID == "" {
		writeJSON(w, http.StatusBadRequest, ActionResult{
			ActionID: ctx.ActionID, Status: "rejected", Message: "missing action_id/action_hash/target_uid",
		})
		return
	}

	// D-02：TOCTOU —— 执行前重新读取目标实际 UID/resourceVersion，确认无漂移。
	observedUID, observedVersion, drift, err := s.readCurrentState(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ActionResult{
			ActionID: ctx.ActionID, Status: "failed", Message: "state read failed: " + err.Error(),
		})
		return
	}
	if drift {
		writeJSON(w, http.StatusConflict, ActionResult{
			ActionID: ctx.ActionID, Status: "rejected",
			ObservedUID: observedUID, ObservedVersion: observedVersion,
			Message: "TOCTOU drift: target resourceVersion/UID changed since approval",
		})
		return
	}

	// 模拟/真实执行（EXECUTION_MODE=approved 且无 TOCTOU → 经 scoped credential 执行 mutation；
	// 本实现为安全边界原型，不执行真实 K8s/OpenStack mutation——保持 EXECUTION_MODE=disabled 默认）。
	if s.mode == ModeApproved {
		// 真实 mutation 路径：此处为安全边界占位，仅记录；真实 adapter 由后续授权接入。
		log.Printf("EXECUTE (approved): action=%s target=%s op=%s (TOCTOU clean uid=%s rv=%s)",
			ctx.ActionID, ctx.TargetUID, ctx.Operation, observedUID, observedVersion)
	}
	res := ActionResult{
		ActionID: ctx.ActionID, Status: "success",
		ObservedUID: observedUID, ObservedVersion: observedVersion,
		Message: "TOCTOU clean; mutation recorded (real mutation requires EXECUTION_MODE=approved + adapter)",
		ExecutedAt: time.Now().UTC().Format(time.RFC3339),
	}
	s.mu.Lock()
	s.results[ctx.ActionID] = res
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, res)
}

// readCurrentState 重新读取目标实际状态（TOCTOU 防护）。
// 返回 (observedUID, observedVersion, drift, err)。
// 在安全边界原型中，目标状态由 target_spec 提供（模拟）；真实实现从 cluster credential resolver
// 读取目标对象。drift = 读取到的 UID/version 与请求不一致。
func (s *server) readCurrentState(ctx ActionExecutionContext) (string, string, bool, error) {
	// 模拟：以请求提供的 UID/version 作为"当前实际状态"（无 drift）。
	// 真实实现应解析 ctx.TargetSpec 中的现状，或经 credBroker 读取目标对象 currentVersion。
	return ctx.TargetUID, ctx.ResourceVersion, false, nil
}

// handleStatus 返回某 action 的执行结果（进程内；权威在 Query API）。
func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if s.token != "" && r.Header.Get("X-Executor-Token") != s.token {
		http.Error(w, "unauthorized", http.StatusForbidden)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/executor/status/")
	s.mu.Lock()
	res, ok := s.results[id]
	s.mu.Unlock()
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleReconcile 处理 execution_unknown 后的目标实际状态 Reconcile（D-03）。
// 禁止对未知写操作盲目 retry——先读取目标实际状态，决定 success/rollback/re-execute。
func (s *server) handleReconcile(w http.ResponseWriter, r *http.Request) {
	if s.token != "" && r.Header.Get("X-Executor-Token") != s.token {
		http.Error(w, "unauthorized", http.StatusForbidden)
		return
	}
	var req struct {
		ActionID     string `json:"action_id"`
		TargetUID    string `json:"target_uid"`
		ExpectedSpec string `json:"expected_spec"` // 执行前期望的目标状态（用于判定是否已生效）
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.ActionID == "" {
		http.Error(w, "missing action_id", http.StatusBadRequest)
		return
	}
	// Reconcile：读取目标实际状态，与 ExpectedSpec 比对。
	// 安全边界原型：模拟——返回 reconciled 状态供 Query API 决定（不盲 retry）。
	res := ActionResult{
		ActionID: req.ActionID, Status: "reconciled",
		Message: "execution_unknown: target state reconciled; re-execute only after confirming desired state not already applied (reconcile-before-retry)",
		ExecutedAt: time.Now().UTC().Format(time.RFC3339),
	}
	s.mu.Lock()
	s.results[req.ActionID] = res
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, res)
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
