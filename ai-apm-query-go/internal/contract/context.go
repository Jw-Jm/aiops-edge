// Package contract contains strict internal JSON contracts shared by the
// query-api boundary and the Agentic AIOps control plane.
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
		return errors.New("TrustedRequestContext lifetime must be between 1 and 60 seconds")
	}
	return nil
}

type ResourceRef struct {
	TenantID     string  `json:"tenant_id"`
	ClusterID    string  `json:"cluster_id"`
	ResourceType string  `json:"resource_type"`
	Namespace    *string `json:"namespace"`
	Name         string  `json:"name"`
	ResourceID   string  `json:"resource_id"`
}

func (resource ResourceRef) Validate() error {
	if err := validateUUID("tenant_id", resource.TenantID); err != nil {
		return err
	}
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
	expected := fmt.Sprintf("urn:aiops:%s:%s:%s:%s:%s", resource.TenantID, resource.ClusterID, resource.ResourceType, namespace, resource.Name)
	if resource.ResourceID != expected {
		return errors.New("resource_id must use tenant and immutable canonical cluster UUID")
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
	ToolName    string           `json:"tool_name"`
	Success     bool             `json:"success"`
	Status      string           `json:"status"`
	Summary     string           `json:"summary"`
	Data        any              `json:"data"`
	Error       *StructuredError `json:"error"`
	EvidenceIDs []string         `json:"evidence_ids"`
	StartedAt   time.Time        `json:"started_at"`
	FinishedAt  time.Time        `json:"finished_at"`
}

func (result ToolResult) Validate() error {
	allowed := map[string]bool{
		"success": true, "partial": true, "no_data": true, "failed": true,
		"timeout": true, "unavailable": true, "permission_denied": true,
	}
	if !allowed[result.Status] {
		return fmt.Errorf("unsupported ToolResult status %q", result.Status)
	}
	if result.FinishedAt.Before(result.StartedAt) {
		return errors.New("finished_at must not precede started_at")
	}
	if result.Success && result.Status != "success" && result.Status != "partial" {
		return errors.New("successful ToolResult must use success or partial status")
	}
	if !result.Success && (result.Status == "success" || result.Status == "partial") {
		return errors.New("failed ToolResult must use a non-success status")
	}
	if result.Status == "permission_denied" && result.Error == nil {
		return errors.New("permission_denied ToolResult requires a structured error")
	}
	return nil
}

func validateUUID(field, value string) error {
	if strings.ToLower(value) != value || !canonicalUUID.MatchString(value) {
		return fmt.Errorf("%s must be a lowercase canonical UUID", field)
	}
	return nil
}
