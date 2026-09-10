package query

import (
	"context"
	"errors"
	"testing"
)

// fakeKnowledgeBackend 是一个可注入的测试 backend，模拟 Chroma 向量索引 + MinIO Knowledge Object。
type fakeKnowledgeBackend struct {
	hits     []KnowledgeHit
	err      error
	calls    int
	gotQ     string
	gotScope KnowledgeScope
}

func (f *fakeKnowledgeBackend) Search(ctx context.Context, scope KnowledgeScope, query string, topK int) ([]KnowledgeHit, error) {
	f.calls++
	f.gotScope = scope
	f.gotQ = query
	if f.err != nil {
		return nil, f.err
	}
	return f.hits, nil
}

func TestKnowledgeRepoSearch(t *testing.T) {
	be := &fakeKnowledgeBackend{hits: []KnowledgeHit{
		{DocumentID: "doc-1", KnowledgeID: "k-1", VersionID: "v-3", TenantID: "t1", ScopeType: "cluster", ClusterID: "c1", Status: "published", IsCurrent: true, Source: "runbook", Version: "v3", Similarity: 0.92, Applicability: "checkout"},
		{DocumentID: "doc-2", KnowledgeID: "k-2", VersionID: "v-1", TenantID: "t1", ScopeType: "platform_common", Status: "published", IsCurrent: true, Source: "sop", Version: "v1", Similarity: 0.81, Applicability: "checkout"},
	}}
	r := NewKnowledgeRepository(be)
	hits, err := r.Search(context.Background(), KnowledgeScope{TenantID: "t1", ClusterID: "c1"}, "checkout pod crashloop", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
	if hits[0].DocumentID != "doc-1" || hits[0].Similarity != 0.92 || hits[0].Source != "runbook" {
		t.Fatalf("hits[0] = %+v", hits[0])
	}
	if be.gotQ != "checkout pod crashloop" {
		t.Fatalf("backend query = %q", be.gotQ)
	}
	if be.gotScope != (KnowledgeScope{TenantID: "t1", ClusterID: "c1"}) {
		t.Fatalf("backend scope = %+v", be.gotScope)
	}
}

func TestKnowledgeRepoEmptyIsNoData(t *testing.T) {
	be := &fakeKnowledgeBackend{hits: nil}
	r := NewKnowledgeRepository(be)
	_, err := r.Search(context.Background(), KnowledgeScope{TenantID: "t1", ClusterID: "c1"}, "nothing", 5)
	if err == nil {
		t.Fatal("expected no_data for empty knowledge result")
	}
	var qe *QueryError
	if !errors.As(err, &qe) || qe.Code != NoDataCode {
		t.Fatalf("expected no_data, got %v", err)
	}
}

func TestKnowledgeRepoBackendUnavailable(t *testing.T) {
	be := &fakeKnowledgeBackend{err: errors.New("chroma down")}
	r := NewKnowledgeRepository(be)
	_, err := r.Search(context.Background(), KnowledgeScope{TenantID: "t1", ClusterID: "c1"}, "x", 5)
	var qe *QueryError
	if !errors.As(err, &qe) || qe.Code != UnavailableCode {
		t.Fatalf("expected unavailable, got %v", err)
	}
}

func TestKnowledgeRepoRequiresCompleteScope(t *testing.T) {
	be := &fakeKnowledgeBackend{}
	r := NewKnowledgeRepository(be)
	_, err := r.Search(context.Background(), KnowledgeScope{TenantID: "t1"}, "x", 5)
	var qe *QueryError
	if !errors.As(err, &qe) || qe.Code != PermissionDeniedCode {
		t.Fatalf("expected permission denied for incomplete scope, got %v", err)
	}
	if be.calls != 0 {
		t.Fatalf("backend called for incomplete scope: %d", be.calls)
	}
}

func TestKnowledgeRepoTrimsAndBoundsQuery(t *testing.T) {
	be := &fakeKnowledgeBackend{hits: []KnowledgeHit{{DocumentID: "doc-1", KnowledgeID: "k-1", VersionID: "v-1", TenantID: "t1", ScopeType: "cluster", ClusterID: "c1", Status: "published", IsCurrent: true}}}
	r := NewKnowledgeRepository(be)
	if _, err := r.Search(context.Background(), KnowledgeScope{TenantID: "t1", ClusterID: "c1"}, "  x  ", 500); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if be.gotQ != "x" {
		t.Fatalf("query = %q", be.gotQ)
	}
}

func TestKnowledgeRepoDropsForeignOldAndNonCurrentHits(t *testing.T) {
	be := &fakeKnowledgeBackend{hits: []KnowledgeHit{
		{DocumentID: "ok", KnowledgeID: "k-1", VersionID: "v-2", TenantID: "t1", ScopeType: "cluster", ClusterID: "c1", Status: "published", IsCurrent: true},
		{DocumentID: "other-cluster", KnowledgeID: "k-2", VersionID: "v-2", TenantID: "t1", ScopeType: "cluster", ClusterID: "c2", Status: "published", IsCurrent: true},
		{DocumentID: "old-version", KnowledgeID: "k-1", VersionID: "v-1", TenantID: "t1", ScopeType: "cluster", ClusterID: "c1", Status: "published", IsCurrent: false},
	}}
	hits, err := NewKnowledgeRepository(be).Search(context.Background(), KnowledgeScope{TenantID: "t1", ClusterID: "c1"}, "x", 5)
	if err != nil || len(hits) != 1 || hits[0].DocumentID != "ok" {
		t.Fatalf("filtered hits=%+v err=%v", hits, err)
	}
}

type fakeKnowledgeAuthority struct {
	current bool
	err     error
}

func (f fakeKnowledgeAuthority) IsCurrentPublished(context.Context, KnowledgeScope, string, string) (bool, error) {
	return f.current, f.err
}

func TestKnowledgeRepoRechecksMySQLCurrentVersion(t *testing.T) {
	be := &fakeKnowledgeBackend{hits: []KnowledgeHit{{DocumentID: "old", KnowledgeID: "k-1", VersionID: "v-1", TenantID: "t1", ScopeType: "cluster", ClusterID: "c1", Status: "published", IsCurrent: true}}}
	r := NewKnowledgeRepositoryWithAuthority(be, fakeKnowledgeAuthority{current: false})
	_, err := r.Search(context.Background(), KnowledgeScope{TenantID: "t1", ClusterID: "c1"}, "x", 5)
	var queryErr *QueryError
	if !errors.As(err, &queryErr) || queryErr.Code != NoDataCode {
		t.Fatalf("expected old current version to be dropped, err=%v", err)
	}
}
