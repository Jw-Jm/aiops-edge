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
	VertexTypes   map[string]int     `json:"vertex_types"`
	RelationTypes map[string]int     `json:"relation_types"`
	Loaded        bool               `json:"loaded"`
	DurationMS    int64              `json:"duration_ms"`
}

type fixtureRange struct {
	EntityType string
	Start      int
	End        int
}

// The benchmark fixture deliberately mirrors the production ontology instead
// of loading one million service-shaped vertices.  The minimum proportions
// are part of the capacity contract: 20k services, 100k Pod/Container, 10k
// VM/VMI and 10k Node/Server/Component entities.
var fixtureRanges = []fixtureRange{
	{EntityType: "service", Start: 0, End: 20_000},
	{EntityType: "pod", Start: 20_000, End: 70_000},
	{EntityType: "container", Start: 70_000, End: 120_000},
	{EntityType: "vm", Start: 120_000, End: 125_000},
	{EntityType: "vmi", Start: 125_000, End: 130_000},
	{EntityType: "k8s_node", Start: 130_000, End: 134_000},
	{EntityType: "physical_server", Start: 134_000, End: 137_000},
	{EntityType: "dimm", Start: 137_000, End: 140_000},
	{EntityType: "middleware", Start: 140_000, End: 165_900},
	{EntityType: "application", Start: 165_900, End: 170_900},
	{EntityType: "business", Start: 170_900, End: 171_900},
	{EntityType: "k8s_service", Start: 171_900, End: 174_900},
	{EntityType: "endpoint_slice", Start: 174_900, End: 177_900},
	{EntityType: "namespace", Start: 177_900, End: 179_900},
	{EntityType: "k8s_cluster", Start: 179_900, End: 180_000},
	{EntityType: "deployment", Start: 180_000, End: 190_000},
	{EntityType: "pvc", Start: 190_000, End: 200_000},
}

type edgeSpec struct {
	RelationType string
	SourceType   string
	TargetType   string
	Count        int
}

var fixtureEdgeSpecs = []edgeSpec{
	{RelationType: "DEPENDS_ON", SourceType: "service", TargetType: "service", Count: 200_000},
	{RelationType: "DEPENDS_ON", SourceType: "service", TargetType: "middleware", Count: 110_000},
	{RelationType: "BELONGS_TO", SourceType: "service", TargetType: "application", Count: 100_000},
	{RelationType: "BELONGS_TO", SourceType: "application", TargetType: "business", Count: 50_000},
	{RelationType: "RUNS_ON", SourceType: "pod", TargetType: "k8s_node", Count: 200_000},
	{RelationType: "RUNS_ON", SourceType: "vmi", TargetType: "k8s_node", Count: 20_000},
	{RelationType: "HOSTS", SourceType: "physical_server", TargetType: "k8s_node", Count: 30_000},
	{RelationType: "HAS_COMPONENT", SourceType: "physical_server", TargetType: "dimm", Count: 30_000},
	{RelationType: "INSTANCE_OF", SourceType: "vmi", TargetType: "vm", Count: 50_000},
	{RelationType: "BACKED_BY", SourceType: "endpoint_slice", TargetType: "pod", Count: 100_000},
	{RelationType: "TARGETS", SourceType: "k8s_service", TargetType: "endpoint_slice", Count: 50_000},
	{RelationType: "OWNS", SourceType: "deployment", TargetType: "pod", Count: 30_000},
	{RelationType: "USES_VOLUME", SourceType: "pod", TargetType: "pvc", Count: 20_000},
	{RelationType: "CONTAINS", SourceType: "k8s_cluster", TargetType: "namespace", Count: 10_000},
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
		TenantID: *tenantID, ClusterID: *clusterID, AnchorUID: vertexUID(0), TargetUID: vertexUID(1), Loaded: false,
		VertexTypes: fixtureTypeCounts(*vertexCount), RelationTypes: fixtureRelationCounts(*edgeCount)}
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
			vertices = append(vertices, fixtureEntity(index, *tenantID, *clusterID))
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
			spec, local := fixtureEdgeSpec(index)
			source, target := edgeEndpointsForSpec(spec, local, *vertexCount)
			edges = append(edges, graph.Edge{
				EdgeUID: fmt.Sprintf("loadtest:edge:%09d", index), SourceUID: vertexUID(source), TargetUID: vertexUID(target),
				RelationType: spec.RelationType, TenantID: *tenantID, ClusterID: *clusterID, Status: "active", Source: "graph-load-test",
				Confidence: 1, Generation: 1, FirstSeenMS: 1, LastSeenMS: 1, ValidFromMS: 1,
				PropagatesFailure: propagationFor(spec.RelationType), CandidateDirection: graph.CandidateDirection(spec.RelationType), ImpactDirection: graph.ImpactDirection(spec.RelationType), AttrsVersion: 1,
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
		vertices = append(vertices, fixtureEntity(index, tenantID, clusterID))
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

func fixtureType(index int) string {
	for _, item := range fixtureRanges {
		if index >= item.Start && index < item.End {
			return item.EntityType
		}
	}
	return "middleware"
}

func fixtureRangeFor(entityType string) fixtureRange {
	for _, item := range fixtureRanges {
		if item.EntityType == entityType {
			return item
		}
	}
	return fixtureRange{EntityType: entityType}
}

func fixtureEntity(index int, tenantID, clusterID string) graph.Entity {
	entityType := fixtureType(index)
	rangeDef := fixtureRangeFor(entityType)
	ordinal := index - rangeDef.Start
	name := fmt.Sprintf("graph-load-%s-%06d", entityType, ordinal)
	namespace := ""
	if entityType == "service" || entityType == "pod" || entityType == "container" || entityType == "k8s_service" {
		namespace = fmt.Sprintf("load-ns-%03d", ordinal%200)
	}
	attrs := map[string]interface{}{"load_test": true, "fixture_entity_type": entityType}
	if entityType == "service" {
		attrs["application_uid"] = fmt.Sprintf("loadtest:application:%04d", ordinal%5000)
		attrs["application_name"] = fmt.Sprintf("load-application-%04d", ordinal%5000)
	}
	return graph.Entity{EntityUID: vertexUID(index), EntityType: entityType, TenantID: tenantID, ClusterID: clusterID,
		Namespace: namespace, Name: name, NameKey: name, Source: "graph-load-test", Status: "active", Confidence: 1,
		FirstSeenMS: 1, LastSeenMS: 1, Generation: 1, AttrsVersion: 1, Attrs: attrs}
}

func fixtureTypeCounts(vertices int) map[string]int {
	counts := map[string]int{}
	for index := 0; index < vertices; index++ {
		counts[fixtureType(index)]++
	}
	return counts
}

func fixtureRelationCounts(edges int) map[string]int {
	counts := map[string]int{}
	for index := 0; index < edges; index++ {
		spec, _ := fixtureEdgeSpec(index)
		counts[spec.RelationType]++
	}
	return counts
}

func fixtureEdgeSpec(index int) (edgeSpec, int) {
	offset := index
	for _, spec := range fixtureEdgeSpecs {
		if offset < spec.Count {
			return spec, offset
		}
		offset -= spec.Count
	}
	// The default CLI count is exactly one million. For custom larger counts,
	// repeat the final valid relation rather than emitting an invalid edge.
	spec := fixtureEdgeSpecs[len(fixtureEdgeSpecs)-1]
	return spec, offset
}

func edgeEndpointsForSpec(spec edgeSpec, local, vertexCount int) (int, int) {
	sourceRange, targetRange := fixtureRangeFor(spec.SourceType), fixtureRangeFor(spec.TargetType)
	if vertexCount < sourceRange.End {
		sourceRange.End = vertexCount
	}
	if vertexCount < targetRange.End {
		targetRange.End = vertexCount
	}
	if sourceRange.End <= sourceRange.Start || targetRange.End <= targetRange.Start {
		// Small custom dry-runs use the service range only; the default fixture
		// remains the full ontology distribution above.
		sourceRange, targetRange = fixtureRange{EntityType: "service", Start: 0, End: vertexCount}, fixtureRange{EntityType: "service", Start: 0, End: vertexCount}
	}
	sourceCount, targetCount := sourceRange.End-sourceRange.Start, targetRange.End-targetRange.Start
	if sourceCount <= 0 || targetCount <= 0 {
		return 0, 1
	}
	sourceOrdinal := local % sourceCount
	bucket := local / sourceCount
	targetOrdinal := (bucket + sourceOrdinal*7919) % targetCount
	if spec.SourceType == spec.TargetType {
		// Enumerate a per-source fan-out directly. This guarantees no self edge
		// and no duplicate (source,target) pair for every bucket in the fixture.
		targetOrdinal = (sourceOrdinal + bucket + 1) % targetCount
	}
	return sourceRange.Start + sourceOrdinal, targetRange.Start + targetOrdinal
}

func propagationFor(relation string) bool {
	return relation == "DEPENDS_ON" || relation == "RUNS_ON" || relation == "HOSTS" || relation == "HAS_COMPONENT" || relation == "INSTANCE_OF" || relation == "BACKED_BY" || relation == "TARGETS" || relation == "OWNS" || relation == "USES_VOLUME"
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
