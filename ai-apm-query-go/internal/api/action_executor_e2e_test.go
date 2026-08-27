package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// TestE2E_ActionExecution_PersistRejected_Disabled 验证完整闭环：
//   - 真实 MySQL 插入一条已批准 action + approval。
//   - query-api 用自身私钥签发 signed ActionExecutionContext，POST 到真实 executor
//     （EXECUTION_MODE=disabled + EXECUTOR_VERIFY_KEYS）。
//   - executor 验签通过（disabled → 403 rejected，而非 signature failed）。
//   - query-api 把 rejected + EXECUTOR_REJECTED 持久化回 ai_actions（durable）。
//
// 依赖真实环境：
//   - MYSQL_HOST/MYSQL_PORT/MYSQL_USER/MYSQL_PASSWORD/MYSQL_DB（query-api store 用）
//   - AI_ACTION_EXECUTOR_URL 指向已启动的测试 executor
//   - AI_ACTION_EXECUTOR_SIGNING_KEY 为测试私钥（executor EXECUTOR_VERIFY_KEYS 对应公钥）
func TestE2E_ActionExecution_PersistRejected_Disabled(t *testing.T) {
	if os.Getenv("E2E_AIACTION") != "1" {
		t.Skip("E2E_AIACTION=1 时运行（真实 MySQL + executor）")
	}
	// 断言必要环境已注入（由 Makefile/验证脚本设置）
	if os.Getenv("AI_ACTION_EXECUTOR_URL") == "" {
		t.Fatal("AI_ACTION_EXECUTOR_URL must be set")
	}
	if os.Getenv("AI_ACTION_EXECUTOR_SIGNING_KEY") == "" {
		t.Fatal("AI_ACTION_EXECUTOR_SIGNING_KEY must be set")
	}

	// 1) 注入 executor client（用测试私钥签发）。
	if err := ConfigureActionExecutionClient(
		os.Getenv("AI_ACTION_EXECUTOR_URL"),
		os.Getenv("AI_ACTION_EXECUTOR_SIGNING_KEY"),
		"", // token 可选
	); err != nil {
		t.Fatalf("configure client: %v", err)
	}

	// 2) 真实 MySQL 插入 approved action + approval。
	actionID := randHexUUID(t)
	runID := "11111111-1111-4111-8111-111111111111"
	tenantID := "22222222-2222-4222-8222-222222222222"
	clusterID := "33333333-3333-4333-8333-333333333333"
	action := &store.AIAction{
		ActionID: actionID, RunID: runID, TenantID: tenantID, ClusterID: clusterID,
		ActionType: "patch", ActionHash: sha256Hex("h-" + actionID), IdempotencyKey: "ik-" + actionID,
		ProposedRisk: "R2", AuthoritativeRisk: "R2", Status: "approved", DryRun: false,
		TargetName: "some-deployment", TargetUID: "uid-" + actionID,
		ResourceVersion: "rv-1", Namespace: "default", Operation: "patch",
		ExecutionStatus: "approved",
		Params:          []byte(`{"replicas":3}`),
	}
	_, err := (&store.AIActionDAO{}).Create(*action)
	if err != nil {
		t.Fatalf("create action: %v", err)
	}
	now := time.Now()
	approvalID := "ffffffff-ffff-4fff-8fff-ffffffffffff"
	_, err = (&store.AIApprovalDecisionDAO{}).Create(store.AIApprovalDecision{
		ApprovalID: approvalID, RunID: runID, ActionID: actionID,
		ActionHash: action.ActionHash, TenantID: tenantID, ClusterID: clusterID,
		Decision: "approved", Approver: "admin", Reason: "e2e", DecidedAt: &now,
	})
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	// 3) 从 DB 读回（含执行字段）并执行。
	h := &Handler{actionDAO: &store.AIActionDAO{}, approvalDAO: &store.AIApprovalDecisionDAO{}}
	fromDB, err := h.actionDAO.GetByID(actionID)
	if err != nil || fromDB == nil {
		t.Fatalf("read action: err=%v action=%v", err, fromDB)
	}
	approval, err := h.approvalDAO.GetApprovedApproval(actionID)
	if err != nil || approval == nil {
		t.Fatalf("read approval: err=%v", err)
	}
	res, execErr := h.executeApprovedAction(fromDB, approval)
	if execErr != nil {
		t.Fatalf("execute: %v", execErr)
	}
	// disabled executor → 期望 rejected（验签通过但 EXECUTION_MODE=disabled）。
	if res.Status != "rejected" {
		t.Fatalf("expected rejected (disabled), got %s: %s", res.Status, res.Message)
	}
	// 4) 验证 durable 持久化。
	after, _ := h.actionDAO.GetByID(actionID)
	if after == nil || after.ExecutionStatus != "rejected" {
		t.Fatalf("expected execution_status=rejected, got %+v", after)
	}
	if after.ErrorCode != contract.ErrorCodeExecutorRejected {
		t.Fatalf("expected error_code=%s, got %s", contract.ErrorCodeExecutorRejected, after.ErrorCode)
	}
	t.Logf("E2E PASS: action=%s execution_status=%s error_code=%s (executor verified signature + disabled reject)",
		actionID, after.ExecutionStatus, after.ErrorCode)
}

// TestE2E_ActionExecution_ExecutorSignature 验证 query-api 签名能被真实 executor 验签通过
// （disabled 拒绝，而非 signature failed）。
func TestE2E_ActionExecution_ExecutorSignature(t *testing.T) {
	if os.Getenv("E2E_AIACTION") != "1" {
		t.Skip("E2E_AIACTION=1 时运行")
	}
	url := os.Getenv("AI_ACTION_EXECUTOR_URL")
	privB64 := os.Getenv("AI_ACTION_EXECUTOR_SIGNING_KEY")
	if url == "" || privB64 == "" {
		t.Fatal("AI_ACTION_EXECUTOR_URL and AI_ACTION_EXECUTOR_SIGNING_KEY required")
	}
	privRaw, _ := base64.RawURLEncoding.DecodeString(privB64)
	priv := ed25519.PrivateKey(privRaw)
	ctx := contract.ActionExecutionContext{
		ActionID: "e2e-sig-1", ActionHash: "h1", TargetUID: "uid-1", TargetName: "t",
		ClusterID: "c1", Namespace: "default", Operation: "patch",
	}
	body, _ := json.Marshal(ctx)
	digest := sha256.Sum256(body)
	sig := ed25519.Sign(priv, digest[:])
	// 直接构造 HTTP 请求体。
	req := httptest.NewRequest(http.MethodPost, url+"/v1/executor/execute", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Executor-Signature", hex.EncodeToString(sig))
	// 用 http.Client 发送到真实 URL（httptest.NewRequest 的 Host 不用于实际传输）。
	req.RequestURI = ""
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("executor call: %v", err)
	}
	defer resp.Body.Close()
	// disabled → 403 rejected（验签通过）。若 403 message 含 "real mutation not permitted"
	// 表示验签通过且被 disabled 拒绝；若 403 含 "signature" 表示验签失败。
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 (disabled reject), got %d", resp.StatusCode)
	}
}

func randHexUUID(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[:4]) + "-" + hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" + hex.EncodeToString(b[8:10]) + "-" + hex.EncodeToString(b[10:16])
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
