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
//
//	POST /v1/executor/execute   ← Query API（签名 ActionExecutionContext）
//	GET  /v1/executor/status/{action_id}
//	POST /v1/executor/reconcile ← execution_unknown 后 Reconcile 目标实际状态
//	GET  /healthz
//
// 配置：
//
//	EXECUTION_MODE       = disabled | manual | approved （默认 disabled）
//	EXECUTOR_TOKEN       = Query API 出示的共享 token（可选）
//	CREDENTIAL_BROKER_URL= Credential Broker 地址（short-lived scoped credential）
//	EXECUTION_LOG_DIR    = 执行审计日志目录（可选）
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
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
	ActionID        string          `json:"action_id"`
	ActionHash      string          `json:"action_hash"` // 绑定 immutable action 身份
	ApprovalID      string          `json:"approval_id"`
	TargetUID       string          `json:"target_uid"`       // 目标对象 UID（执行前重新读取校验）
	TargetName      string          `json:"target_name"`      // 目标对象 name（K8s lookup 用）
	ResourceVersion string          `json:"resource_version"` // TOCTOU：执行前校验当前版本一致
	ClusterID       string          `json:"cluster_id"`
	ResourceType    string          `json:"resource_type"` // deployment/statefulset/daemonset/pod/node
	Namespace       string          `json:"namespace"`
	Operation       string          `json:"operation"` // patch/scale/restart 等（白名单）
	TargetSpec      json.RawMessage `json:"target_spec"`
	CredentialRef   string          `json:"credential_ref"` // 经 Credential Broker 换 short-lived scope
	ApprovedAt      string          `json:"approved_at"`
	ExecutedBy      string          `json:"executed_by"`
}

// ActionResult 是执行结果（回写 Query API，非本服务 SoT）。
type ActionResult struct {
	ActionID        string `json:"action_id"`
	Status          string `json:"status"` // success | failed | execution_unknown | rejected | rollback_required
	ObservedUID     string `json:"observed_uid"`
	ObservedVersion string `json:"observed_version"`
	Message         string `json:"message"`
	ExecutedAt      string `json:"executed_at"`
}

type server struct {
	mode          ExecutionMode
	token         string
	mu            sync.Mutex
	results       map[string]ActionResult // 进程内结果（非 SoT；权威在 Query API）
	credBrokerURL string
	verifyKeyB64  string // D-Gate：query-api 签发公钥（Ed25519，base64），用于验证已签名执行上下文
	// 真实 K8s mutation（F5 已废除）：in-cluster SA token + API server（KUBERNETES_SERVICE_HOST）。
	k8sEnabled bool
	k8sToken   string
	k8sHost    string
	httpClient *http.Client
	// Function seams keep the execution state machine testable without requiring
	// a live Kubernetes API server. Production uses the methods below directly.
	readCurrentStateFn   func(ActionExecutionContext) (string, string, bool, error)
	patchTargetFn        func(ActionExecutionContext, string) error
	readReconcileStateFn func(ReconcileRequest) (reconcileObserved, error)
	// Local validation seams are nil/false by default and are populated only by
	// explicit test harness configuration. They never participate in the normal
	// production path.
	dispatchGate           chan struct{}
	dispatchGateActionID   string
	dispatchGateFile       string
	dropResponseAfterApply bool
	dropResponseActionID   string
}

// ReconcileRequest is the immutable Action context needed to decide whether
// an execution whose response was lost already took effect.
type ReconcileRequest struct {
	ActionID        string          `json:"action_id"`
	ActionHash      string          `json:"action_hash"`
	ClusterID       string          `json:"cluster_id"`
	TargetUID       string          `json:"target_uid"`
	TargetName      string          `json:"target_name"`
	ResourceVersion string          `json:"resource_version"`
	Namespace       string          `json:"namespace"`
	ResourceType    string          `json:"resource_type"`
	Operation       string          `json:"operation"`
	TargetSpec      json.RawMessage `json:"target_spec"`
}

type reconcileObserved struct {
	UID             string
	ResourceVersion string
	Replicas        int32
	Annotations     map[string]string
	Unschedulable   bool
	PodsRemaining   int
}

type k8sNotFoundError struct {
	resourceType string
	name         string
}

func (e *k8sNotFoundError) Error() string {
	return fmt.Sprintf("kubernetes %s %q not found", e.resourceType, e.name)
}

// newK8sClient 初始化 in-cluster K8s client（SA token + service host）。
// 需挂载 /var/run/secrets/kubernetes.io/serviceaccount/token 且 POD_SA_ACCESS=true。
func (s *server) newK8sClient() {
	tokenBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err == nil && len(tokenBytes) > 0 {
		host := os.Getenv("KUBERNETES_SERVICE_HOST")
		port := os.Getenv("KUBERNETES_SERVICE_PORT")
		s.k8sToken = strings.TrimSpace(string(tokenBytes))
		if host != "" {
			s.k8sHost = "https://" + host + ":" + firstNonEmpty(port, "443")
			// C-05 K8s TLS：加载 in-cluster CA（验证 API Server 证书），不做 insecureSkipVerify。
			tlsConf := &tls.Config{MinVersion: tls.VersionTLS12}
			if ca, caErr := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"); caErr == nil && len(ca) > 0 {
				pool := x509.NewCertPool()
				if pool.AppendCertsFromPEM(ca) {
					tlsConf.RootCAs = pool
				}
			}
			s.httpClient = &http.Client{
				Timeout:   15 * time.Second,
				Transport: &http.Transport{TLSClientConfig: tlsConf},
			}
			s.k8sEnabled = true
		}
	}
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
		verifyKeyB64:  os.Getenv("EXECUTOR_VERIFY_KEYS"),
	}
	// Deterministic local-validation fault injection is opt-in and cannot be
	// enabled while the executor is disabled. Production deployments leave
	// LOCAL_VALIDATION_ENABLED unset.
	if os.Getenv("LOCAL_VALIDATION_ENABLED") == "true" && mode != ModeDisabled {
		s.dispatchGateActionID = strings.TrimSpace(os.Getenv("LOCAL_VALIDATION_FAULT_ACTION_ID"))
		s.dispatchGateFile = strings.TrimSpace(os.Getenv("LOCAL_VALIDATION_DISPATCH_GATE_FILE"))
		s.dropResponseAfterApply = os.Getenv("LOCAL_VALIDATION_DROP_RESPONSE_AFTER_APPLY") == "true"
		s.dropResponseActionID = s.dispatchGateActionID
	}
	// F5 已废除：若 POD_SA_ACCESS=true 则启用真实 K8s mutation（in-cluster SA + 限定 RBAC）。
	if os.Getenv("POD_SA_ACCESS") == "true" {
		s.newK8sClient()
	}
	if err := validateExecutionConfig(s.mode, s.k8sEnabled, s.verifyKeyB64); err != nil {
		log.Fatalf("invalid execution configuration: %v", err)
	}
	log.Printf("ai-action-executor running EXECUTION_MODE=%s k8sEnabled=%v", mode, s.k8sEnabled)

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
	// D-Gate：认证必须是已签名的 ActionExecutionContext（Ed25519，query-api 签发），
	// 不能只是可选 shared token。校验签名 + action_hash 绑定 immutable Action。
	body, err := readBody(r)
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if s.verifyKeyB64 != "" {
		if err := verifySignedContext(r, body, s.verifyKeyB64); err != nil {
			writeJSON(w, http.StatusForbidden, ActionResult{
				Status: "rejected", Message: "signed execution context verification failed: " + err.Error(),
			})
			return
		}
	} else if s.token != "" && r.Header.Get("X-Executor-Token") != s.token {
		// 回退：verify key 未配置时仅允许 disabled（默认）——approved 需要签名。
		http.Error(w, "unauthorized executor token", http.StatusForbidden)
		return
	}
	if s.mode == ModeDisabled {
		writeJSON(w, http.StatusForbidden, ActionResult{
			Status: "rejected", Message: "EXECUTION_MODE=disabled; real mutation not permitted",
		})
		return
	}
	if s.mode == ModeApproved && s.verifyKeyB64 == "" {
		// D-Gate：approved 必须已配置验签公钥（不可仅靠可选 token 走真实路径）。
		writeJSON(w, http.StatusServiceUnavailable, ActionResult{
			Status: "rejected", Message: "EXECUTION_MODE=approved requires EXECUTOR_VERIFY_KEYS (signed context); not configured",
		})
		return
	}
	var ctx ActionExecutionContext
	if err := json.Unmarshal(body, &ctx); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if ctx.ResourceType == "" {
		// Legacy signed contexts defaulted to deployment. New canonical Actions
		// always carry the concrete resource type in the signed body.
		ctx.ResourceType = "deployment"
	}
	// 校验 action 身份完整性（action_hash 必须非空——绑定 immutable action）。
	if ctx.ActionID == "" || ctx.ActionHash == "" || ctx.TargetUID == "" {
		writeJSON(w, http.StatusBadRequest, ActionResult{
			ActionID: ctx.ActionID, Status: "rejected", Message: "missing action_id/action_hash/target_uid",
		})
		return
	}
	s.waitForDispatchGate(ctx.ActionID)
	if err := validateExecutionConfig(s.mode, s.k8sEnabled, s.verifyKeyB64); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, ActionResult{
			ActionID: ctx.ActionID, Status: "rejected",
			Message: "Kubernetes mutation capability unavailable: " + err.Error(),
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

	// 真实执行（F5 已废除）：EXECUTION_MODE=approved + POD_SA_ACCESS=true → 经 in-cluster SA
	// 对目标 deployment 执行受控真实 patch（白名单 operation + If-Match resourceVersion precondition）。
	// 保留 rollback 能力：patch 后记录 before/after，供 reconcile/rollback 恢复。
	execMsg := "manual mode; mutation not applied"
	execStatus := "dry_run"
	if s.mode == ModeApproved && s.k8sEnabled {
		execStatus = "success"
		if err := s.patchTarget(ctx, observedVersion); err != nil {
			// 真实写失败 → failed（不伪报 success）
			execStatus = "failed"
			execMsg = "real K8s mutation failed: " + err.Error()
			writeJSON(w, http.StatusInternalServerError, ActionResult{
				ActionID: ctx.ActionID, Status: execStatus,
				ObservedUID: observedUID, ObservedVersion: observedVersion, Message: execMsg,
			})
			return
		}
		execMsg = "real K8s mutation applied (verified): action=" + ctx.ActionID + " target=" + ctx.TargetUID +
			" op=" + ctx.Operation + " ns=" + ctx.Namespace
		log.Printf("EXECUTE (approved+real): %s (uid=%s rv=%s)", execMsg, observedUID, observedVersion)
		if s.dropResponseAfterApply && s.dropResponseActionID == ctx.ActionID {
			// The patch has already been applied. Aborting the HTTP response forces
			// Query API into execution_unknown so it must reconcile before retrying.
			panic(http.ErrAbortHandler)
		}
	}
	res := ActionResult{
		ActionID: ctx.ActionID, Status: execStatus,
		ObservedUID: observedUID, ObservedVersion: observedVersion,
		Message:    execMsg,
		ExecutedAt: time.Now().UTC().Format(time.RFC3339),
	}
	s.mu.Lock()
	s.results[ctx.ActionID] = res
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, res)
}

// waitForDispatchGate pauses one explicitly selected local-validation action.
// A channel is used by unit tests; a gate file is used by the local harness so
// an operator can change the target object before releasing dispatch.
func (s *server) waitForDispatchGate(actionID string) {
	if s.dispatchGateActionID == "" || s.dispatchGateActionID != actionID {
		return
	}
	if s.dispatchGate != nil {
		<-s.dispatchGate
		return
	}
	if s.dispatchGateFile == "" {
		return
	}
	deadline := time.Now().Add(10 * time.Minute)
	for {
		if _, err := os.Stat(s.dispatchGateFile); os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			log.Printf("local validation dispatch gate timed out for action=%s", actionID)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// readCurrentState 重新读取目标实际状态（TOCTOU 防护，真实 K8s）。
// 返回 (observedUID, observedVersion, drift, err)。drift = 读取到的 UID/version 与请求不一致。
// F5 已废除：当 k8sEnabled 时从真实 K8s API 读取目标 deployment 的当前 UID/resourceVersion。
func (s *server) readCurrentState(ctx ActionExecutionContext) (string, string, bool, error) {
	if s.readCurrentStateFn != nil {
		return s.readCurrentStateFn(ctx)
	}
	if s.k8sEnabled {
		ns := ctx.Namespace
		if ns == "" && ctx.ResourceType != "node" {
			ns = "default"
		}
		name := ctx.TargetName
		if name == "" {
			name = ctx.TargetUID
		}
		if name == "" {
			return "", "", false, errors.New("cannot resolve target name for real K8s reread")
		}
		observed, err := s.k8sReadObjectState(firstNonEmpty(ctx.ResourceType, "deployment"), ns, name)
		if err != nil {
			return "", "", false, err
		}
		// drift：UID 或 resourceVersion 与批准时不一致（TOCTOU precondition）。
		drift := observed.UID != ctx.TargetUID || (ctx.ResourceVersion != "" && observed.ResourceVersion != ctx.ResourceVersion)
		return observed.UID, observed.ResourceVersion, drift, nil
	}
	// 回退（无 K8s 访问）：模拟（无 drift）。
	return ctx.TargetUID, ctx.ResourceVersion, false, nil
}

// k8sReadDeployment 从真实 K8s API 读取 deployment 的 UID + resourceVersion。
func (s *server) k8sReadDeployment(namespace, name string) (string, string, error) {
	state, err := s.k8sReadDeploymentState(namespace, name)
	if err != nil {
		return "", "", err
	}
	return state.UID, state.ResourceVersion, nil
}

func (s *server) k8sReadDeploymentState(namespace, name string) (reconcileObserved, error) {
	return s.k8sReadObjectState("deployment", namespace, name)
}

func (s *server) k8sReadObjectState(resourceType, namespace, name string) (reconcileObserved, error) {
	if !s.k8sEnabled || s.httpClient == nil {
		return reconcileObserved{}, errors.New("k8s client not configured")
	}
	endpoint, err := k8sObjectURL(s.k8sHost, resourceType, namespace, name)
	if err != nil {
		return reconcileObserved{}, err
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return reconcileObserved{}, err
	}
	req.Header.Set("Authorization", "Bearer "+s.k8sToken)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return reconcileObserved{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return reconcileObserved{}, &k8sNotFoundError{resourceType: resourceType, name: name}
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return reconcileObserved{}, errors.New("k8s read failed: " + resp.Status + " " + string(body))
	}
	var obj struct {
		Metadata struct {
			UID             string            `json:"uid"`
			ResourceVersion string            `json:"resourceVersion"`
			Annotations     map[string]string `json:"annotations"`
		} `json:"metadata"`
		Spec struct {
			Replicas      int32 `json:"replicas"`
			Unschedulable bool  `json:"unschedulable"`
		} `json:"spec"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return reconcileObserved{}, err
	}
	return reconcileObserved{UID: obj.Metadata.UID, ResourceVersion: obj.Metadata.ResourceVersion,
		Replicas: obj.Spec.Replicas, Annotations: obj.Metadata.Annotations, Unschedulable: obj.Spec.Unschedulable}, nil
}

func (s *server) k8sReadDrainState(name string) (reconcileObserved, error) {
	state, err := s.k8sReadObjectState("node", "", name)
	if err != nil {
		return reconcileObserved{}, err
	}
	pods, err := s.listDrainPods(context.Background(), name)
	if err != nil {
		return reconcileObserved{}, err
	}
	state.PodsRemaining = len(pods)
	return state, nil
}

func k8sObjectURL(host, resourceType, namespace, name string) (string, error) {
	resourceType = strings.ToLower(strings.TrimSpace(resourceType))
	if name == "" {
		return "", errors.New("target name is empty")
	}
	switch resourceType {
	case "deployment", "statefulset", "daemonset":
		if namespace == "" {
			return "", errors.New("namespace is required for workload")
		}
		return host + "/apis/apps/v1/namespaces/" + url.PathEscape(namespace) + "/" + resourceType + "s/" + url.PathEscape(name), nil
	case "pod":
		if namespace == "" {
			return "", errors.New("namespace is required for pod")
		}
		return host + "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods/" + url.PathEscape(name), nil
	case "node":
		if namespace != "" {
			return "", errors.New("node must not include a namespace")
		}
		return host + "/api/v1/nodes/" + url.PathEscape(name), nil
	default:
		return "", errors.New("unsupported resource type: " + resourceType)
	}
}

// patchTarget 按 canonical resource_type/operation 路由到受控 Kubernetes API。
func (s *server) patchTarget(ctx ActionExecutionContext, observedVersion string) error {
	if s.patchTargetFn != nil {
		return s.patchTargetFn(ctx, observedVersion)
	}
	if !s.k8sEnabled || s.httpClient == nil {
		return errors.New("k8s client not configured")
	}
	resourceType := strings.ToLower(strings.TrimSpace(firstNonEmpty(ctx.ResourceType, "deployment")))
	ns := ctx.Namespace
	if ns == "" && resourceType != "node" {
		ns = "default"
	}
	name := ctx.TargetName
	if name == "" {
		name = ctx.TargetUID
	}
	if name == "" {
		return errors.New("cannot resolve target name")
	}
	if ctx.Operation == "delete_pod" {
		return s.deletePod(ctx, ns, name, observedVersion)
	}
	if ctx.Operation == "evict_pod" {
		return s.evictPod(ctx, ns, name, observedVersion)
	}
	if ctx.Operation == "drain" {
		return s.drainNode(ctx, observedVersion, name)
	}
	// 仅允许白名单操作（workload annotation/scale、node cordon），防任意 mutation。
	payload, err := buildPatchPayload(ctx)
	if err != nil {
		return err
	}
	payload, err = addResourceVersionPrecondition(payload, observedVersion)
	if err != nil {
		return err
	}
	endpoint, err := k8sObjectURL(s.k8sHost, resourceType, ns, name)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPatch, endpoint, strings.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.k8sToken)
	req.Header.Set("Content-Type", "application/strategic-merge-patch+json")
	// 乐观并发：resourceVersion precondition（TOCTOU）。
	if observedVersion != "" {
		req.Header.Set("If-Match", `"`+observedVersion+`"`)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return errors.New("k8s patch failed: " + resp.Status + " " + string(body))
	}
	return nil
}

type k8sDeletePreconditions struct {
	UID             string `json:"uid,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

type k8sDeleteOptions struct {
	GracePeriodSeconds int64                   `json:"gracePeriodSeconds"`
	Preconditions      *k8sDeletePreconditions `json:"preconditions,omitempty"`
}

func deleteOptionsPayload(ctx ActionExecutionContext, grace, observedVersion int64) (string, error) {
	options := k8sDeleteOptions{GracePeriodSeconds: grace}
	if ctx.TargetUID != "" || observedVersion > 0 {
		options.Preconditions = &k8sDeletePreconditions{UID: ctx.TargetUID}
		if observedVersion > 0 {
			options.Preconditions.ResourceVersion = fmt.Sprintf("%d", observedVersion)
		}
	}
	payload, err := json.Marshal(options)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func (s *server) deletePod(ctx ActionExecutionContext, namespace, name, observedVersion string) error {
	grace, err := buildGracePeriod(ctx)
	if err != nil {
		return err
	}
	if strings.ToLower(strings.TrimSpace(ctx.ResourceType)) != "pod" {
		return errors.New("delete_pod requires pod")
	}
	endpoint, err := k8sObjectURL(s.k8sHost, "pod", namespace, name)
	if err != nil {
		return err
	}
	version := int64(0)
	if observedVersion != "" {
		version, err = parseResourceVersion(observedVersion)
		if err != nil {
			return err
		}
	}
	payload, err := deleteOptionsPayload(ctx, grace, version)
	if err != nil {
		return err
	}
	return s.doK8sMutation(http.MethodDelete, endpoint, string(payload), "application/json", "")
}

func (s *server) evictPod(ctx ActionExecutionContext, namespace, name, observedVersion string) error {
	grace, err := buildGracePeriod(ctx)
	if err != nil {
		return err
	}
	if strings.ToLower(strings.TrimSpace(ctx.ResourceType)) != "pod" {
		return errors.New("evict_pod requires pod")
	}
	if namespace == "" {
		return errors.New("namespace is required for pod eviction")
	}
	endpoint := s.k8sHost + "/apis/policy/v1/namespaces/" + url.PathEscape(namespace) + "/pods/" + url.PathEscape(name) + "/eviction"
	version := int64(0)
	if observedVersion != "" {
		version, err = parseResourceVersion(observedVersion)
		if err != nil {
			return err
		}
	}
	options, err := deleteOptionsPayload(ctx, grace, version)
	if err != nil {
		return err
	}
	payload := `{"apiVersion":"policy/v1","deleteOptions":` + options + `,"kind":"Eviction","metadata":{"name":` + strconv.Quote(name) + `,"namespace":` + strconv.Quote(namespace) + `}}`
	return s.doK8sMutation(http.MethodPost, endpoint, string(payload), "application/json", "")
}

func (s *server) drainNode(ctx ActionExecutionContext, observedVersion, name string) error {
	if strings.ToLower(strings.TrimSpace(ctx.ResourceType)) != "node" {
		return errors.New("drain requires node")
	}
	timeout, err := buildDrainTimeout(ctx)
	if err != nil {
		return err
	}
	requestContext, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()
	pods, err := s.listDrainPods(requestContext, name)
	if err != nil {
		return err
	}
	patchPayload := `{"spec":{"unschedulable":true}}`
	patchPayload, err = addResourceVersionPrecondition(patchPayload, observedVersion)
	if err != nil {
		return err
	}
	endpoint, err := k8sObjectURL(s.k8sHost, "node", "", name)
	if err != nil {
		return err
	}
	if err := s.doK8sMutationWithContext(requestContext, http.MethodPatch, endpoint, patchPayload, "application/strategic-merge-patch+json", observedVersion); err != nil {
		return err
	}
	for _, pod := range pods {
		podCtx := ActionExecutionContext{ResourceType: "pod", Namespace: pod.Namespace, TargetName: pod.Name, TargetUID: pod.UID, ResourceVersion: pod.ResourceVersion, Operation: "evict_pod", TargetSpec: json.RawMessage(`{"grace_period_seconds":30}`)}
		if err := s.evictPodWithContext(requestContext, podCtx); err != nil {
			return fmt.Errorf("drain pod %s/%s: %w", pod.Namespace, pod.Name, err)
		}
	}
	return nil
}

type drainPod struct {
	Name            string
	Namespace       string
	UID             string
	ResourceVersion string
}

func (s *server) listDrainPods(ctx context.Context, nodeName string) ([]drainPod, error) {
	if nodeName == "" {
		return nil, errors.New("node name is empty")
	}
	values := url.Values{}
	values.Set("fieldSelector", "spec.nodeName="+nodeName)
	endpoint := s.k8sHost + "/api/v1/pods?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.k8sToken)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, errors.New("k8s pod list failed: " + resp.Status + " " + string(body))
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name            string            `json:"name"`
				Namespace       string            `json:"namespace"`
				UID             string            `json:"uid"`
				ResourceVersion string            `json:"resourceVersion"`
				Annotations     map[string]string `json:"annotations"`
				OwnerReferences []struct {
					Kind string `json:"kind"`
				} `json:"ownerReferences"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	pods := make([]drainPod, 0, len(list.Items))
	for _, item := range list.Items {
		if item.Metadata.Annotations["kubernetes.io/config.mirror"] != "" {
			continue
		}
		isDaemonSet := false
		for _, owner := range item.Metadata.OwnerReferences {
			if owner.Kind == "DaemonSet" {
				isDaemonSet = true
				break
			}
		}
		if !isDaemonSet && item.Metadata.Name != "" && item.Metadata.Namespace != "" {
			pods = append(pods, drainPod{Name: item.Metadata.Name, Namespace: item.Metadata.Namespace, UID: item.Metadata.UID, ResourceVersion: item.Metadata.ResourceVersion})
		}
	}
	return pods, nil
}

func (s *server) evictPodWithContext(requestContext context.Context, ctx ActionExecutionContext) error {
	grace, err := buildGracePeriod(ctx)
	if err != nil {
		return err
	}
	endpoint, err := k8sObjectURL(s.k8sHost, "pod", ctx.Namespace, ctx.TargetName)
	if err != nil {
		return err
	}
	endpoint += "/eviction"
	// Replace the core API path with the policy eviction subresource.
	endpoint = strings.Replace(endpoint, "/api/v1/", "/apis/policy/v1/", 1)
	version := int64(0)
	if ctx.ResourceVersion != "" {
		version, err = parseResourceVersion(ctx.ResourceVersion)
		if err != nil {
			return err
		}
	}
	options, err := deleteOptionsPayload(ctx, grace, version)
	if err != nil {
		return err
	}
	payload := `{"apiVersion":"policy/v1","deleteOptions":` + options + `,"kind":"Eviction","metadata":{"name":` + strconv.Quote(ctx.TargetName) + `,"namespace":` + strconv.Quote(ctx.Namespace) + `}}`
	return s.doK8sMutationWithContext(requestContext, http.MethodPost, endpoint, string(payload), "application/json", "")
}

func (s *server) doK8sMutation(method, endpoint, payload, contentType, observedVersion string) error {
	return s.doK8sMutationWithContext(context.Background(), method, endpoint, payload, contentType, observedVersion)
}

func (s *server) doK8sMutationWithContext(ctx context.Context, method, endpoint, payload, contentType, observedVersion string) error {
	if s.httpClient == nil {
		return errors.New("k8s client not configured")
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, strings.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.k8sToken)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if observedVersion != "" {
		req.Header.Set("If-Match", `"`+observedVersion+`"`)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return errors.New("k8s mutation failed: " + resp.Status + " " + string(body))
	}
	return nil
}

func buildGracePeriod(ctx ActionExecutionContext) (int64, error) {
	if strings.ToLower(strings.TrimSpace(ctx.Operation)) != "delete_pod" && strings.ToLower(strings.TrimSpace(ctx.Operation)) != "evict_pod" {
		return 0, errors.New("pod deletion requires delete_pod or evict_pod")
	}
	var params struct {
		GracePeriodSeconds *int64 `json:"grace_period_seconds"`
	}
	if err := decodeStrictObject(ctx.TargetSpec, &params); err != nil || params.GracePeriodSeconds == nil || *params.GracePeriodSeconds < 0 || *params.GracePeriodSeconds > 600 {
		return 0, errors.New("grace_period_seconds must be an integer between 0 and 600")
	}
	return *params.GracePeriodSeconds, nil
}

func buildDrainTimeout(ctx ActionExecutionContext) (int64, error) {
	if strings.ToLower(strings.TrimSpace(ctx.ResourceType)) != "node" || strings.ToLower(strings.TrimSpace(ctx.Operation)) != "drain" {
		return 0, errors.New("drain requires node")
	}
	var params struct {
		DrainTimeout *int64 `json:"drain_timeout"`
	}
	if err := decodeStrictObject(ctx.TargetSpec, &params); err != nil || params.DrainTimeout == nil || *params.DrainTimeout < 1 || *params.DrainTimeout > 3600 {
		return 0, errors.New("drain_timeout must be an integer between 1 and 3600")
	}
	return *params.DrainTimeout, nil
}

func decodeStrictObject(raw json.RawMessage, target interface{}) error {
	if len(raw) == 0 {
		return errors.New("missing target spec")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("target spec must contain one JSON value")
	}
	return nil
}

func parseResourceVersion(raw string) (int64, error) {
	version, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || version < 0 {
		return 0, errors.New("resourceVersion must be a non-negative integer")
	}
	return version, nil
}

func addResourceVersionPrecondition(payload, resourceVersion string) (string, error) {
	if strings.TrimSpace(resourceVersion) == "" {
		return payload, nil
	}
	if _, err := parseResourceVersion(resourceVersion); err != nil {
		return "", err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &object); err != nil {
		return "", err
	}
	metadata := map[string]json.RawMessage{}
	if raw := object["metadata"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &metadata); err != nil {
			return "", err
		}
	}
	metadata["resourceVersion"] = json.RawMessage(strconv.Quote(strings.TrimSpace(resourceVersion)))
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	object["metadata"] = metadataJSON
	result, err := json.Marshal(object)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

// validateExecutionConfig prevents an approved request from entering a path
// that cannot perform a Kubernetes mutation. The process fails at startup in
// production, and the handler repeats the check to keep tests and misconfigured
// embeddings fail-closed as well.
func validateExecutionConfig(mode ExecutionMode, k8sEnabled bool, verifyKeyB64 string) error {
	if mode != ModeApproved {
		return nil
	}
	if strings.TrimSpace(verifyKeyB64) == "" {
		return errors.New("EXECUTION_MODE=approved requires EXECUTOR_VERIFY_KEYS")
	}
	if !k8sEnabled {
		return errors.New("EXECUTION_MODE=approved requires a Kubernetes mutation client")
	}
	return nil
}

// buildPatchPayload 根据 operation 构造受控 patch（annotation / replicas 白名单）。
func buildPatchPayload(ctx ActionExecutionContext) (string, error) {
	switch ctx.Operation {
	case "patch", "rollout_restart":
		if ctx.Operation == "rollout_restart" && (len(ctx.TargetSpec) == 0 || string(ctx.TargetSpec) == "{}") {
			return `{"metadata":{"annotations":{"aiops.observability.io/restartedAt":"requested"}}}`, nil
		}
		if len(ctx.TargetSpec) == 0 || string(ctx.TargetSpec) == "null" {
			// Keep manually constructed test/preview contexts safe and useful; all
			// production candidates pass preflight and therefore carry an explicit
			// allow-listed annotation payload.
			return `{"metadata":{"annotations":{"aio-action-executor/verified":"true"}}}`, nil
		}
		var spec struct {
			Metadata struct {
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(ctx.TargetSpec, &spec); err != nil || len(spec.Metadata.Annotations) == 0 {
			return "", errors.New("patch target_spec must contain metadata.annotations")
		}
		for key, value := range spec.Metadata.Annotations {
			if (!strings.HasPrefix(key, "aiops.observability.io/") && !strings.HasPrefix(key, "aio-action-executor/")) || len(value) > 256 {
				return "", errors.New("patch target_spec contains an annotation outside the allowlist")
			}
		}
		payload, err := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": spec.Metadata.Annotations}})
		if err != nil {
			return "", err
		}
		return string(payload), nil
	case "scale":
		if ctx.ResourceType != "" && ctx.ResourceType != "deployment" && ctx.ResourceType != "statefulset" {
			return "", errors.New("scale requires deployment or statefulset")
		}
		var spec struct {
			Replicas *int32 `json:"replicas"`
		}
		if len(ctx.TargetSpec) > 0 {
			_ = json.Unmarshal(ctx.TargetSpec, &spec)
		}
		r := int32(1)
		if spec.Replicas != nil {
			r = *spec.Replicas
		}
		return fmt.Sprintf(`{"spec":{"replicas":%d}}`, r), nil
	case "cordon":
		if ctx.ResourceType != "" && ctx.ResourceType != "node" {
			return "", errors.New("cordon requires node")
		}
		return `{"spec":{"unschedulable":true}}`, nil
	case "uncordon":
		if ctx.ResourceType != "" && ctx.ResourceType != "node" {
			return "", errors.New("uncordon requires node")
		}
		return `{"spec":{"unschedulable":false}}`, nil
	default:
		return "", errors.New("unsupported operation: " + ctx.Operation)
	}
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
	body, err := readBody(r)
	if err != nil {
		http.Error(w, "bad reconcile body", http.StatusBadRequest)
		return
	}
	if s.verifyKeyB64 != "" {
		if err := verifySignedContext(r, body, s.verifyKeyB64); err != nil {
			writeJSON(w, http.StatusForbidden, ActionResult{Status: "rejected", Message: "signed reconciliation context verification failed: " + err.Error()})
			return
		}
	} else if s.token != "" && r.Header.Get("X-Executor-Token") != s.token {
		http.Error(w, "unauthorized", http.StatusForbidden)
		return
	}
	var req ReconcileRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad reconcile body", http.StatusBadRequest)
		return
	}
	if req.ActionID == "" || req.ActionHash == "" || req.TargetUID == "" || req.TargetName == "" || req.Operation == "" || len(req.TargetSpec) == 0 {
		http.Error(w, "missing action context", http.StatusBadRequest)
		return
	}
	var observed reconcileObserved
	if s.readReconcileStateFn != nil {
		observed, err = s.readReconcileStateFn(req)
	} else if s.k8sEnabled {
		resourceType := firstNonEmpty(strings.ToLower(strings.TrimSpace(req.ResourceType)), "deployment")
		if resourceType == "node" && req.Operation == "drain" {
			observed, err = s.k8sReadDrainState(req.TargetName)
		} else {
			ns := req.Namespace
			if resourceType != "node" {
				ns = firstNonEmpty(ns, "default")
			}
			observed, err = s.k8sReadObjectState(resourceType, ns, req.TargetName)
		}
	} else {
		writeJSON(w, http.StatusServiceUnavailable, ActionResult{ActionID: req.ActionID, Status: "execution_unknown",
			Message: "reconciliation requires a Kubernetes read capability"})
		return
	}
	if err != nil {
		var notFound *k8sNotFoundError
		if errors.As(err, &notFound) && (req.Operation == "delete_pod" || req.Operation == "evict_pod") {
			res := ActionResult{ActionID: req.ActionID, Status: "applied",
				Message:    "target pod is absent; the approved pod deletion is already reflected, no retry was issued",
				ExecutedAt: time.Now().UTC().Format(time.RFC3339)}
			s.mu.Lock()
			s.results[req.ActionID] = res
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, res)
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, ActionResult{ActionID: req.ActionID, Status: "execution_unknown",
			Message: "reconciliation state read failed: " + err.Error()})
		return
	}
	if observed.UID != req.TargetUID {
		writeJSON(w, http.StatusConflict, ActionResult{ActionID: req.ActionID, Status: "drift",
			ObservedUID: observed.UID, ObservedVersion: observed.ResourceVersion,
			Message: "reconciliation target UID drifted; no retry is permitted"})
		return
	}
	applied, err := desiredStateMatches(req, observed)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, ActionResult{ActionID: req.ActionID, Status: "execution_unknown",
			ObservedUID: observed.UID, ObservedVersion: observed.ResourceVersion, Message: err.Error()})
		return
	}
	status := "not_applied"
	message := "target state does not prove the requested mutation; operator review is required before retry"
	if applied {
		status = "applied"
		message = "target state already matches the approved Action; no retry was issued"
	}
	res := ActionResult{
		ActionID: req.ActionID, Status: status, ObservedUID: observed.UID, ObservedVersion: observed.ResourceVersion,
		Message:    message,
		ExecutedAt: time.Now().UTC().Format(time.RFC3339),
	}
	s.mu.Lock()
	s.results[req.ActionID] = res
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, res)
}

func desiredStateMatches(req ReconcileRequest, observed reconcileObserved) (bool, error) {
	switch req.Operation {
	case "scale":
		if req.ResourceType != "" && req.ResourceType != "deployment" && req.ResourceType != "statefulset" {
			return false, errors.New("scale reconciliation requires deployment or statefulset")
		}
		var desired struct {
			Replicas *int32 `json:"replicas"`
		}
		if err := json.Unmarshal(req.TargetSpec, &desired); err != nil || desired.Replicas == nil {
			return false, errors.New("scale reconciliation requires target_spec.replicas")
		}
		return observed.Replicas == *desired.Replicas, nil
	case "patch", "rollout_restart":
		if req.Operation == "rollout_restart" && req.ResourceType != "" && req.ResourceType != "deployment" && req.ResourceType != "statefulset" && req.ResourceType != "daemonset" {
			return false, errors.New("rollout_restart reconciliation requires a workload")
		}
		var desired struct {
			Metadata struct {
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(req.TargetSpec, &desired); err != nil || len(desired.Metadata.Annotations) == 0 {
			return false, errors.New("patch reconciliation requires target_spec.metadata.annotations")
		}
		for key, value := range desired.Metadata.Annotations {
			if observed.Annotations == nil || observed.Annotations[key] != value {
				return false, nil
			}
		}
		return true, nil
	case "cordon":
		if req.ResourceType != "" && req.ResourceType != "node" {
			return false, errors.New("cordon reconciliation requires node")
		}
		return observed.Unschedulable, nil
	case "uncordon":
		if req.ResourceType != "" && req.ResourceType != "node" {
			return false, errors.New("uncordon reconciliation requires node")
		}
		return !observed.Unschedulable, nil
	case "drain":
		if req.ResourceType != "" && req.ResourceType != "node" {
			return false, errors.New("drain reconciliation requires node")
		}
		return observed.Unschedulable && observed.PodsRemaining == 0, nil
	case "delete_pod", "evict_pod":
		if req.ResourceType != "" && req.ResourceType != "pod" {
			return false, errors.New("pod deletion reconciliation requires pod")
		}
		return false, nil
	default:
		return false, errors.New("unsupported reconciliation operation: " + req.Operation)
	}
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

// readBody 读取并限制请求体大小（防超大 body）。
func readBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, errors.New("empty body")
	}
	return body, nil
}

// verifySignedContext 验证 ActionExecutionContext 的 Ed25519 签名（D-Gate signed context）。
// 签名 = Ed25519(header("X-Executor-Signature", hex), body)；公钥来自 EXECUTOR_VERIFY_KEYS。
func verifySignedContext(r *http.Request, body []byte, verifyKeyB64 string) error {
	sigHex := r.Header.Get("X-Executor-Signature")
	if sigHex == "" {
		return errors.New("missing X-Executor-Signature")
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return errors.New("invalid signature encoding")
	}
	pubRaw, err := base64.RawURLEncoding.DecodeString(verifyKeyB64)
	if err != nil {
		return errors.New("invalid verify key")
	}
	pub := ed25519.PublicKey(pubRaw)
	// 签名覆盖 body 的 SHA256（绑定完整 execution context，防篡改）。
	digest := sha256.Sum256(body)
	if !ed25519.Verify(pub, digest[:], sig) {
		return errors.New("Ed25519 signature invalid")
	}
	return nil
}
