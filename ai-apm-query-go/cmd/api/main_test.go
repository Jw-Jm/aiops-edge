package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"

	trustedauth "github.com/observability-platform/ai-apm-query-go/internal/auth"
)

func TestTrustedContextVerifyConfigFromEnvAcceptsRotatingPublicKeys(t *testing.T) {
	first := make(ed25519.PublicKey, ed25519.PublicKeySize)
	second := make(ed25519.PublicKey, ed25519.PublicKeySize)
	for index := range first {
		first[index] = byte(index + 1)
		second[index] = byte(index + 2)
	}
	t.Setenv("INTERNAL_TOKEN", "test-only-service-token")
	t.Setenv("TRUSTED_CONTEXT_ISSUER", "ai-orchestrator")
	t.Setenv("TRUSTED_CONTEXT_PUBLIC_KEYS", base64.RawURLEncoding.EncodeToString(first)+","+base64.RawURLEncoding.EncodeToString(second))

	config, err := trustedContextVerifyConfigFromEnv()
	if err != nil {
		t.Fatalf("trustedContextVerifyConfigFromEnv() error = %v", err)
	}
	if config.Audience != "ai-apm-query-go" || config.Issuer != "ai-orchestrator" || config.ServiceToken != "test-only-service-token" {
		t.Fatalf("trustedContextVerifyConfigFromEnv() = %+v, want configured audience, issuer, and service credential", config)
	}
	if len(config.PublicKeys) != 2 || config.PublicKeys[trustedauth.KeyID(first)] == nil || config.PublicKeys[trustedauth.KeyID(second)] == nil {
		t.Fatalf("trustedContextVerifyConfigFromEnv() did not retain both rotation keys")
	}
}
