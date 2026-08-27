package graph

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

type fakeOutboxStore struct {
	items   []store.GraphProjectionOutbox
	claimed map[string]bool
	done    []string
	retried []string
}

func (f *fakeOutboxStore) ScanPending(int) ([]store.GraphProjectionOutbox, error) {
	return f.items, nil
}
func (f *fakeOutboxStore) Claim(eventID, _ string, _ time.Duration) (bool, error) {
	if f.claimed == nil {
		f.claimed = map[string]bool{}
	}
	f.claimed[eventID] = true
	return true, nil
}
func (f *fakeOutboxStore) Done(eventID, _ string) error { f.done = append(f.done, eventID); return nil }
func (f *fakeOutboxStore) Retry(eventID, _ string, _ time.Time, _ string) error {
	f.retried = append(f.retried, eventID)
	return nil
}

type fakeLeaseStore struct{}

func (fakeLeaseStore) Acquire(string, string, time.Duration) (store.GraphWorkerLease, bool, error) {
	return store.GraphWorkerLease{}, true, nil
}
func (fakeLeaseStore) Renew(store.GraphWorkerLease, time.Duration) (bool, error) { return true, nil }

type failingProjectorRepository struct{}

func (failingProjectorRepository) GetEntity(context.Context, GraphScope, string) (Entity, error) {
	return Entity{}, errors.New("unused")
}
func (failingProjectorRepository) SearchEntities(context.Context, GraphScope, EntitySearchQuery) ([]Entity, error) {
	return nil, errors.New("unused")
}
func (failingProjectorRepository) Neighbors(context.Context, GraphScope, NeighborQuery) (Subgraph, error) {
	return Subgraph{}, errors.New("unused")
}
func (failingProjectorRepository) ShortestPath(context.Context, GraphScope, PathQuery) (Subgraph, error) {
	return Subgraph{}, errors.New("unused")
}
func (failingProjectorRepository) Impact(context.Context, GraphScope, ImpactQuery) (Subgraph, error) {
	return Subgraph{}, errors.New("unused")
}
func (failingProjectorRepository) CandidateSubgraph(context.Context, GraphScope, NeighborQuery) (Subgraph, error) {
	return Subgraph{}, errors.New("unused")
}
func (failingProjectorRepository) BatchMutate(context.Context, MutationBatch) (MutationResult, error) {
	return MutationResult{}, errors.New("projection failed")
}
func (failingProjectorRepository) Health(context.Context) GraphHealth { return GraphHealth{} }

func TestOutboxProjectorRetriesProjectionFailureWithBoundedBackoff(t *testing.T) {
	outbox := &fakeOutboxStore{items: []store.GraphProjectionOutbox{{EventID: "event-1", TenantID: "tenant-a", ClusterID: "cluster-a", MutationKind: "upsert_vertex", PayloadJSON: `{"entity_uid":"service:v1:tenant-a:s","entity_type":"service","tenant_id":"tenant-a","cluster_id":"cluster-a","name":"checkout","name_key":"checkout","source":"catalog","status":"active"}`, RetryCount: 2}}}
	projector := NewOutboxProjector(failingProjectorRepository{}, outbox, fakeLeaseStore{}, "worker-1")
	if err := projector.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce succeeded after projection failure")
	}
	if len(outbox.retried) != 1 || outbox.retried[0] != "event-1" {
		t.Fatalf("retried=%v", outbox.retried)
	}
	if got := RetryBackoff(2); got != 4*time.Second {
		t.Fatalf("RetryBackoff(2)=%v, want 4s", got)
	}
}

func TestOutboxProjectorAcceptsDeterministicRetryAndMarksDone(t *testing.T) {
	repo := NewMemoryRepository()
	outbox := &fakeOutboxStore{items: []store.GraphProjectionOutbox{{EventID: "event-1", TenantID: "tenant-a", ClusterID: "cluster-a", MutationKind: "upsert_vertex", PayloadJSON: `{"entity_uid":"service:v1:tenant-a:s","entity_type":"service","tenant_id":"tenant-a","cluster_id":"cluster-a","name":"checkout","name_key":"checkout","source":"catalog","status":"active"}`}}}
	projector := NewOutboxProjector(repo, outbox, fakeLeaseStore{}, "worker-1")
	if err := projector.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(outbox.done) != 1 {
		t.Fatalf("done=%v", outbox.done)
	}
	if _, err := repo.GetEntity(context.Background(), GraphScope{TenantID: "tenant-a", ClusterIDs: map[string]struct{}{"cluster-a": {}}}, "service:v1:tenant-a:s"); err != nil {
		t.Fatal(err)
	}
}
