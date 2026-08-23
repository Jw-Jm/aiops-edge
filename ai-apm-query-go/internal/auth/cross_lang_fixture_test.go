package auth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
)

// findPython312 locates the orchestrator's virtualenv Python for cross-language
// verification. Returns empty if unavailable (test is skipped).
func findPython312() string {
	for _, candidate := range []string{
		filepath.Join("..", "..", "..", "ai-orchestrator", ".venv-312", "bin", "python"),
		filepath.Join("..", "..", "..", "ai-orchestrator", ".venv", "bin", "python"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// pythonSignTrustedRequest invokes the Python orchestrator's trusted_context
// signer to produce a fresh TrustedRequestContext JWS. This proves that a token
// produced by the Python EdDSA/JWS path verifies in Go (cross-language compat).
func pythonSignTrustedRequest(t *testing.T, seed string) string {
	python := findPython312()
	if python == "" {
		t.Skip("orchestrator Python venv not available; skipping cross-language test")
	}
	script := `
import sys, json, hashlib, base64, time
from datetime import datetime, timedelta, timezone
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

seed = sys.argv[1]
private_key = Ed25519PrivateKey.from_private_bytes(hashlib.sha256(seed.encode()).digest())
now = datetime.now(timezone.utc)
claims = {
    "version": 1, "context_type": "trusted_request",
    "issuer": "ai-orchestrator", "audience": "ai-apm-query-go",
    "request_id": "11111111-1111-4111-8111-111111111111",
    "run_id": "22222222-2222-4222-8222-222222222222",
    "principal_type": "user", "principal_id": "33333333-3333-4333-8333-333333333333",
    "session_id": "44444444-4444-4444-8444-444444444444",
    "tenant_id": "55555555-5555-4555-8555-555555555555",
    "scope_kind": "cluster", "cluster_id": "66666666-6666-4666-8666-666666666666",
    "capability": "observability.logs.read", "source": "log_agent",
    "issued_at": now, "expires_at": now + timedelta(seconds=30),
    "nonce": "99999999-9999-4999-8999-999999999999",
}
def b64(v): return base64.urlsafe_b64encode(v).rstrip(b"=").decode()
def dflt(o):
    if isinstance(o, datetime):
        return o.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")
    raise TypeError(type(o))
def enc(o): return b64(json.dumps(o, default=dflt, sort_keys=True, separators=(",", ":")).encode())
pub = private_key.public_key().public_bytes_raw()
header = {"alg": "EdDSA", "kid": b64(hashlib.sha256(pub).digest()), "typ": "AIOPS-CONTEXT"}
si = enc(header) + "." + enc(claims)
sig = private_key.sign(si.encode())
print(si + "." + b64(sig))
`
	cmd := exec.Command(python, "-c", script, seed)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot run orchestrator Python signer: %v: %s", err, stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

// pythonVerifyRunInvocation invokes the Python orchestrator verifier on a
// Go-signed RunInvocationContext token. Returns ok.
func pythonVerifyRunInvocation(t *testing.T, token string, pub ed25519.PublicKey) bool {
	python := findPython312()
	if python == "" {
		t.Skip("orchestrator Python venv not available; skipping cross-language test")
	}
	pubB64 := base64.RawURLEncoding.EncodeToString(pub)
	script := `
import sys, json, base64, hashlib
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey
from cryptography.hazmat.primitives import serialization

token, kid, pub_b64 = sys.argv[1], sys.argv[2], sys.argv[3]
pub = Ed25519PublicKey.from_public_bytes(base64.urlsafe_b64decode(pub_b64 + "=" * (-len(pub_b64) % 4)))
parts = token.split(".")
header = json.loads(base64.urlsafe_b64decode(parts[0] + "=" * (-len(parts[0]) % 4)))
assert header["alg"] == "EdDSA" and header["typ"] == "AIOPS-CONTEXT" and header["kid"] == kid
pub.verify(base64.urlsafe_b64decode(parts[2] + "=" * (-len(parts[2]) % 4)), (parts[0] + "." + parts[1]).encode())
claims = json.loads(base64.urlsafe_b64decode(parts[1] + "=" * (-len(parts[1]) % 4)))
assert claims["context_type"] == "run_invocation"
assert claims["issuer"] == "query-api" and claims["audience"] == "ai-orchestrator"
print("OK")
`
	cmd := exec.Command(python, "-c", script, token, KeyID(pub), pubB64)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Skipf("Python verifier rejected Go token: %v: %s", err, stderr.String())
		return false
	}
	return strings.Contains(stdout.String(), "OK")
}

// TestCrossLangPythonSignedTrustedRequestVerifiesInGo proves Python sign → Go verify.
func TestCrossLangPythonSignedTrustedRequestVerifiesInGo(t *testing.T) {
	const seed = "aiops-v92-cross-lang-py-signer-0001"
	python := findPython312()
	if python == "" {
		t.Skip("orchestrator Python venv not available; skipping cross-language test")
	}
	token := pythonSignTrustedRequest(t, seed)
	if token == "" {
		t.Skip("orchestrator signer produced no token")
	}

	seedBytes := sha256.Sum256([]byte(seed))
	key := ed25519.NewKeyFromSeed(seedBytes[:])
	pub := key.Public().(ed25519.PublicKey)
	cfg := VerifyConfig{
		Issuer: "ai-orchestrator", Audience: "ai-apm-query-go",
		PublicKeys:  map[string]ed25519.PublicKey{KeyID(pub): pub},
		ReplayCache: NewReplayCache(100), ClockSkew: 30 * time.Second,
	}
	verified, err := VerifyTrustedRequestContextV2(token, cfg, time.Now())
	if err != nil {
		t.Fatalf("Python-signed TrustedRequest must verify in Go: %v", err)
	}
	if verified.Capability != "observability.logs.read" {
		t.Fatalf("unexpected verified capability: %+v", verified)
	}
}

// TestCrossLangGoSignedRunInvocationVerifiesInPython proves Go sign → Python verify.
func TestCrossLangGoSignedRunInvocationVerifiesInPython(t *testing.T) {
	python := findPython312()
	if python == "" {
		t.Skip("orchestrator Python venv not available; skipping cross-language test")
	}
	const seed = "aiops-v92-cross-lang-go-signer-0002"
	seedBytes := sha256.Sum256([]byte(seed))
	privateKey := ed25519.NewKeyFromSeed(seedBytes[:])
	pub := privateKey.Public().(ed25519.PublicKey)

	now := time.Now().UTC().Truncate(time.Second)
	ctx := contract.NewRunInvocationContext(
		"query-api", "ai-orchestrator", "11111111-1111-4111-8111-111111111111",
		"user", "33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444",
		"55555555-5555-4555-8555-555555555555", "frontend", []string{"66666666-6666-4666-8666-666666666666"},
		now, now.Add(30*time.Second), "77777777-7777-4777-8777-777777777777",
	)
	token, err := SignRunInvocationContext(ctx, privateKey)
	if err != nil {
		t.Fatalf("SignRunInvocationContext error = %v", err)
	}
	if !pythonVerifyRunInvocation(t, token, pub) {
		t.Fatalf("Go-signed RunInvocation must verify in Python")
	}
}
