package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	trustedauth "github.com/observability-platform/ai-apm-query-go/internal/auth"
	"github.com/observability-platform/ai-apm-query-go/internal/contract"
)

func newControlPlaneVerifier(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	pub := priv.Public().(ed25519.PublicKey)
	restore := configureInternalRequestVerifier(trustedauth.VerifyConfig{
		Issuer:       "ai-orchestrator",
		Audience:     "ai-apm-query-go",
		PublicKeys:   map[string]ed25519.PublicKey{trustedauth.KeyID(pub): pub},
		ServiceToken: "test-service-token",
		ReplayCache:  trustedauth.NewReplayCache(100),
		ClockSkew:    30 * time.Second,
	})
	t.Cleanup(restore)
	return priv
}

// buildControlPlaneRequest 构造 system principal 的 control-plane 签名请求。
func buildControlPlaneRequest(t *testing.T, priv ed25519.PrivateKey, capability string, mutate func(*contract.TrustedRequestContext)) *http.Request {
	t.Helper()
	now := time.Now().UTC()
	ctx := contract.NewTrustedRequestContext(
		"ai-orchestrator", "ai-apm-query-go", "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", "system",
		"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "", "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "run", "", capability, "control-plane",
		now, now.Add(30*time.Second), "11111111-1111-4111-8111-111111111111",
	)
	if mutate != nil {
		mutate(&ctx)
	}
	token, err := trustedauth.SignTrustedRequestContextV2(ctx, priv)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/control-plane/runs/x/transition", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", "test-service-token")
	req.Header.Set("X-Trusted-Request-Context", token)
	return req
}

func TestAuthorizeControlPlaneAcceptsSystemPrincipal(t *testing.T) {
	priv := newControlPlaneVerifier(t)
	req := buildControlPlaneRequest(t, priv, "control_plane.runs.mutate", nil)
	rctx, err := authorizeInternalControlPlane(req, "control_plane.runs.mutate", "ai-orchestrator")
	if err != nil {
		t.Fatalf("expected authorize, got %v", err)
	}
	if rctx.TenantID != "7ed01afc-cc79-4ecd-8767-a2befa6168ad" {
		t.Fatalf("bad tenant: %s", rctx.TenantID)
	}
}

func TestAuthorizeControlPlaneRejectsUserPrincipal(t *testing.T) {
	priv := newControlPlaneVerifier(t)
	req := buildControlPlaneRequest(t, priv, "control_plane.runs.mutate", func(ctx *contract.TrustedRequestContext) {
		ctx.PrincipalType = "user"
		ctx.SessionID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	})
	if _, err := authorizeInternalControlPlane(req, "control_plane.runs.mutate", "ai-orchestrator"); err == nil {
		t.Fatalf("expected reject for user principal")
	}
}

func TestAuthorizeControlPlaneRejectsWrongCapability(t *testing.T) {
	priv := newControlPlaneVerifier(t)
	req := buildControlPlaneRequest(t, priv, "control_plane.events.append", nil)
	if _, err := authorizeInternalControlPlane(req, "control_plane.runs.mutate", "ai-orchestrator"); err == nil {
		t.Fatalf("expected reject for wrong capability")
	}
}

func TestAuthorizeControlPlaneRejectsWrongIssuer(t *testing.T) {
	priv := newControlPlaneVerifier(t)
	req := buildControlPlaneRequest(t, priv, "control_plane.runs.mutate", nil)
	if _, err := authorizeInternalControlPlane(req, "control_plane.runs.mutate", "query-api"); err == nil {
		t.Fatalf("expected reject for wrong issuer")
	}
}

func TestAuthorizeControlPlaneRejectsMissingToken(t *testing.T) {
	priv := newControlPlaneVerifier(t)
	_ = priv
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/control-plane/runs/x/transition", nil)
	if _, err := authorizeInternalControlPlane(req, "control_plane.runs.mutate", "ai-orchestrator"); err == nil {
		t.Fatalf("expected reject for missing token")
	}
}
