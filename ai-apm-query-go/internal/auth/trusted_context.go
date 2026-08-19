// Package auth contains standalone internal service authentication primitives.
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

var (
	ErrInvalidService   = errors.New("invalid service token")
	ErrInvalidSignature = errors.New("invalid trusted context signature")
	ErrInvalidContext   = errors.New("invalid trusted request context")
	ErrExpiredContext   = errors.New("expired trusted request context")
	ErrReplayedContext  = errors.New("replayed trusted request context")
	ErrReplayCacheFull  = errors.New("trusted request context replay cache is full")
	ErrWrongAudience    = errors.New("wrong trusted request context audience")
)

// VerifyConfig holds the independently managed service credential and signing
// verification material for TrustedRequestContext tokens.
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
// TrustedRequestContext nonces.
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

// SignTrustedRequestContext produces a strict EdDSA JWS containing only the
// shared RequestContext contract. Service authentication is deliberately not
// part of this token.
func SignTrustedRequestContext(ctx contract.RequestContext, privateKey ed25519.PrivateKey) (string, error) {
	if err := validateLifetime(ctx); err != nil {
		return "", err
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("%w: invalid Ed25519 private key", ErrInvalidSignature)
	}
	header, err := json.Marshal(protectedHeader{Algorithm: "EdDSA", KeyID: KeyID(privateKey.Public().(ed25519.PublicKey)), Type: "JWT"})
	if err != nil {
		return "", fmt.Errorf("%w: encode protected header", ErrInvalidContext)
	}
	payload, err := json.Marshal(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: encode context", ErrInvalidContext)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	signature := ed25519.Sign(privateKey, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
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

// VerifyTrustedRequestContext validates a strict EdDSA JWS, its contract
// claims, time bounds, configured issuer/audience, and one-time nonce use.
func VerifyTrustedRequestContext(token string, cfg VerifyConfig, now time.Time) (contract.RequestContext, error) {
	var zero contract.RequestContext
	if cfg.Audience == "" || cfg.Issuer == "" || cfg.ReplayCache == nil || cfg.ClockSkew < 0 {
		return zero, fmt.Errorf("%w: invalid verifier configuration", ErrInvalidContext)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return zero, ErrInvalidSignature
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return zero, ErrInvalidSignature
	}
	var header protectedHeader
	if err := decodeStrict(headerBytes, &header); err != nil || header.Algorithm != "EdDSA" || header.Type != "JWT" || header.KeyID == "" {
		return zero, ErrInvalidSignature
	}
	publicKey, exists := cfg.PublicKeys[header.KeyID]
	if !exists || len(publicKey) != ed25519.PublicKeySize {
		return zero, ErrInvalidSignature
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey, []byte(parts[0]+"."+parts[1]), signature) {
		return zero, ErrInvalidSignature
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return zero, ErrInvalidContext
	}
	var ctx contract.RequestContext
	if err := contract.DecodeStrict(payload, &ctx); err != nil {
		return zero, fmt.Errorf("%w: %v", ErrInvalidContext, err)
	}
	if err := validateLifetime(ctx); err != nil {
		return zero, err
	}
	if ctx.Issuer != cfg.Issuer {
		return zero, ErrInvalidContext
	}
	if ctx.Audience != cfg.Audience {
		return zero, ErrWrongAudience
	}
	if ctx.ExpiresAt.Before(now.Add(-cfg.ClockSkew)) {
		return zero, ErrExpiredContext
	}
	if ctx.IssuedAt.After(now.Add(cfg.ClockSkew)) {
		return zero, ErrInvalidContext
	}
	if err := cfg.ReplayCache.CheckAndStore(ctx.Nonce, ctx.ExpiresAt.Add(cfg.ClockSkew), now); err != nil {
		return zero, err
	}
	return ctx, nil
}

func validateLifetime(ctx contract.RequestContext) error {
	if err := ctx.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidContext, err)
	}
	lifetime := ctx.ExpiresAt.UTC().Sub(ctx.IssuedAt.UTC())
	if lifetime < 30*time.Second || lifetime > 60*time.Second {
		return fmt.Errorf("%w: context lifetime must be between 30 and 60 seconds", ErrInvalidContext)
	}
	return nil
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
