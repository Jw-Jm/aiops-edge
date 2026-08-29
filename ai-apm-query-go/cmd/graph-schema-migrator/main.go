package main

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/graph"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

func main() {
	baseURL := os.Getenv("HUGEGRAPH_URL")
	graphspace := getenv("HUGEGRAPH_GRAPHSPACE", "DEFAULT")
	graphName := getenv("HUGEGRAPH_GRAPH", "aiops")
	username := os.Getenv("HUGEGRAPH_USERNAME")
	password := os.Getenv("HUGEGRAPH_PASSWORD")
	readTimeout := durationEnv("GRAPH_READ_TIMEOUT_MS", 1500*time.Millisecond)
	writeTimeout := durationEnv("GRAPH_WRITE_TIMEOUT_MS", 3*time.Second)
	client, err := graph.NewHugeGraphClient(baseURL, graphspace, graphName, username, password, readTimeout, writeTimeout)
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := client.EnsureGraph(ctx); err != nil {
		log.Fatal(err)
	}
	if err := client.VerifyGraph(ctx); err != nil {
		log.Fatal(err)
	}
	if err := client.EnsureSchema(ctx); err != nil {
		log.Fatal(err)
	}
	manifest, checksum := graph.SchemaManifestV2()
	dao := &store.GraphSchemaStateDAO{}
	if err := dao.Upsert(store.GraphSchemaState{SchemaName: "aiops", SchemaVersion: graph.GraphSchemaVersion, SchemaChecksumSHA256: checksum, Graphspace: graphspace, GraphName: graphName, AppliedBy: "graph-schema-migrator"}); err != nil {
		log.Fatal(err)
	}
	log.Printf("HugeGraph schema aiops v%d applied checksum=%s manifest_bytes=%d", graph.GraphSchemaVersion, checksum, len(manifest))
}

func getenv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func durationEnv(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	ms, err := strconv.Atoi(value)
	if err != nil || ms <= 0 {
		return fallback
	}
	return time.Duration(ms) * time.Millisecond
}
