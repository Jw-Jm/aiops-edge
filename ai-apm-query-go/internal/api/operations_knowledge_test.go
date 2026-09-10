package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

func TestKnowledgePathSeparatesCollectionActionsAndResourceActions(t *testing.T) {
	cluster, id, action, ok := knowledgePath("/api/v1/clusters/cluster-a/knowledge")
	if !ok || cluster != "cluster-a" || id != "" || action != "" {
		t.Fatalf("collection path parsed as %q %q %q %v", cluster, id, action, ok)
	}
	cluster, id, action, ok = knowledgePath("/api/v1/clusters/cluster-a/knowledge/k-1/review")
	if !ok || cluster != "cluster-a" || id != "k-1" || action != "review" {
		t.Fatalf("review path parsed as %q %q %q %v", cluster, id, action, ok)
	}
	cluster, id, action, ok = knowledgePath("/api/v1/clusters/cluster-a/knowledge/index-status")
	if !ok || cluster != "cluster-a" || id != "" || action != "index-status" {
		t.Fatalf("index status path parsed as %q %q %q %v", cluster, id, action, ok)
	}
	cluster, id, action, ok = knowledgePath("/api/v1/clusters/cluster-a/knowledge/search")
	if !ok || cluster != "cluster-a" || id != "" || action != "search" {
		t.Fatalf("search path parsed as %q %q %q %v", cluster, id, action, ok)
	}
}

func TestKnowledgeRouterFailsClosedOnCrossClusterScope(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/cluster-b/knowledge", nil)
	req = withAuthorizationContext(req, AuthorizationContext{UserID: "user-1", TenantID: "tenant-1", ActiveClusterID: "cluster-a", SessionID: "session-1"})
	rec := httptest.NewRecorder()
	(&Handler{}).OperationsKnowledgeRouter(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross cluster status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestKnowledgeScopeConstantsMatchStoreContract(t *testing.T) {
	if store.KnowledgePlatformCommon != "platform_common" || store.KnowledgeCluster != "cluster" || store.KnowledgePendingReview != "pending_review" {
		t.Fatal("knowledge scope/status contract drifted")
	}
}
