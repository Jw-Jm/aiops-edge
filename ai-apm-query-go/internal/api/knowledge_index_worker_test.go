package api

import (
	"strings"
	"testing"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

func TestBuildKnowledgeIndexMetadataMarksOnlyCurrentPublishedVersion(t *testing.T) {
	item := store.OperationsKnowledge{KnowledgeID: "k-1", TenantID: "tenant-a", ScopeType: store.KnowledgeCluster, ClusterID: "cluster-a", KnowledgeType: "runbook", Status: store.KnowledgePublished, CurrentVersionID: "v-2"}
	version := store.OperationsKnowledgeVersion{VersionID: "v-2", KnowledgeID: "k-1", ContentSHA256: strings.Repeat("a", 64), SourceRevision: "git:abc"}
	metadata := BuildKnowledgeIndexMetadata(item, version)
	if !metadata.IsCurrent || metadata.Status != "published" || metadata.ScopeType != "cluster" || metadata.ContentChecksum != strings.Repeat("a", 64) {
		t.Fatalf("metadata=%+v", metadata)
	}
}

func TestSanitizeKnowledgeIndexErrorRemovesConnectionDetails(t *testing.T) {
	message := sanitizeKnowledgeIndexError(assertError("POST https://chroma.example/query?token=secret failed password=hidden"))
	if strings.Contains(message, "https://") || strings.Contains(message, "token=") || strings.Contains(message, "password=") {
		t.Fatalf("sanitized error leaked connection details: %q", message)
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

func assertError(message string) error { return errorString(message) }
