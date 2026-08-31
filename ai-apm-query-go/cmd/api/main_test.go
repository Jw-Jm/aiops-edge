package main

import (
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	trustedauth "github.com/observability-platform/ai-apm-query-go/internal/auth"
	"github.com/observability-platform/ai-apm-query-go/internal/bootstrap"
)

func TestRequireDatabaseFailsClosedWhenUnavailable(t *testing.T) {
	if _, err := requireDatabase(func() *sql.DB { return nil }); err == nil {
		t.Fatal("requireDatabase should fail when MySQL is unavailable")
	}
}

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

func TestRuntimeModeHTTPStartsNoBackgroundLoops(t *testing.T) {
	plan, err := bootstrap.PlanForMode(bootstrap.ModeHTTP)
	if err != nil {
		t.Fatalf("PlanForMode(http) error = %v", err)
	}
	if !plan.StartHTTP {
		t.Fatal("http mode should start the public HTTP server")
	}
	if plan.StartRunDispatch || plan.StartAlertEval || plan.StartToolReconcile {
		t.Fatalf("http mode must not start background loops: %+v", plan)
	}
}

func TestParseModeFailsClosedForLegacyAndUnknownRoles(t *testing.T) {
	for _, candidate := range []string{"", "api", "worker", "unknown"} {
		if _, err := bootstrap.ParseMode(candidate); err == nil {
			t.Fatalf("ParseMode(%q) should fail closed", candidate)
		}
	}
}

func TestHelmChartRendersSeparateQueryRuntimeWorkloads(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not installed")
	}
	chartDir := filepath.Join("..", "..", "..", "deploy", "helm", "aiops")
	args := []string{
		"template", "aiops", chartDir,
		"--set-string", "secrets.jwtSecret=test-jwt-secret-012345678901234567890123",
		"--set-string", "secrets.llmEncryptionKey=test-llm-encryption-key-012345678901",
		"--set-string", "secrets.internalToken=test-internal-token-0123456789012345",
		"--set-string", "secrets.orchestratorToQueryToken=test-orch-to-query-token-0123456789",
		"--set-string", "secrets.orchestratorToQuerySigningKey=test-orch-to-query-signing-key",
		"--set-string", "secrets.orchestratorToQueryVerifyKeys=test-orch-to-query-verify-keys",
		"--set-string", "secrets.queryToOrchestratorToken=test-query-to-orch-token-0123456789",
		"--set-string", "secrets.queryToOrchestratorSigningKey=test-query-to-orch-signing-key",
		"--set-string", "secrets.queryToOrchestratorVerifyKeys=test-query-to-orch-verify-keys",
		"--set-string", "secrets.ingestApiKey=test-ingest-api-key-0123456789012345",
		"--set-string", "secrets.clickhousePassword=test-clickhouse-password-0123456789",
		"--set-string", "secrets.mysqlRootPassword=test-mysql-root-password-0123456789",
		"--set-string", "secrets.mysqlAppPassword=test-mysql-app-password-012345678901",
		"--set-string", "secrets.mysqlMigratorPassword=test-mysql-migrator-password-012345",
		"--set-string", "secrets.executorToken=test-executor-token-012345678901234567",
		"--set-string", "secrets.aiActionExecutorSigningKey=test-executor-signing-key",
		"--set-string", "secrets.aiActionExecutorVerifyKeys=test-executor-verify-keys",
		"--set-string", "secrets.hugeGraphPassword=test-hugegraph-password-012345678901",
	}
	cmd := exec.Command("helm", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, output)
	}
	rendered := string(output)
	for _, deploymentName := range []string{
		"name: query-api-http",
		"name: query-run-dispatch",
		"name: query-alert-eval",
	} {
		if !strings.Contains(rendered, deploymentName) {
			t.Fatalf("expected rendered chart to contain %q", deploymentName)
		}
	}
	if strings.Count(rendered, "kind: Service\nmetadata:\n  name: query-api") != 1 {
		t.Fatalf("expected exactly one public query-api service, render was:\n%s", rendered)
	}
	if strings.Contains(rendered, "name: query-run-dispatch\nspec:\n  type:") || strings.Contains(rendered, "name: query-alert-eval\nspec:\n  type:") {
		t.Fatalf("background query runtimes must not render a public Service")
	}
}
