package query

import (
	"context"
	"errors"
	"testing"
)

// fakeKnowledgeBackend 是一个可注入的测试 backend，模拟 Chroma 向量索引 + MinIO Knowledge Object。
type fakeKnowledgeBackend struct {
	hits  []KnowledgeHit
	err   error
	calls int
	gotQ  string
}

func (f *fakeKnowledgeBackend) Search(ctx context.Context, query string, topK int) ([]KnowledgeHit, error) {
	f.calls++
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
	hits, err := r.Search(context.Background(), KnowledgeScope{TenantID: "t1"}, "checkout pod crashloop", 5)
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
}

func TestKnowledgeRepoEmptyIsNoData(t *testing.T) {
	be := &fakeKnowledgeBackend{hits: nil}
	r := NewKnowledgeRepository(be)
	_, err := r.Search(context.Background(), KnowledgeScope{TenantID: "t1"}, "nothing", 5)
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
	_, err := r.Search(context.Background(), KnowledgeScope{TenantID: "t1"}, "x", 5)
	var qe *QueryError
	if !errors.As(err, &qe) || qe.Code != UnavailableCode {
		t.Fatalf("expected unavailable, got %v", err)
	}
}
