package graph

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// NewRepositoryFromEnv constructs the configured graph backend. The default is
// legacy_mysql so existing installations remain readable during migration.
// Non-legacy backends fail closed when HugeGraph credentials/configuration are
// incomplete; there is no silent fallback from a requested HugeGraph backend.
func NewRepositoryFromEnv() (GraphRepository, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("GRAPH_BACKEND")))
	if backend == "" {
		backend = "legacy_mysql"
	}
	switch backend {
	case "legacy_mysql":
		return NewLegacyMySQLRepository(), nil
	case "shadow", "hugegraph":
		client, err := newHugeGraphClientFromEnv()
		if err != nil {
			return nil, graphError(ErrGraphUnavailable, err.Error())
		}
		hugegraph := NewHugeGraphRepository(client)
		if backend == "hugegraph" {
			return hugegraph, nil
		}
		return NewShadowRepository(NewLegacyMySQLRepository(), hugegraph, persistShadowDiff), nil
	default:
		return nil, fmt.Errorf("unsupported GRAPH_BACKEND %q", backend)
	}
}

// NewGraphRepositoryFromEnv is the scheme-facing name; keep the shorter name
// for existing query-api wiring.
func NewGraphRepositoryFromEnv() (Repository, error) { return NewRepositoryFromEnv() }

func newHugeGraphClientFromEnv() (*HugeGraphClient, error) {
	baseURL := strings.TrimSpace(os.Getenv("HUGEGRAPH_URL"))
	graphspace := firstNonEmptyEnv("HUGEGRAPH_GRAPHSPACE", "DEFAULT")
	graphName := firstNonEmptyEnv("HUGEGRAPH_GRAPH", "aiops")
	username := strings.TrimSpace(os.Getenv("HUGEGRAPH_USERNAME"))
	password := strings.TrimSpace(os.Getenv("HUGEGRAPH_PASSWORD"))
	if baseURL == "" || username == "" || password == "" {
		return nil, fmt.Errorf("HUGEGRAPH_URL, HUGEGRAPH_USERNAME and HUGEGRAPH_PASSWORD are required")
	}
	readTimeout := durationEnv("GRAPH_READ_TIMEOUT_MS", 1500*time.Millisecond)
	writeTimeout := durationEnv("GRAPH_WRITE_TIMEOUT_MS", 3*time.Second)
	return NewHugeGraphClient(baseURL, graphspace, graphName, username, password, readTimeout, writeTimeout)
}

func firstNonEmptyEnv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func durationEnv(name string, fallback time.Duration) time.Duration {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return time.Duration(value) * time.Millisecond
}

func persistShadowDiff(diff ShadowDiff) {
	if store.GetDB() == nil {
		return
	}
	_ = (&store.GraphShadowDiffDAO{}).Insert(store.GraphShadowDiffRun{
		DiffRunID: SHA256Parts(diff.TenantID, diff.ScopeClusterID, diff.SampleKind, time.Now().UTC().Format(time.RFC3339Nano)),
		TenantID:  diff.TenantID, ScopeClusterID: diff.ScopeClusterID, SampleKind: diff.SampleKind,
		SampleCount: diff.SampleCount, MismatchCount: diff.MismatchCount, Detail: diff.Detail,
	})
}
