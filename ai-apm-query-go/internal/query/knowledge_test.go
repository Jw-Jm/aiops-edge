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
		{DocumentID: "doc-1", Source: "runbook", Version: "v3", Similarity: 0.92, Applicability: "checkout"},
		{DocumentID: "doc-2", Source: "sop", Version: "v1", Similarity: 0.81, Applicability: "checkout"},
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
	be := &fakeKnowledgeBackend{hits: []KnowledgeHit{{DocumentID: "doc-1"}}}
	r := NewKnowledgeRepository(be)
	if _, err := r.Search(context.Background(), KnowledgeScope{TenantID: "t1", ClusterID: "c1"}, "  x  ", 500); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if be.gotQ != "x" {
		t.Fatalf("query = %q", be.gotQ)
	}
}
