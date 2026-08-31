package bootstrap

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/observability-platform/ai-apm-query-go/internal/api"
	trustedauth "github.com/observability-platform/ai-apm-query-go/internal/auth"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
	"github.com/observability-platform/ai-apm-query-go/internal/store/migrations"
)

type Mode string

const (
	ModeHTTP        Mode = "http"
	ModeRunDispatch Mode = "run-dispatch"
	ModeAlertEval   Mode = "alert-eval"
)

type ModePlan struct {
	StartHTTP           bool
	StartRunDispatch    bool
	StartActionDispatch bool
	StartToolReconcile  bool
	StartAlertEval      bool
	StartLogShipper     bool
}

type Options struct {
	Mode           Mode
	Port           int
	ClickHouseHost string
	ClickHousePort int
}

func AddClickHouseFlags(fs *flag.FlagSet, defaultHost string, defaultPort int) (*string, *int) {
	chHost := fs.String("ch-host", defaultHost, "ClickHouse host")
	chPort := fs.Int("ch-port", defaultPort, "ClickHouse HTTP port")
	return chHost, chPort
}

func ParseMode(raw string) (Mode, error) {
	switch Mode(strings.TrimSpace(raw)) {
	case ModeHTTP:
		return ModeHTTP, nil
	case ModeRunDispatch:
		return ModeRunDispatch, nil
	case ModeAlertEval:
		return ModeAlertEval, nil
	default:
		return "", fmt.Errorf("invalid runtime mode %q", raw)
	}
}

func PlanForMode(mode Mode) (ModePlan, error) {
	switch mode {
	case ModeHTTP:
		return ModePlan{StartHTTP: true, StartLogShipper: true}, nil
	case ModeRunDispatch:
		return ModePlan{StartRunDispatch: true, StartActionDispatch: true, StartToolReconcile: true}, nil
	case ModeAlertEval:
		return ModePlan{StartAlertEval: true}, nil
	default:
		return ModePlan{}, fmt.Errorf("unsupported runtime mode %q", mode)
	}
}

func Run(ctx context.Context, opts Options) error {
	plan, err := PlanForMode(opts.Mode)
	if err != nil {
		return err
	}
	db, err := RequireDatabase(store.GetDB)
	if err != nil {
		return fmt.Errorf("query runtime startup blocked: %w", err)
	}

	chHost, chPort := clickHouseConfig(opts.ClickHouseHost, opts.ClickHousePort)

	if err := migrations.RequireCurrent(db); err != nil {
		return fmt.Errorf("schema not ready (read-only checksum check): %w", err)
	}
	if err := store.EnsureBootstrapData(db); err != nil {
		return fmt.Errorf("bootstrap data: %w", err)
	}

	handler := api.NewHandler(chHost, chPort)
	config, err := TrustedContextVerifyConfigFromEnv()
	if err != nil {
		return fmt.Errorf("internal signed-context authorization is required: %w", err)
	}
	api.ConfigureInternalRequestVerifier(config)
	issuer, err := runInvocationIssuerFromEnv()
	if err != nil {
		return fmt.Errorf("query-to-orchestrator RunInvocation issuer is required: %w", err)
	}
	api.ConfigureRunInvocationIssuer(issuer)
	if err := api.ConfigureActionExecutionClient(
		os.Getenv("AI_ACTION_EXECUTOR_URL"),
		os.Getenv("AI_ACTION_EXECUTOR_SIGNING_KEY"),
		os.Getenv("EXECUTOR_TOKEN"),
	); err != nil {
		if strings.EqualFold(strings.TrimSpace(os.Getenv("EXECUTION_MODE")), "approved") {
			return fmt.Errorf("approved action execution configuration is invalid: %w", err)
		}
		log.Printf("ai-action-executor client unavailable (execution remains fail-closed): %v", err)
	}
	if vmURL := os.Getenv("VICTORIA_METRICS_URL"); vmURL != "" {
		handler.SetVMURL(vmURL)
	}
	if plan.StartRunDispatch {
		go handler.RunDispatchLoop(ctx)
	}
	if plan.StartActionDispatch {
		go handler.RunActionDispatchLoop(ctx)
	}
	if plan.StartToolReconcile {
		go handler.RunToolReconcilerLoop(ctx, 30*time.Second)
	}
	if plan.StartAlertEval {
		api.SetAlertCH(handler)
		handler.StartAlertEvaluation()
	}
	if err := seedBootstrapData(db); err != nil {
		return err
	}
	api.InitK8sRules()

	var server *http.Server
	if plan.StartHTTP {
		server = newHTTPServer(handler, opts.Port)
		if err := configureMTLSServer(server); err != nil {
			return fmt.Errorf("configure internal mTLS: %w", err)
		}
		go handler.StartLogShipper()
		go func() {
			log.Printf("Query API listening on :%d", opts.Port)
			if err := listenHTTP(server); err != nil && err != http.ErrServerClosed {
				log.Fatalf("server error: %v", err)
			}
		}()
	} else {
		log.Printf("Query runtime running in mode=%s (no public HTTP server)", opts.Mode)
	}

	<-ctx.Done()
	log.Printf("query runtime mode=%s shutting down", opts.Mode)
	if server != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful shutdown error: %v", err)
		}
	}
	return nil
}

func clickHouseConfig(defaultHost string, defaultPort int) (string, int) {
	host := defaultHost
	if envHost := strings.TrimSpace(os.Getenv("CLICKHOUSE_HOST")); envHost != "" {
		host = envHost
	}
	port := defaultPort
	if envPort := strings.TrimSpace(os.Getenv("CLICKHOUSE_PORT")); envPort != "" {
		fmt.Sscanf(envPort, "%d", &port)
	}
	return host, port
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func TrustedContextVerifyConfigFromEnv() (trustedauth.VerifyConfig, error) {
	serviceToken := strings.TrimSpace(os.Getenv("INTERNAL_TOKEN"))
	issuer := strings.TrimSpace(os.Getenv("TRUSTED_CONTEXT_ISSUER"))
	rawKeys := strings.TrimSpace(os.Getenv("TRUSTED_CONTEXT_PUBLIC_KEYS"))
	if serviceToken == "" || issuer == "" || rawKeys == "" {
		return trustedauth.VerifyConfig{}, fmt.Errorf("internal signed-context configuration is incomplete")
	}
	publicKeys := make(map[string]ed25519.PublicKey)
	for _, rawKey := range strings.Split(rawKeys, ",") {
		encoded := strings.TrimSpace(rawKey)
		publicKey, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil || len(publicKey) != ed25519.PublicKeySize {
			return trustedauth.VerifyConfig{}, fmt.Errorf("invalid trusted-context public key")
		}
		key := ed25519.PublicKey(publicKey)
		publicKeys[trustedauth.KeyID(key)] = key
	}
	return trustedauth.VerifyConfig{
		Audience:     "ai-apm-query-go",
		Issuer:       issuer,
		PublicKeys:   publicKeys,
		ServiceToken: serviceToken,
		ReplayCache:  api.NewMySQLReplayCache(issuer, "ai-apm-query-go"),
	}, nil
}

func runInvocationIssuerFromEnv() (*trustedauth.RunInvocationIssuer, error) {
	encodedKey := strings.TrimSpace(os.Getenv("QUERY_TO_ORCHESTRATOR_SIGNING_KEY"))
	serviceToken := strings.TrimSpace(os.Getenv("QUERY_TO_ORCHESTRATOR_TOKEN"))
	if encodedKey == "" || serviceToken == "" {
		return nil, fmt.Errorf("query-to-orchestrator signing key or service token is empty")
	}
	privateKey, err := trustedauth.DecodePrivateKey(encodedKey)
	if err != nil {
		return nil, err
	}
	return trustedauth.NewRunInvocationIssuer(privateKey, serviceToken)
}

func RequireDatabase(getter func() *sql.DB) (*sql.DB, error) {
	if getter == nil {
		return nil, fmt.Errorf("mysql getter is not configured")
	}
	db := getter()
	if db == nil {
		return nil, fmt.Errorf("mysql unavailable")
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("mysql ping failed: %w", err)
	}
	return db, nil
}

func canonicalBootstrapConfigFromEnv() (store.CanonicalBootstrapConfig, error) {
	cfg := store.CanonicalBootstrapConfig{
		TenantID:              strings.TrimSpace(os.Getenv("AIOPS_SYSTEM_TENANT_ID")),
		TenantName:            strings.TrimSpace(os.Getenv("AIOPS_SYSTEM_TENANT_NAME")),
		ClusterID:             strings.TrimSpace(os.Getenv("AIOPS_SYSTEM_CLUSTER_ID")),
		ClusterSlug:           strings.TrimSpace(os.Getenv("AIOPS_SYSTEM_CLUSTER_SLUG")),
		ClusterName:           strings.TrimSpace(os.Getenv("AIOPS_SYSTEM_CLUSTER_NAME")),
		ClusterEnvironment:    strings.TrimSpace(os.Getenv("AIOPS_SYSTEM_CLUSTER_ENVIRONMENT")),
		ClusterRegion:         strings.TrimSpace(os.Getenv("AIOPS_SYSTEM_CLUSTER_REGION")),
		ClusterCredentialRef:  strings.TrimSpace(os.Getenv("AIOPS_SYSTEM_CLUSTER_CREDENTIAL_REF")),
		KubernetesIdentityUID: strings.TrimSpace(os.Getenv("AIOPS_SYSTEM_CLUSTER_IDENTITY_UID")),
	}
	if cfg.TenantName == "" {
		cfg.TenantName = "AIOps Tenant"
	}
	if cfg.ClusterEnvironment == "" {
		cfg.ClusterEnvironment = "local"
	}
	if cfg.ClusterRegion == "" {
		cfg.ClusterRegion = "local"
	}
	if cfg.ClusterName == "" {
		cfg.ClusterName = cfg.ClusterSlug
	}
	if err := cfg.Validate(); err != nil {
		return store.CanonicalBootstrapConfig{}, fmt.Errorf("canonical bootstrap configuration is missing or invalid: %w", err)
	}
	return cfg, nil
}

func seedBootstrapData(db *sql.DB) error {
	adminPW := firstNonEmpty(os.Getenv("ADMIN_INITIAL_PASSWORD"), os.Getenv("ADMIN_PASSWORD"))
	if adminPW == "" {
		return fmt.Errorf("admin bootstrap: ADMIN_INITIAL_PASSWORD is required")
	}
	adminHash, err := bcrypt.GenerateFromPassword([]byte(adminPW), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("admin password hash: %w", err)
	}
	if err := (&store.UserDAO{}).SeedAdmin(string(adminHash)); err != nil {
		return fmt.Errorf("admin bootstrap: %w", err)
	}
	cfg, err := canonicalBootstrapConfigFromEnv()
	if err != nil {
		return fmt.Errorf("canonical authorization bootstrap: %w", err)
	}
	if err := store.EnsureCanonicalBootstrapData(db, cfg); err != nil {
		return fmt.Errorf("canonical authorization bootstrap: %w", err)
	}
	_ = store.SeedTopologyTypes()
	return nil
}
