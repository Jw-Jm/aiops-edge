// Package auth — RunInvocation issuer for query-api → orchestrator (P3.9-B2).
//
// query-api signs RunInvocationContext (and later RunControlContext) with its own
// Ed25519 private key (typ=AIOPS-CONTEXT) and sends them to the orchestrator's
// trusted ingress together with a directional service credential
// (QUERY_TO_ORCHESTRATOR_TOKEN). The orchestrator holds only query-api's public key.
package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
)

const (
	// InvocationIssuer / InvocationAudience are the fixed direction claims for
	// query-api → orchestrator contexts (V9.2 §11).
	InvocationIssuer   = "query-api"
	InvocationAudience = "ai-orchestrator"
)

// RunInvocationIssuer holds the query-api signing material for the
// query-api → orchestrator direction.
type RunInvocationIssuer struct {
	privateKey ed25519.PrivateKey
	serviceToken string
}

// NewRunInvocationIssuer builds the issuer from the query-api signing private key
// and its directional service credential.
func NewRunInvocationIssuer(privateKey ed25519.PrivateKey, serviceToken string) (*RunInvocationIssuer, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid Ed25519 private key size")
	}
	if serviceToken == "" {
		return nil, fmt.Errorf("query-to-orchestrator service token is required")
	}
	return &RunInvocationIssuer{privateKey: privateKey, serviceToken: serviceToken}, nil
}

// Issuer returns the fixed issuer claim for this direction.
func (i *RunInvocationIssuer) Issuer() string { return InvocationIssuer }

// Audience returns the fixed audience claim for this direction.
func (i *RunInvocationIssuer) Audience() string { return InvocationAudience }

// ServiceToken is the directional service credential sent as X-Internal-Token.
func (i *RunInvocationIssuer) ServiceToken() string { return i.serviceToken }

// SignRunInvocation signs a RunInvocationContext. The caller provides the identity
// claims (principal, session, tenant, cluster_scope) and an optional capability
// (default "ai.investigate"). The issuer fills issuer/audience/request_id/TTL/nonce
// and signs with EdDSA typ=AIOPS-CONTEXT.
//
// P19.6: capability distinguishes 调查型 (ai.investigate) from 对话型 (ai.chat).
// It is signed into the context so the orchestrator trusts it, never a client claim.
func (i *RunInvocationIssuer) SignRunInvocation(
	principalType, principalID, sessionID, tenantID, source string,
	clusterScope []string,
	now time.Time,
	capability ...string,
) (string, error) {
	cap := "ai.investigate"
	if len(capability) > 0 && capability[0] != "" {
		cap = capability[0]
	}
	ctx := contract.NewRunInvocationContext(
		InvocationIssuer, InvocationAudience,
		randomUUID(), principalType, principalID, sessionID, tenantID, source,
		clusterScope, now, now.Add(60*time.Second), randomUUID(),
	)
	ctx.Capability = cap
	return SignRunInvocationContext(ctx, i.privateKey)
}

// SignRunControl signs a RunControlContext for controlling an existing run.
func (i *RunInvocationIssuer) SignRunControl(
	principalType, principalID, sessionID, tenantID, runID, operation string,
	now time.Time,
) (string, error) {
	ctx := contract.NewRunControlContext(
		InvocationIssuer, InvocationAudience,
		randomUUID(), principalType, principalID, sessionID, tenantID,
		runID, operation, now, now.Add(60*time.Second), randomUUID(),
	)
	return SignRunControlContext(ctx, i.privateKey)
}

// DecodePrivateKey decodes a base64 Ed25519 private key.
func DecodePrivateKey(encoded string) (ed25519.PrivateKey, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode Ed25519 private key: %w", err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid Ed25519 private key size")
	}
	return ed25519.PrivateKey(raw), nil
}

func randomUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
