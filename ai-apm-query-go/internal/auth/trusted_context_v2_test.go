package auth

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
)

// signTypJWT builds a typ=JWT signed token (legacy single RequestContext) inline.
// Used only to prove the V2 verifier rejects the legacy protocol; the legacy
// production signer was removed in P3.9-D.
func signTypJWT(t *testing.T, ctx contract.RequestContext, priv ed25519.PrivateKey) string {
	t.Helper()
	header, _ := json.Marshal(protectedHeader{Algorithm: "EdDSA", KeyID: KeyID(priv.Public().(ed25519.PublicKey)), Type: "JWT"})
	payload, _ := json.Marshal(ctx)
	si := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	sig := ed25519.Sign(priv, []byte(si))
	return si + "." + base64.RawURLEncoding.EncodeToString(sig)
}

const (
	testTenant  = "55555555-5555-4555-8555-555555555555"
	testCluster = "66666666-6666-4666-8666-666666666666"
	testUser    = "33333333-3333-4333-8333-333333333333"
	testSession = "44444444-4444-4444-8444-444444444444"
	testRun     = "22222222-2222-4222-8222-222222222222"
	testRequest = "11111111-1111-4111-8111-111111111111"
	testNonce   = "77777777-7777-4777-8777-777777777777"
)

func v2Config() VerifyConfig {
	_, priv, _ := ed25519.GenerateKey(nil)
	_, peerPriv, _ := ed25519.GenerateKey(nil)
	_ = priv
	cfg := VerifyConfig{
		Issuer:       "ai-orchestrator",
		Audience:     "ai-apm-query-go",
		PublicKeys:   map[string]ed25519.PublicKey{KeyID(peerPriv.Public().(ed25519.PublicKey)): peerPriv.Public().(ed25519.PublicKey)},
		ReplayCache:  NewReplayCache(100),
		ClockSkew:    30 * time.Second,
	}
	return cfg
}

func runInvocationFixture() contract.RunInvocationContext {
	now := time.Now().UTC().Truncate(time.Second)
	return contract.NewRunInvocationContext(
		"query-api", "ai-orchestrator", testRequest, "user", testUser, testSession,
		testTenant, "frontend", []string{testCluster}, now, now.Add(30*time.Second), testNonce)
}

func runControlFixture() contract.RunControlContext {
	now := time.Now().UTC().Truncate(time.Second)
	return contract.NewRunControlContext(
		"query-api", "ai-orchestrator", testRequest, "user", testUser, testSession,
		testTenant, testRun, "cancel", now, now.Add(30*time.Second), testNonce)
}

func trustedRequestFixture() contract.TrustedRequestContext {
	now := time.Now().UTC().Truncate(time.Second)
	return contract.NewTrustedRequestContext(
		"ai-orchestrator", "ai-apm-query-go", testRequest, "user", testUser, testSession,
		testTenant, testRun, "cluster", testCluster, "observability.logs.read", "log_agent",
		now, now.Add(30*time.Second), testNonce)
}

func TestSignVerifyRoundTripV2(t *testing.T) {
	_, signPriv, _ := ed25519.GenerateKey(nil)
	signPub := signPriv.Public().(ed25519.PublicKey)

	// RunInvocation: query-api signs; orchestrator verifies.
	inv := runInvocationFixture()
	token, err := SignRunInvocationContext(inv, signPriv)
	if err != nil {
		t.Fatalf("SignRunInvocationContext error = %v", err)
	}
	if !strings.HasPrefix(token, "ey") || !strings.Contains(token, ".") {
		t.Fatalf("token not JWS compact: %s", token)
	}
	cfg := VerifyConfig{
		Issuer: "query-api", Audience: "ai-orchestrator",
		PublicKeys: map[string]ed25519.PublicKey{KeyID(signPub): signPub},
		ReplayCache: NewReplayCache(100), ClockSkew: 30 * time.Second,
	}
	verified, err := VerifyRunInvocationContext(token, cfg, time.Now())
	if err != nil {
		t.Fatalf("VerifyRunInvocationContext error = %v", err)
	}
	if verified.ContextType != "run_invocation" || verified.TenantID != testTenant {
		t.Fatalf("unexpected verified context: %+v", verified)
	}

	// RunControl: query-api signs; orchestrator verifies. Use a distinct nonce.
	ctrl := runControlFixture()
	ctrl.Nonce = "88888888-8888-4888-8888-888888888888"
	ctrlToken, err := SignRunControlContext(ctrl, signPriv)
	if err != nil {
		t.Fatalf("SignRunControlContext error = %v", err)
	}
	ctrlVerified, err := VerifyRunControlContext(ctrlToken, cfg, time.Now())
	if err != nil {
		t.Fatalf("VerifyRunControlContext error = %v", err)
	}
	if ctrlVerified.Operation != "cancel" {
		t.Fatalf("unexpected run control operation: %+v", ctrlVerified)
	}

	// TrustedRequest: orchestrator signs; query-api verifies. Use a distinct nonce.
	tr := trustedRequestFixture()
	tr.Nonce = "99999999-9999-4999-8999-999999999999"
	trToken, err := SignTrustedRequestContextV2(tr, signPriv)
	if err != nil {
		t.Fatalf("SignTrustedRequestContextV2 error = %v", err)
	}
	trCfg := VerifyConfig{
		Issuer: "ai-orchestrator", Audience: "ai-apm-query-go",
		PublicKeys: map[string]ed25519.PublicKey{KeyID(signPub): signPub},
		ReplayCache: NewReplayCache(100), ClockSkew: 30 * time.Second,
	}
	trVerified, err := VerifyTrustedRequestContextV2(trToken, trCfg, time.Now())
	if err != nil {
		t.Fatalf("VerifyTrustedRequestContextV2 error = %v", err)
	}
	if trVerified.Capability != "observability.logs.read" {
		t.Fatalf("unexpected trusted request: %+v", trVerified)
	}
}

func TestV2RejectsWrongContextTypeOnEndpoint(t *testing.T) {
	_, signPriv, _ := ed25519.GenerateKey(nil)
	signPub := signPriv.Public().(ed25519.PublicKey)
	cfg := VerifyConfig{
		Issuer: "query-api", Audience: "ai-orchestrator",
		PublicKeys: map[string]ed25519.PublicKey{KeyID(signPub): signPub},
		ReplayCache: NewReplayCache(100), ClockSkew: 30 * time.Second,
	}

	// A valid RunControlContext sent to the RunInvocation verifier must reject.
	ctrl := runControlFixture()
	ctrlToken, _ := SignRunControlContext(ctrl, signPriv)
	if _, err := VerifyRunInvocationContext(ctrlToken, cfg, time.Now()); err != ErrWrongContextType {
		t.Fatalf("expected ErrWrongContextType, got %v", err)
	}
}

func TestV2RejectsTamperedAndWrongKey(t *testing.T) {
	_, signPriv, _ := ed25519.GenerateKey(nil)
	_, otherPriv, _ := ed25519.GenerateKey(nil)

	tr := trustedRequestFixture()
	trToken, _ := SignTrustedRequestContextV2(tr, signPriv)

	// wrong public key
	otherPub := otherPriv.Public().(ed25519.PublicKey)
	wrongCfg := VerifyConfig{
		Issuer: "ai-orchestrator", Audience: "ai-apm-query-go",
		PublicKeys: map[string]ed25519.PublicKey{KeyID(otherPub): otherPub},
		ReplayCache: NewReplayCache(100), ClockSkew: 30 * time.Second,
	}
	if _, err := VerifyTrustedRequestContextV2(trToken, wrongCfg, time.Now()); err == nil {
		t.Fatal("expected wrong-key verification to fail")
	}

	// tampered payload
	parts := strings.Split(trToken, ".")
	if len(parts) == 3 {
		// flip a byte in the payload segment
		tampered := parts[0] + "." + parts[1][:len(parts[1])-1] + "A" + "." + parts[2]
		goodCfg := VerifyConfig{
			Issuer: "ai-orchestrator", Audience: "ai-apm-query-go",
			PublicKeys: map[string]ed25519.PublicKey{KeyID(signPriv.Public().(ed25519.PublicKey)): signPriv.Public().(ed25519.PublicKey)},
			ReplayCache: NewReplayCache(100), ClockSkew: 30 * time.Second,
		}
		if _, err := VerifyTrustedRequestContextV2(tampered, goodCfg, time.Now()); err == nil {
			t.Fatal("expected tampered payload to fail")
		}
	}
}

func TestV2RejectsExpiredAndReplay(t *testing.T) {
	_, signPriv, _ := ed25519.GenerateKey(nil)
	signPub := signPriv.Public().(ed25519.PublicKey)
	cfg := VerifyConfig{
		Issuer: "ai-orchestrator", Audience: "ai-apm-query-go",
		PublicKeys: map[string]ed25519.PublicKey{KeyID(signPub): signPub},
		ReplayCache: NewReplayCache(100), ClockSkew: 30 * time.Second,
	}

	// expired
	// 用一个 now 基准派生 IssuedAt/ExpiresAt：若用两次 time.Now() 会引入微秒间隔 δ，
	// 使 lifetime = 60s + δ > 60s，validateCommon 会拒绝签名（返回空 token → verify 报
	// ErrInvalidSignature），从而间歇性 flaky。用同一 now 保证 lifetime 恰为 60s。
	tr := trustedRequestFixture()
	expiredNow := time.Now().UTC()
	tr.IssuedAt = expiredNow.Add(-5 * time.Minute)
	tr.ExpiresAt = expiredNow.Add(-4 * time.Minute)
	tr.Nonce = "88888888-8888-4888-8888-888888888888"
	expiredToken, _ := SignTrustedRequestContextV2(tr, signPriv)
	if _, err := VerifyTrustedRequestContextV2(expiredToken, cfg, time.Now()); err != ErrExpiredContext {
		t.Fatalf("expected ErrExpiredContext, got %v", err)
	}

	// replay
	fresh := trustedRequestFixture()
	fresh.Nonce = "99999999-9999-4999-8999-999999999999"
	replayCfg := VerifyConfig{
		Issuer: "ai-orchestrator", Audience: "ai-apm-query-go",
		PublicKeys: map[string]ed25519.PublicKey{KeyID(signPub): signPub},
		ReplayCache: NewReplayCache(100), ClockSkew: 30 * time.Second,
	}
	freshToken, _ := SignTrustedRequestContextV2(fresh, signPriv)
	if _, err := VerifyTrustedRequestContextV2(freshToken, replayCfg, time.Now()); err != nil {
		t.Fatalf("first verify should pass: %v", err)
	}
	if _, err := VerifyTrustedRequestContextV2(freshToken, replayCfg, time.Now()); err != ErrReplayedContext {
		t.Fatalf("expected ErrReplayedContext, got %v", err)
	}
}

func TestV2RejectsTypJWTAndWrongIssuerAudience(t *testing.T) {
	_, signPriv, _ := ed25519.GenerateKey(nil)
	signPub := signPriv.Public().(ed25519.PublicKey)

	// Build a token with typ=JWT (forbidden) inline; legacy signer was removed in P3.9.
	legacy := contract.RequestContext{
		Version: 1, Issuer: "ai-orchestrator", Audience: "ai-apm-query-go",
		RequestID: testRequest, RunID: testRun, UserID: testUser, SessionID: testSession,
		TenantID: testTenant, ClusterID: testCluster, Source: "planner", Capability: "observability.logs.read",
		IssuedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(30 * time.Second), Nonce: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
	}
	legacyToken := signTypJWT(t, legacy, signPriv)
	cfg := VerifyConfig{
		Issuer: "ai-orchestrator", Audience: "ai-apm-query-go",
		PublicKeys: map[string]ed25519.PublicKey{KeyID(signPub): signPub},
		ReplayCache: NewReplayCache(100), ClockSkew: 30 * time.Second,
	}
	// typ=JWT must be rejected by the V2 verifier (not decoded as AIOPS-CONTEXT).
	if _, err := VerifyTrustedRequestContextV2(legacyToken, cfg, time.Now()); err == nil {
		t.Fatal("V2 verifier must reject typ=JWT legacy token")
	}

	// wrong audience
	tr := trustedRequestFixture()
	tr.Nonce = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	badAudCfg := VerifyConfig{
		Issuer: "ai-orchestrator", Audience: "wrong-audience",
		PublicKeys: map[string]ed25519.PublicKey{KeyID(signPub): signPub},
		ReplayCache: NewReplayCache(100), ClockSkew: 30 * time.Second,
	}
	goodToken, _ := SignTrustedRequestContextV2(tr, signPriv)
	if _, err := VerifyTrustedRequestContextV2(goodToken, badAudCfg, time.Now()); err != ErrWrongAudience {
		t.Fatalf("expected ErrWrongAudience, got %v", err)
	}
}
