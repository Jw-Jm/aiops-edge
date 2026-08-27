// Package contract contains strict internal JSON contracts shared by the
// query-api boundary and the Agentic AIOps control plane (V9.2).
//
// This is the single Go contract mainline. It mirrors
// ai-orchestrator/contracts.py and observability-frontend/src/api/contracts.ts.
package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

var canonicalUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// DecodeStrict rejects unknown JSON fields so security-sensitive context
// cannot silently gain caller-controlled authorization claims.
func DecodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if validator, ok := target.(interface{ Validate() error }); ok {
		if err := validator.Validate(); err != nil {
			return err
		}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("contract payload contains trailing JSON")
		}
		return err
	}
	return nil
}

func validateUUID(field, value string) error {
	if strings.ToLower(value) != value || !canonicalUUID.MatchString(value) {
		return fmt.Errorf("%s must be a lowercase canonical UUID", field)
	}
	return nil
}

func validateOptionalUUID(field, value string) error {
	if value == "" {
		return nil
	}
	return validateUUID(field, value)
}

// commonContext holds the shared context claims (V9.2 §11).
type commonContext struct {
	Version       int       `json:"version"`
	ContextType   string    `json:"context_type"`
	Issuer        string    `json:"issuer"`
	Audience      string    `json:"audience"`
	RequestID     string    `json:"request_id"`
	PrincipalType string    `json:"principal_type"`
	PrincipalID   string    `json:"principal_id"`
	SessionID     string    `json:"session_id"`
	TenantID      string    `json:"tenant_id"`
	IssuedAt      time.Time `json:"issued_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	Nonce         string    `json:"nonce"`
}

func (c commonContext) validateCommon() error {
	if c.Version < 1 {
		return errors.New("version must be positive")
	}
	if c.PrincipalType != "user" && c.PrincipalType != "system" {
		return errors.New("principal_type must be user or system")
	}
	if err := validateUUID("principal_id", c.PrincipalID); err != nil {
		return err
	}
	if err := validateOptionalUUID("session_id", c.SessionID); err != nil {
		return err
	}
	if c.PrincipalType == "user" && c.SessionID == "" {
		return errors.New("user principal requires session_id")
	}
	if c.PrincipalType == "system" && c.SessionID != "" {
		return errors.New("system principal must have null session_id")
	}
	if err := validateUUID("tenant_id", c.TenantID); err != nil {
		return err
	}
	if err := validateUUID("nonce", c.Nonce); err != nil {
		return err
	}
	if c.Issuer == "" || c.Audience == "" {
		return errors.New("issuer and audience are required")
	}
	if c.IssuedAt.IsZero() || c.ExpiresAt.IsZero() {
		return errors.New("issued_at and expires_at are required")
	}
	lifetime := c.ExpiresAt.UTC().Sub(c.IssuedAt.UTC())
	if lifetime <= 0 || lifetime > 60*time.Second {
		return errors.New("context lifetime must be between 1 and 60 seconds")
	}
	return nil
}

// RunInvocationContext: query-api → orchestrator, to create a new Run (V9.2 §11.1).
//
// P19.6: Capability is a service-side authorized capability carried in the signed
// context so the orchestrator can distinguish 对话型 (ai.chat) from 调查型
// (ai.investigate) at the trusted ingress without trusting any client-provided
// claim. Empty/absent defaults to "ai.investigate" for backward compatibility with
// the investigation chain.
type RunInvocationContext struct {
	commonContext
	Source       string   `json:"source"`
	ClusterScope []string `json:"cluster_scope"`
	Capability   string   `json:"capability,omitempty"`
	// RunID/InvocationID bind an investigation invocation to the already
	// persisted control-plane Run. They are intentionally absent for chat.
	RunID        string `json:"run_id,omitempty"`
	InvocationID string `json:"invocation_id,omitempty"`
}

// NewRunInvocationContext builds a RunInvocationContext from its explicit fields.
func NewRunInvocationContext(issuer, audience, requestID, principalType, principalID, sessionID, tenantID, source string, clusterScope []string, issuedAt, expiresAt time.Time, nonce string) RunInvocationContext {
	return RunInvocationContext{
		commonContext: commonContext{
			Version: 1, ContextType: "run_invocation", Issuer: issuer, Audience: audience,
			RequestID: requestID, PrincipalType: principalType, PrincipalID: principalID,
			SessionID: sessionID, TenantID: tenantID, IssuedAt: issuedAt, ExpiresAt: expiresAt, Nonce: nonce,
		},
		Source: source, ClusterScope: clusterScope,
	}
}

func (c RunInvocationContext) Validate() error {
	if err := c.validateCommon(); err != nil {
		return err
	}
	if c.ContextType != "run_invocation" {
		return errors.New("context_type must be run_invocation")
	}
	if c.Source == "" {
		return errors.New("source is required")
	}
	if len(c.ClusterScope) == 0 {
		return errors.New("cluster_scope must not be empty")
	}
	for i, id := range c.ClusterScope {
		if err := validateUUID(fmt.Sprintf("cluster_scope[%d]", i), id); err != nil {
			return err
		}
	}
	if c.Capability != "" && c.Capability != "ai.chat" && c.Capability != "ai.investigate" {
		return errors.New("capability must be one of ai.chat | ai.investigate")
	}
	if c.Capability == "ai.investigate" {
		if err := validateUUID("run_id", c.RunID); err != nil {
			return err
		}
		if err := validateUUID("invocation_id", c.InvocationID); err != nil {
			return err
		}
	}
	if c.Capability == "ai.chat" && (c.RunID != "" || c.InvocationID != "") {
		return errors.New("chat invocation must not carry run identity")
	}
	return nil
}

// RunControlContext: query-api → orchestrator, to control an existing Run (V9.2 §11.2).
type RunControlContext struct {
	commonContext
	RunID      string `json:"run_id"`
	Operation  string `json:"operation"`
	ActionID   string `json:"action_id"`
	DecisionID string `json:"decision_id"`
}

func (c RunControlContext) Validate() error {
	if err := c.validateCommon(); err != nil {
		return err
	}
	if c.ContextType != "run_control" {
		return errors.New("context_type must be run_control")
	}
	if err := validateUUID("run_id", c.RunID); err != nil {
		return err
	}
	switch c.Operation {
	case "cancel", "stream", "action_decision":
	default:
		return errors.New("operation must be cancel, stream, or action_decision")
	}
	return validateOptionalUUID("action_id", c.ActionID)
}

// NewRunControlContext builds a RunControlContext from its explicit fields.
func NewRunControlContext(issuer, audience, requestID, principalType, principalID, sessionID, tenantID, runID, operation string, issuedAt, expiresAt time.Time, nonce string) RunControlContext {
	return RunControlContext{
		commonContext: commonContext{
			Version: 1, ContextType: "run_control", Issuer: issuer, Audience: audience,
			RequestID: requestID, PrincipalType: principalType, PrincipalID: principalID,
			SessionID: sessionID, TenantID: tenantID, IssuedAt: issuedAt, ExpiresAt: expiresAt, Nonce: nonce,
		},
		RunID: runID, Operation: operation,
	}
}

// TrustedRequestContext: orchestrator → query-api, for tool/data access (V9.2 §11.3).
type TrustedRequestContext struct {
	commonContext
	RunID        string `json:"run_id"`
	ScopeKind    string `json:"scope_kind"`
	ClusterID    string `json:"cluster_id"`
	Capability   string `json:"capability"`
	Source       string `json:"source"`
	WorkloadKind string `json:"workload_kind,omitempty"`
}

func (c TrustedRequestContext) Validate() error {
	if err := c.validateCommon(); err != nil {
		return err
	}
	if c.ContextType != "trusted_request" {
		return errors.New("context_type must be trusted_request")
	}
	if err := validateUUID("run_id", c.RunID); err != nil {
		return err
	}
	if c.ScopeKind != "cluster" && c.ScopeKind != "run" {
		return errors.New("scope_kind must be cluster or run")
	}
	if c.ScopeKind == "cluster" {
		if c.ClusterID == "" {
			return errors.New("cluster scope requires cluster_id")
		}
		if err := validateUUID("cluster_id", c.ClusterID); err != nil {
			return err
		}
	} else { // run scope
		if c.ClusterID != "" {
			return errors.New("run scope must have null cluster_id")
		}
		if !strings.HasPrefix(c.Capability, "control_plane.") {
			return errors.New("run scope only allows control_plane.* capability")
		}
	}
	if c.Source == "" {
		return errors.New("source is required")
	}
	// Empty is retained only for pre-convergence signed callers. New issuers
	// must populate one of the three bounded workload kinds.
	if c.WorkloadKind != "" && c.WorkloadKind != "investigation" && c.WorkloadKind != "chat" && c.WorkloadKind != "platform" {
		return errors.New("workload_kind must be investigation, chat, or platform")
	}
	return nil
}

// NewTrustedRequestContext builds a TrustedRequestContext from its explicit fields.
func NewTrustedRequestContext(issuer, audience, requestID, principalType, principalID, sessionID, tenantID, runID, scopeKind, clusterID, capability, source string, issuedAt, expiresAt time.Time, nonce string) TrustedRequestContext {
	return TrustedRequestContext{
		commonContext: commonContext{
			Version: 1, ContextType: "trusted_request", Issuer: issuer, Audience: audience,
			RequestID: requestID, PrincipalType: principalType, PrincipalID: principalID,
			SessionID: sessionID, TenantID: tenantID, IssuedAt: issuedAt, ExpiresAt: expiresAt, Nonce: nonce,
		},
		RunID: runID, ScopeKind: scopeKind, ClusterID: clusterID, Capability: capability, Source: source,
	}
}

// RequestContext is the DEPRECATED legacy single-context compatibility type.
// It is not a target contract, must not gain new callers, and is removed after
// Phase 3 production callers switch to the three V9.2 contexts.
type RequestContext struct {
	Version    int       `json:"version"`
	Issuer     string    `json:"issuer"`
	Audience   string    `json:"audience"`
	RequestID  string    `json:"request_id"`
	RunID      string    `json:"run_id"`
	UserID     string    `json:"user_id"`
	SessionID  string    `json:"session_id"`
	TenantID   string    `json:"tenant_id"`
	ClusterID  string    `json:"cluster_id"`
	Source     string    `json:"source"`
	Capability string    `json:"capability"`
	IssuedAt   time.Time `json:"issued_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Nonce      string    `json:"nonce"`
}

func (context RequestContext) Validate() error {
	if context.Version < 1 {
		return errors.New("version must be positive")
	}
	for field, value := range map[string]string{
		"request_id": context.RequestID,
		"run_id":     context.RunID,
		"user_id":    context.UserID,
		"session_id": context.SessionID,
		"tenant_id":  context.TenantID,
		"cluster_id": context.ClusterID,
		"nonce":      context.Nonce,
	} {
		if err := validateUUID(field, value); err != nil {
			return err
		}
	}
	if context.Issuer == "" || context.Audience == "" || context.Source == "" || context.Capability == "" {
		return errors.New("issuer, audience, source and capability are required")
	}
	if context.IssuedAt.IsZero() || context.ExpiresAt.IsZero() {
		return errors.New("issued_at and expires_at are required")
	}
	lifetime := context.ExpiresAt.UTC().Sub(context.IssuedAt.UTC())
	if lifetime <= 0 || lifetime > 60*time.Second {
		return errors.New("legacy RequestContext lifetime must be between 1 and 60 seconds")
	}
	return nil
}

// ResourceRef — canonical resource_id does NOT include tenant_id (V9.2 §10).
// tenant_id is an ownership/isolation dimension, stored separately.
type ResourceRef struct {
	ClusterID    string  `json:"cluster_id"`
	ResourceType string  `json:"resource_type"`
	Namespace    *string `json:"namespace"`
	Name         string  `json:"name"`
	ResourceID   string  `json:"resource_id"`
	TenantID     string  `json:"tenant_id"`
}

func (resource ResourceRef) Validate() error {
	if err := validateUUID("cluster_id", resource.ClusterID); err != nil {
		return err
	}
	if resource.ResourceType == "" || resource.Name == "" {
		return errors.New("resource_type and name are required")
	}
	namespace := ""
	if resource.Namespace != nil {
		namespace = *resource.Namespace
	}
	if namespace == "" {
		namespace = "_"
	}
	expected := fmt.Sprintf("%s:%s:%s:%s", resource.ResourceType, resource.ClusterID, namespace, resource.Name)
	if resource.ResourceID != expected {
		return errors.New("resource_id must be <type>:<cluster_uuid>:<namespace-or->_>:<name> and must NOT include tenant_id")
	}
	// tenant_id is required as an isolation dimension but never part of resource_id.
	if err := validateUUID("tenant_id", resource.TenantID); err != nil {
		return err
	}
	return nil
}

type StructuredError struct {
	ErrorCode string            `json:"error_code"`
	Message   string            `json:"message"`
	Retryable bool              `json:"retryable"`
	Fields    map[string]string `json:"fields"`
}

type ToolResult struct {
	ToolName     string   `json:"tool_name"`
	ClusterID    string   `json:"cluster_id"`
	Success      bool     `json:"success"`
	Status       string   `json:"status"`
	Summary      string   `json:"summary"`
	Data         any      `json:"data"`
	ErrorCode    string   `json:"error_code"`
	ErrorMessage string   `json:"error_message"`
	Retryable    bool     `json:"retryable"`
	EvidenceIDs  []string `json:"evidence_ids"`
	SourceSystem string   `json:"source_system"`
	QueryID      string   `json:"query_id"`
	TimeRange    any      `json:"time_range"`
	// Error 是 Go binding 内部承载结构化错误的结构（经 error_code/error_message 落 wire）。
	// `json:"-"`：不序列化到 wire，保证三端 wire 严格为 V1 冻结 15 字段（Python/TS/Schema
	// 均不接受第 16 个 "error" 字段）。R2 方案 B 三端一致性（Bugbot B4/C3）。
	Error      *StructuredError `json:"-"`
	StartedAt  time.Time        `json:"started_at"`
	FinishedAt time.Time        `json:"finished_at"`
}

func (result ToolResult) Validate() error {
	allowed := map[string]bool{
		"success": true, "partial": true, "no_data": true, "failed": true,
		"timeout": true, "unavailable": true, "permission_denied": true,
	}
	if !allowed[result.Status] {
		return fmt.Errorf("unsupported ToolResult status %q", result.Status)
	}
	if err := validateUUID("cluster_id", result.ClusterID); err != nil {
		return err
	}
	if result.FinishedAt.Before(result.StartedAt) {
		return errors.New("finished_at must not precede started_at")
	}
	// V9.2: "executed successfully" and "has data" are distinct.
	// success=true is allowed with status in {success, partial, no_data}.
	if result.Success && result.Status != "success" && result.Status != "partial" && result.Status != "no_data" {
		return errors.New("successful ToolResult must use success, partial, or no_data")
	}
	if !result.Success && (result.Status == "success" || result.Status == "partial" || result.Status == "no_data") {
		return errors.New("failed ToolResult must use a non-success status")
	}
	if result.Status == "permission_denied" && result.ErrorCode == "" {
		return errors.New("permission_denied ToolResult requires a structured error code")
	}
	return nil
}

// ActionExecutionContext 是 query-api → ai-action-executor 的执行上下文（Stage D，报告 §29）。
//
// 安全边界：Action Execution Result 权威持久化在 query-api/MySQL（ai_actions），
// ai-action-executor 不是第二套 Action SoT。query-api 用动作执行专用 Ed25519 私钥
// （AI_ACTION_EXECUTOR_SIGNING_KEY）按 executor 的签名机制（Ed25519 over body
// SHA256，X-Executor-Signature header）签发，executor 持对应公钥
// （AI_ACTION_EXECUTOR_VERIFY_KEYS）验签。
//
// 字段与 ai-action-executor 的 ActionExecutionContext JSON 严格对应（action_id /
// action_hash / approval_id / target_uid / target_name / resource_version / cluster_id /
// resource_type / namespace / operation / target_spec / credential_ref / approved_at / executed_by）。
type ActionExecutionContext struct {
	ActionID        string          `json:"action_id"`   // 绑定 immutable action 身份
	ActionHash      string          `json:"action_hash"` // 绑定 immutable action
	ApprovalID      string          `json:"approval_id"`
	TargetUID       string          `json:"target_uid"`       // 执行前重新读取校验（TOCTOU）
	TargetName      string          `json:"target_name"`      // K8s lookup 用
	ResourceVersion string          `json:"resource_version"` // TOCTOU precondition
	ClusterID       string          `json:"cluster_id"`
	ResourceType    string          `json:"resource_type"` // deployment/statefulset/daemonset/pod/node
	Namespace       string          `json:"namespace"`
	Operation       string          `json:"operation"` // canonical K8s operation（白名单）
	TargetSpec      json.RawMessage `json:"target_spec"`
	CredentialRef   string          `json:"credential_ref"`
	ApprovedAt      string          `json:"approved_at"`
	ExecutedBy      string          `json:"executed_by"`
}

// ActionResult 是 executor 返回的执行结果（回写 ai_actions，非本服务 SoT）。
type ActionResult struct {
	ActionID        string `json:"action_id"`
	Status          string `json:"status"` // execute: success|failed|execution_unknown|rejected|rollback_required; reconcile: applied|not_applied|drift|unknown
	ObservedUID     string `json:"observed_uid"`
	ObservedVersion string `json:"observed_version"`
	Message         string `json:"message"`
	ExecutedAt      string `json:"executed_at"`
}

// Validate 校验 ActionExecutionContext 的必填安全字段。
func (c ActionExecutionContext) Validate() error {
	if c.ActionID == "" || c.ActionHash == "" || c.TargetUID == "" {
		return errors.New("action_id, action_hash and target_uid are required")
	}
	if c.TargetName == "" {
		return errors.New("target_name is required")
	}
	if c.ResourceType == "" || c.Operation == "" {
		return errors.New("operation is required")
	}
	allowed := map[string]map[string]bool{
		"patch":           {"deployment": true}, // legacy internal executor compatibility; browser proposals use canonical operations
		"rollout_restart": {"deployment": true, "statefulset": true, "daemonset": true},
		"scale":           {"deployment": true, "statefulset": true},
		"delete_pod":      {"pod": true},
		"evict_pod":       {"pod": true},
		"cordon":          {"node": true},
		"uncordon":        {"node": true},
		"drain":           {"node": true},
	}
	if kinds, ok := allowed[c.Operation]; !ok || !kinds[c.ResourceType] {
		return fmt.Errorf("unsupported operation %q for resource_type %q", c.Operation, c.ResourceType)
	}
	return nil
}
