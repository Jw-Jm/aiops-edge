// graph-load-generator creates a deterministic, ontology-valid HugeGraph
// fixture for the production validation gate. It is a validation utility, not
// a runtime data path; the query-api remains the only production graph owner.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	graph "github.com/observability-platform/ai-apm-query-go/internal/graph"
)

type report struct {
	Vertices      int                `json:"vertices"`
	Edges         int                `json:"edges"`
	BatchSize     int                `json:"batch_size"`
	Concurrency   int                `json:"concurrency"`
	TenantID      string             `json:"tenant_id"`
	ClusterID     string             `json:"cluster_id"`
	AnchorUID     string             `json:"anchor_uid"`
	TargetUID     string             `json:"target_uid"`
	BatchMutation map[string]float64 `json:"batch_mutation,omitempty"`
	Loaded        bool               `json:"loaded"`
	DurationMS    int64              `json:"duration_ms"`
}

func main() {
	vertexCount := flag.Int("vertices", envInt("GRAPH_LOAD_VERTICES", 200000), "number of vertices")
	edgeCount := flag.Int("edges", envInt("GRAPH_LOAD_EDGES", 1000000), "number of edges")
	batchSize := flag.Int("batch-size", envInt("GRAPH_LOAD_BATCH_SIZE", 500), "mutation batch size")
	tenantID := flag.String("tenant-id", envString("GRAPH_LOAD_TENANT_ID", "load-test-tenant"), "tenant id")
	clusterID := flag.String("cluster-id", envString("GRAPH_LOAD_CLUSTER_ID", "load-test-cluster"), "cluster id")
	load := flag.Bool("load", true, "write the fixture to HugeGraph")
	benchmarkIterations := flag.Int("batch-benchmark-iterations", envInt("GRAPH_LOAD_BATCH_BENCHMARK_ITERATIONS", 20), "batch mutation latency samples")
	concurrency := flag.Int("concurrency", envInt("GRAPH_LOAD_CONCURRENCY", 4), "parallel fixture mutation workers")
	flag.Parse()
	if *vertexCount < 2 || *edgeCount < 1 || *batchSize < 1 || *batchSize > 500 || *benchmarkIterations < 0 || *concurrency < 1 || *concurrency > 32 {
		fatal("vertices >= 2, edges >= 1, batch-size 1..500, concurrency 1..32 and non-negative benchmark iterations are required")
	}

	result := report{Vertices: *vertexCount, Edges: *edgeCount, BatchSize: *batchSize, Concurrency: *concurrency,
		TenantID: *tenantID, ClusterID: *clusterID, AnchorUID: vertexUID(0), TargetUID: vertexUID(1), Loaded: false}
	if !*load {
		writeReport(result)
		return
	}

	baseURL := os.Getenv("HUGEGRAPH_URL")
	graphspace := envString("HUGEGRAPH_GRAPHSPACE", "DEFAULT")
	graphName := envString("HUGEGRAPH_GRAPH", "aiops")
	if baseURL == "" {
		fatal("HUGEGRAPH_URL is required when --load=true")
	}
	client, err := graph.NewHugeGraphClient(baseURL, graphspace, graphName, os.Getenv("HUGEGRAPH_USERNAME"), os.Getenv("HUGEGRAPH_PASSWORD"), 10*time.Second, 30*time.Second)
	if err != nil {
		fatal(err.Error())
	}
	ctx := context.Background()
	started := time.Now()
	if err := loadBatches(ctx, *vertexCount, *batchSize, *concurrency, func(start, end int) error {
		vertices := make([]graph.Entity, 0, end-start)
		for index := start; index < end; index++ {
			vertices = append(vertices, graph.Entity{
				EntityUID: vertexUID(index), EntityType: "service", TenantID: *tenantID, ClusterID: *clusterID,
				Name: fmt.Sprintf("graph-load-service-%06d", index), NameKey: fmt.Sprintf("graph-load-service-%06d", index),
				Source: "graph-load-test", Status: "active", Confidence: 1, FirstSeenMS: 1, LastSeenMS: 1,
				Generation: 1, AttrsVersion: 1, Attrs: map[string]interface{}{"load_test": true},
			})
		}
		if err := client.PutVerticesBatch(ctx, vertices); err != nil {
			return fmt.Errorf("load vertices [%d,%d): %w", start, end, err)
		}
		return nil
	}); err != nil {
		fatal(err.Error())
	}
	if err := loadBatches(ctx, *edgeCount, *batchSize, *concurrency, func(start, end int) error {
		edges := make([]graph.Edge, 0, end-start)
		for index := start; index < end; index++ {
			source, target := edgeEndpoints(index, *vertexCount)
			edges = append(edges, graph.Edge{
				EdgeUID: fmt.Sprintf("loadtest:edge:%09d", index), SourceUID: vertexUID(source), TargetUID: vertexUID(target),
				RelationType: "DEPENDS_ON", TenantID: *tenantID, ClusterID: *clusterID, Status: "active", Source: "graph-load-test",
				Confidence: 1, Generation: 1, FirstSeenMS: 1, LastSeenMS: 1, ValidFromMS: 1,
				PropagatesFailure: true, CandidateDirection: "OUT", ImpactDirection: "OUT", AttrsVersion: 1,
			})
		}
		if err := client.PutEdgesBatch(ctx, edges); err != nil {
			return fmt.Errorf("load edges [%d,%d): %w", start, end, err)
		}
		return nil
	}); err != nil {
		fatal(err.Error())
	}
	result.Loaded = true
	result.DurationMS = time.Since(started).Milliseconds()
	if *benchmarkIterations > 0 {
		result.BatchMutation = benchmarkBatch(ctx, client, *tenantID, *clusterID, *batchSize, *benchmarkIterations)
	}
	writeReport(result)
}

// loadBatches parallelizes only the validation fixture writer. Runtime graph
// projection remains serialized by its lease/worker contract; the load gate
// needs concurrency because HugeGraph's REST edge endpoint is the bottleneck
// on a single-node local cluster.
func loadBatches(ctx context.Context, total, batchSize, concurrency int, load func(start, end int) error) error {
	if total <= 0 {
		return nil
	}
	workerCount := min(concurrency, (total+batchSize-1)/batchSize)
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	worker := func() {
		defer wg.Done()
		for start := range jobs {
			if workCtx.Err() != nil {
				return
			}
			if err := load(start, min(start+batchSize, total)); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				mu.Unlock()
				return
			}
		}
	}
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go worker()
	}
	for start := 0; start < total; start += batchSize {
		select {
		case jobs <- start:
		case <-workCtx.Done():
			break
		}
		if workCtx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	return firstErr
}

func benchmarkBatch(ctx context.Context, client *graph.HugeGraphClient, tenantID, clusterID string, batchSize, iterations int) map[string]float64 {
	samples := make([]int64, 0, iterations)
	vertices := make([]graph.Entity, 0, batchSize)
	for index := 0; index < batchSize; index++ {
		vertices = append(vertices, graph.Entity{EntityUID: vertexUID(index), EntityType: "service", TenantID: tenantID, ClusterID: clusterID,
			Name: fmt.Sprintf("graph-load-service-%06d", index), NameKey: fmt.Sprintf("graph-load-service-%06d", index), Source: "graph-load-test",
			Status: "active", Confidence: 1, Generation: 1, AttrsVersion: 1})
	}
	for index := 0; index < iterations; index++ {
		started := time.Now()
		if err := client.PutVerticesBatch(ctx, vertices); err != nil {
			fatal(fmt.Sprintf("batch benchmark: %v", err))
		}
		samples = append(samples, time.Since(started).Microseconds())
	}
	return map[string]float64{"iterations": float64(iterations), "p95_ms": float64(percentile(samples, .95)) / 1000}
}

func percentile(samples []int64, p float64) int64 {
	if len(samples) == 0 {
		return 0
	}
	// Iterations are intentionally small; sort without pulling another package
	// into this tiny validation binary.
	for i := 0; i < len(samples); i++ {
		for j := i + 1; j < len(samples); j++ {
			if samples[j] < samples[i] {
				samples[i], samples[j] = samples[j], samples[i]
			}
		}
	}
	index := int(float64(len(samples))*p) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(samples) {
		index = len(samples) - 1
	}
	return samples[index]
}

func vertexUID(index int) string { return fmt.Sprintf("loadtest:vertex:%06d", index) }

func edgeEndpoints(index, vertexCount int) (int, int) {
	source := index % vertexCount
	// Keep endpoint pairs unique (HugeGraph's native edge identity is
	// outV+label+inV); five deterministic fan-out buckets cover 1M edges
	// without silently overwriting duplicates.
	bucket := index / vertexCount
	target := (source + 1 + bucket*7919) % vertexCount
	if target == source {
		target = (target + 1) % vertexCount
	}
	return source, target
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
func envString(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func envInt(key string, fallback int) int {
	if value, err := strconv.Atoi(os.Getenv(key)); err == nil && value > 0 {
		return value
	}
	return fallback
}
func fatal(message string) { fmt.Fprintln(os.Stderr, "graph-load-generator:", message); os.Exit(1) }
func writeReport(value report) {
	encoded, _ := json.MarshalIndent(value, "", "  ")
	fmt.Println(string(encoded))
}
