package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/observability-platform/ai-apm-query-go/internal/bootstrap"
)

func main() {
	fs := flag.NewFlagSet("query-run-dispatch", flag.ExitOnError)
	chHost, chPort := bootstrap.AddClickHouseFlags(fs, "clickhouse.observability.svc.cluster.local", 8123)
	_ = fs.Parse(os.Args[1:])

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := bootstrap.Run(ctx, bootstrap.Options{
		Mode:           bootstrap.ModeRunDispatch,
		ClickHouseHost: *chHost,
		ClickHousePort: *chPort,
	}); err != nil {
		log.Fatalf("query-run-dispatch failed: %v", err)
	}
}
