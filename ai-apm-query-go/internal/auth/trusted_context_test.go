package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
)

func TestSignAndVerifyTrustedRequestContext(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)
	token, err := SignTrustedRequestContext(testContext(now), privateKey)
	if err != nil {
		t.Fatalf("SignTrustedRequestContext() error = %v", err)
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token parts = %d, want 3", len(parts))
	}
	var header map[string]string
	decodedHeader, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(decodedHeader, &header); err != nil {
		t.Fatal(err)
	}
	if header["alg"] != "EdDSA" || header["kid"] != KeyID(publicKey) {
		t.Fatalf("unexpected protected header: %#v", header)
	}

	verified, err := VerifyTrustedRequestContext(token, verifyConfig(publicKey), now)
	if err != nil {
		t.Fatalf("VerifyTrustedRequestContext() error = %v", err)
	}
	if verified != testContext(now) {
		t.Fatalf("verified context = %#v, want %#v", verified, testContext(now))
	}
}

func TestVerifyTrustedRequestContextRejectsInvalidAlgorithmAndKeyID(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)
	token, err := SignTrustedRequestContext(testContext(now), privateKey)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name   string
		mutate func(map[string]string)
	}{
		{"algorithm", func(header map[string]string) { header["alg"] = "HS256" }},
		{"key ID", func(header map[string]string) { header["kid"] = "unknown" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := VerifyTrustedRequestContext(rewriteHeader(t, token, test.mutate), verifyConfig(publicKey), now); err == nil {
				t.Fatal("VerifyTrustedRequestContext() accepted invalid protected header")
			}
		})
	}
}

func TestVerifyTrustedRequestContextValidatesAudienceIssuerLifetimeAndTime(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)

	for _, test := range []struct {
		name    string
		context contract.RequestContext
		config  VerifyConfig
	}{
		{"wrong audience", testContext(now), VerifyConfig{Audience: "other", Issuer: "ai-orchestrator", PublicKeys: map[string]ed25519.PublicKey{KeyID(publicKey): publicKey}, ReplayCache: NewReplayCache(8)}},
		{"wrong issuer", testContext(now), VerifyConfig{Audience: "ai-apm-query-go", Issuer: "other", PublicKeys: map[string]ed25519.PublicKey{KeyID(publicKey): publicKey}, ReplayCache: NewReplayCache(8)}},
		{"short lifetime", withContext(testContext(now), func(context *contract.RequestContext) { context.ExpiresAt = context.IssuedAt.Add(29 * time.Second) }), verifyConfig(publicKey)},
		{"long lifetime", withContext(testContext(now), func(context *contract.RequestContext) { context.ExpiresAt = context.IssuedAt.Add(61 * time.Second) }), verifyConfig(publicKey)},
		{"expired", withContext(testContext(now), func(context *contract.RequestContext) {
			context.IssuedAt = now.Add(-60 * time.Second)
			context.ExpiresAt = now.Add(-30 * time.Second)
		}), verifyConfig(publicKey)},
		{"future", withContext(testContext(now), func(context *contract.RequestContext) {
			context.IssuedAt = now.Add(time.Second)
			context.ExpiresAt = now.Add(31 * time.Second)
		}), verifyConfig(publicKey)},
	} {
		t.Run(test.name, func(t *testing.T) {
			token, err := SignTrustedRequestContext(test.context, privateKey)
			if test.name == "short lifetime" || test.name == "long lifetime" {
				token, err = signUnchecked(test.context, privateKey)
			}
			if err != nil {
				t.Fatalf("SignTrustedRequestContext() error = %v", err)
			}
			if _, err := VerifyTrustedRequestContext(token, test.config, now); err == nil {
				t.Fatal("VerifyTrustedRequestContext() accepted invalid context")
			}
		})
	}
}

func TestVerifyTrustedRequestContextRejectsReplayAndServiceTokenIsSeparate(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)
	config := verifyConfig(publicKey)
	token, err := SignTrustedRequestContext(testContext(now), privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyTrustedRequestContext(token, config, now); err != nil {
		t.Fatalf("first verification error = %v", err)
	}
	if _, err := VerifyTrustedRequestContext(token, config, now); err == nil {
		t.Fatal("VerifyTrustedRequestContext() accepted replay")
	}
	if err := VerifyServiceToken("different", config); err == nil {
		t.Fatal("VerifyServiceToken() accepted a different credential")
	}
	if err := VerifyServiceToken("service-secret", config); err != nil {
		t.Fatalf("VerifyServiceToken() error = %v", err)
	}
}

func TestReplayCacheDoesNotEvictValidNonceWhenBounded(t *testing.T) {
	cache := NewReplayCache(1)
	now := time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)
	if err := cache.CheckAndStore("first", now.Add(30*time.Second), now); err != nil {
		t.Fatalf("store first nonce: %v", err)
	}
	if err := cache.CheckAndStore("second", now.Add(30*time.Second), now); err == nil {
		t.Fatal("cache accepted a new nonce by evicting a valid replay record")
	}
	if err := cache.CheckAndStore("first", now.Add(30*time.Second), now); err == nil {
		t.Fatal("cache accepted replay after reaching capacity")
	}
}

func testContext(now time.Time) contract.RequestContext {
	return contract.RequestContext{Version: 1, Issuer: "ai-orchestrator", Audience: "ai-apm-query-go", RequestID: "11111111-1111-4111-8111-111111111111", RunID: "22222222-2222-4222-8222-222222222222", UserID: "33333333-3333-4333-8333-333333333333", SessionID: "44444444-4444-4444-8444-444444444444", TenantID: "55555555-5555-4555-8555-555555555555", ClusterID: "66666666-6666-4666-8666-666666666666", Source: "planner", Capability: "kubernetes.read", IssuedAt: now, ExpiresAt: now.Add(30 * time.Second), Nonce: "77777777-7777-4777-8777-777777777777"}
}

func verifyConfig(publicKey ed25519.PublicKey) VerifyConfig {
	return VerifyConfig{Audience: "ai-apm-query-go", Issuer: "ai-orchestrator", PublicKeys: map[string]ed25519.PublicKey{KeyID(publicKey): publicKey}, ServiceToken: "service-secret", ReplayCache: NewReplayCache(8)}
}

func withContext(context contract.RequestContext, mutate func(*contract.RequestContext)) contract.RequestContext {
	mutate(&context)
	return context
}

func rewriteHeader(t *testing.T, token string, mutate func(map[string]string)) string {
	t.Helper()
	parts := strings.Split(token, ".")
	decoded, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	var header map[string]string
	if err := json.Unmarshal(decoded, &header); err != nil {
		t.Fatal(err)
	}
	mutate(header)
	encoded, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	parts[0] = base64.RawURLEncoding.EncodeToString(encoded)
	return strings.Join(parts, ".")
}

func signUnchecked(context contract.RequestContext, privateKey ed25519.PrivateKey) (string, error) {
	header, err := json.Marshal(protectedHeader{Algorithm: "EdDSA", KeyID: KeyID(privateKey.Public().(ed25519.PublicKey)), Type: "JWT"})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(context)
	if err != nil {
		return "", err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(signingInput))), nil
}
