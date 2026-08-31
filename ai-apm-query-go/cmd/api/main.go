package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	trustedauth "github.com/observability-platform/ai-apm-query-go/internal/auth"
	"github.com/observability-platform/ai-apm-query-go/internal/bootstrap"
)

func main() {
	fs := flag.NewFlagSet("query-api-http", flag.ExitOnError)
	port := fs.Int("port", 8080, "HTTP server port")
	chHost, chPort := bootstrap.AddClickHouseFlags(fs, "clickhouse.observability.svc.cluster.local", 8123)
	_ = fs.Parse(os.Args[1:])

	if err := bootstrap.Run(contextWithSignals(), bootstrap.Options{
		Mode:           bootstrap.ModeHTTP,
		Port:           *port,
		ClickHouseHost: *chHost,
		ClickHousePort: *chPort,
	}); err != nil {
		log.Fatalf("query-api-http failed: %v", err)
	}
}

func contextWithSignals() context.Context {
	ctx, _ := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	return ctx
}

func requireDatabase(getter func() *sql.DB) (*sql.DB, error) {
	return bootstrap.RequireDatabase(getter)
}

func trustedContextVerifyConfigFromEnv() (trustedauth.VerifyConfig, error) {
	return bootstrap.TrustedContextVerifyConfigFromEnv()
}
