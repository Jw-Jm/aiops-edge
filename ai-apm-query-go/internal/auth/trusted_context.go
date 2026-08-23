// Package auth contains standalone internal service authentication primitives.
//
// V9.2 Service Identity (INTERNAL-AUTH-P0-011):
//   - JWS Compact Serialization, EdDSA / Ed25519
//   - JWS header: alg=EdDSA, typ=AIOPS-CONTEXT, kid=<key-id>
//   - Each traffic direction uses an independent signing keypair
//   - The verifier holds only the opposite direction's public key
//   - nonce + replay protection is unified across all three contexts
package auth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
)

// JWS type for V9.2 contexts. Legacy internal contexts used typ=JWT which is now forbidden.
const jwsTypeAIOPSContext = "AIOPS-CONTEXT"

var (
	ErrInvalidService    = errors.New("invalid service token")
	ErrInvalidSignature  = errors.New("invalid trusted context signature")
	ErrInvalidContext    = errors.New("invalid trusted request context")
	ErrExpiredContext    = errors.New("expired trusted request context")
	ErrReplayedContext   = errors.New("replayed trusted request context")
	ErrReplayCacheFull   = errors.New("trusted request context replay cache is full")
	ErrWrongAudience     = errors.New("wrong trusted request context audience")
	ErrWrongContextType  = errors.New("wrong trusted request context type")
)

// VerifyConfig holds the independently managed service credential and the
// opposite direction's public verification keys for the V9.2 contexts.
type VerifyConfig struct {
	Audience     string
	Issuer       string
	PublicKeys   map[string]ed25519.PublicKey
	ServiceToken string
	ReplayCache  ReplayCache
	ClockSkew    time.Duration
}

// ReplayCache records nonce values until the verifier's acceptance window has
// closed. Implementations must reject a nonce that was already recorded and
// remain bounded.
type ReplayCache interface {
	CheckAndStore(nonce string, expiresAt, now time.Time) error
}

// InMemoryReplayCache is a concurrency-safe bounded cache for short-lived
// context nonces.
type InMemoryReplayCache struct {
	mu       sync.Mutex
	maxItems int
	nonces   map[string]time.Time
}

// NewReplayCache creates a bounded cache. A non-positive value retains one
// entry, which keeps replay protection enabled even for a misconfigured size.
func NewReplayCache(maxItems int) *InMemoryReplayCache {
	if maxItems < 1 {
		maxItems = 1
	}
	return &InMemoryReplayCache{maxItems: maxItems, nonces: make(map[string]time.Time)}
}

// CheckAndStore rejects a replay and removes expired records before storing a
// new nonce. A full cache fails closed rather than evicting a valid nonce.
func (cache *InMemoryReplayCache) CheckAndStore(nonce string, expiresAt, now time.Time) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for storedNonce, storedExpiry := range cache.nonces {
		if storedExpiry.Before(now) {
			delete(cache.nonces, storedNonce)
		}
	}
	if _, exists := cache.nonces[nonce]; exists {
		return ErrReplayedContext
	}
	if len(cache.nonces) >= cache.maxItems {
		return ErrReplayCacheFull
	}
	cache.nonces[nonce] = expiresAt
	return nil
}

type protectedHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ"`
}

// KeyID derives a deterministic, non-secret key identifier for key rotation.
func KeyID(publicKey ed25519.PublicKey) string {
	digest := sha256.Sum256(publicKey)
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// VerifyServiceToken checks the separate internal service credential in
// constant time. It must be called independently from context verification.
func VerifyServiceToken(provided string, cfg VerifyConfig) error {
	providedHash := sha256.Sum256([]byte(provided))
	expectedHash := sha256.Sum256([]byte(cfg.ServiceToken))
	matches := subtle.ConstantTimeCompare(providedHash[:], expectedHash[:])
	if cfg.ServiceToken == "" || matches != 1 {
		return ErrInvalidService
	}
	return nil
}

// signJWS signs any V9.2 context payload with EdDSA and typ=AIOPS-CONTEXT.
func signJWS(payload any, privateKey ed25519.PrivateKey) (string, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("%w: invalid Ed25519 private key", ErrInvalidSignature)
	}
	header, err := json.Marshal(protectedHeader{
		Algorithm: "EdDSA",
		KeyID:     KeyID(privateKey.Public().(ed25519.PublicKey)),
		Type:      jwsTypeAIOPSContext,
	})
	if err != nil {
		return "", fmt.Errorf("%w: encode protected header", ErrInvalidContext)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("%w: encode context", ErrInvalidContext)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body)
	signature := ed25519.Sign(privateKey, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// verifyJWSPayload verifies the JWS envelope (alg=EdDSA, typ=AIOPS-CONTEXT, kid,
// signature) and returns the raw payload bytes. Time/replay/type checks are done
// by the caller with the decoded typed context.
func verifyJWSPayload(token string, cfg VerifyConfig) ([]byte, error) {
	if cfg.Audience == "" || cfg.Issuer == "" || cfg.ReplayCache == nil || cfg.ClockSkew < 0 {
		return nil, fmt.Errorf("%w: invalid verifier configuration", ErrInvalidContext)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, ErrInvalidSignature
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrInvalidSignature
	}
	var header protectedHeader
	if err := decodeStrict(headerBytes, &header); err != nil || header.Algorithm != "EdDSA" || header.Type != jwsTypeAIOPSContext || header.KeyID == "" {
		return nil, ErrInvalidSignature
	}
	publicKey, exists := cfg.PublicKeys[header.KeyID]
	if !exists || len(publicKey) != ed25519.PublicKeySize {
		return nil, ErrInvalidSignature
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey, []byte(parts[0]+"."+parts[1]), signature) {
		return nil, ErrInvalidSignature
	}
	return base64.RawURLEncoding.DecodeString(parts[1])
}

// verifyCommonClaims validates issuer, audience, time bounds and nonce replay for
// a decoded context carrying the shared claims (Issuer/Audience/IssuedAt/ExpiresAt/Nonce).
func verifyCommonClaims(issuer, audience string, issuedAt, expiresAt time.Time, nonce string, cfg VerifyConfig, now time.Time) error {
	if issuer != cfg.Issuer {
		return ErrInvalidContext
	}
	if audience != cfg.Audience {
		return ErrWrongAudience
	}
	if expiresAt.Before(now.Add(-cfg.ClockSkew)) {
		return ErrExpiredContext
	}
	if issuedAt.After(now.Add(cfg.ClockSkew)) {
		return ErrInvalidContext
	}
	if err := cfg.ReplayCache.CheckAndStore(nonce, expiresAt.Add(cfg.ClockSkew), now); err != nil {
		return err
	}
	return nil
}

// ═══════════════════════════════════════════════════════════════════════
// V9.2 three contexts — sign
// ═══════════════════════════════════════════════════════════════════════

// SignRunInvocationContext signs a RunInvocationContext (query-api → orchestrator).
func SignRunInvocationContext(ctx contract.RunInvocationContext, privateKey ed25519.PrivateKey) (string, error) {
	if ctx.ContextType != "run_invocation" {
		return "", ErrWrongContextType
	}
	if err := ctx.Validate(); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidContext, err)
	}
	return signJWS(ctx, privateKey)
}

// SignRunControlContext signs a RunControlContext (query-api → orchestrator).
func SignRunControlContext(ctx contract.RunControlContext, privateKey ed25519.PrivateKey) (string, error) {
	if ctx.ContextType != "run_control" {
		return "", ErrWrongContextType
	}
	if err := ctx.Validate(); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidContext, err)
	}
	return signJWS(ctx, privateKey)
}

// SignTrustedRequestContextV2 signs a TrustedRequestContext (orchestrator → query-api).
func SignTrustedRequestContextV2(ctx contract.TrustedRequestContext, privateKey ed25519.PrivateKey) (string, error) {
	if ctx.ContextType != "trusted_request" {
		return "", ErrWrongContextType
	}
	if err := ctx.Validate(); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidContext, err)
	}
	return signJWS(ctx, privateKey)
}

// ═══════════════════════════════════════════════════════════════════════
// V9.2 three contexts — verify (type-specific, prevents confused-deputy)
// ═══════════════════════════════════════════════════════════════════════

// VerifyRunInvocationContext verifies a RunInvocationContext token.
func VerifyRunInvocationContext(token string, cfg VerifyConfig, now time.Time) (contract.RunInvocationContext, error) {
	var zero contract.RunInvocationContext
	payload, err := verifyJWSPayload(token, cfg)
	if err != nil {
		return zero, err
	}
	// Check context_type before strict decode to return a clean ErrWrongContextType
	// for a valid token of the wrong type (confused-deputy prevention).
	var typeProbe struct {
		ContextType string `json:"context_type"`
	}
	if err := json.Unmarshal(payload, &typeProbe); err != nil || typeProbe.ContextType != "run_invocation" {
		return zero, ErrWrongContextType
	}
	var ctx contract.RunInvocationContext
	if err := contract.DecodeStrict(payload, &ctx); err != nil {
		return zero, fmt.Errorf("%w: %v", ErrInvalidContext, err)
	}
	if err := ctx.Validate(); err != nil {
		return zero, fmt.Errorf("%w: %v", ErrInvalidContext, err)
	}
	if err := verifyCommonClaims(ctx.Issuer, ctx.Audience, ctx.IssuedAt, ctx.ExpiresAt, ctx.Nonce, cfg, now); err != nil {
		return zero, err
	}
	return ctx, nil
}

// VerifyRunControlContext verifies a RunControlContext token.
func VerifyRunControlContext(token string, cfg VerifyConfig, now time.Time) (contract.RunControlContext, error) {
	var zero contract.RunControlContext
	payload, err := verifyJWSPayload(token, cfg)
	if err != nil {
		return zero, err
	}
	var typeProbe struct {
		ContextType string `json:"context_type"`
	}
	if err := json.Unmarshal(payload, &typeProbe); err != nil || typeProbe.ContextType != "run_control" {
		return zero, ErrWrongContextType
	}
	var ctx contract.RunControlContext
	if err := contract.DecodeStrict(payload, &ctx); err != nil {
		return zero, fmt.Errorf("%w: %v", ErrInvalidContext, err)
	}
	if err := ctx.Validate(); err != nil {
		return zero, fmt.Errorf("%w: %v", ErrInvalidContext, err)
	}
	if err := verifyCommonClaims(ctx.Issuer, ctx.Audience, ctx.IssuedAt, ctx.ExpiresAt, ctx.Nonce, cfg, now); err != nil {
		return zero, err
	}
	return ctx, nil
}

// VerifyTrustedRequestContextV2 verifies a TrustedRequestContext token.
func VerifyTrustedRequestContextV2(token string, cfg VerifyConfig, now time.Time) (contract.TrustedRequestContext, error) {
	var zero contract.TrustedRequestContext
	payload, err := verifyJWSPayload(token, cfg)
	if err != nil {
		return zero, err
	}
	var typeProbe struct {
		ContextType string `json:"context_type"`
	}
	if err := json.Unmarshal(payload, &typeProbe); err != nil || typeProbe.ContextType != "trusted_request" {
		return zero, ErrWrongContextType
	}
	var ctx contract.TrustedRequestContext
	if err := contract.DecodeStrict(payload, &ctx); err != nil {
		return zero, fmt.Errorf("%w: %v", ErrInvalidContext, err)
	}
	if err := ctx.Validate(); err != nil {
		return zero, fmt.Errorf("%w: %v", ErrInvalidContext, err)
	}
	if err := verifyCommonClaims(ctx.Issuer, ctx.Audience, ctx.IssuedAt, ctx.ExpiresAt, ctx.Nonce, cfg, now); err != nil {
		return zero, err
	}
	return ctx, nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON")
		}
		return err
	}
	return nil
}
