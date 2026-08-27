package graph

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

type fakeSyncState struct {
	previous store.GraphSyncState
	started  int64
	finished store.GraphSyncState
}

func (f *fakeSyncState) Get(string, string, string) (*store.GraphSyncState, error) {
	return &f.previous, nil
}
func (f *fakeSyncState) Start(_ string, _ string, _ string, generation int64) error {
	f.started = generation
	return nil
}
func (f *fakeSyncState) Finish(source, tenant, cluster, watermark string, generation int64, status, lastError string) error {
	f.finished = store.GraphSyncState{Source: source, TenantID: tenant, ScopeClusterID: cluster, Watermark: watermark, Generation: generation, Status: status, LastError: lastError}
	return nil
}

type fakeReconcileRuns struct{ statuses []string }

func (f *fakeReconcileRuns) Start(store.GraphReconcileRun) error {
	f.statuses = append(f.statuses, "running")
	return nil
}
func (f *fakeReconcileRuns) Finish(string, status, _ string, _ int64, _ int64, _ int64, _ int64) error {
	f.statuses = append(f.statuses, status)
	return nil
}

type recordingReconcileRepository struct {
	MemoryRepository
	mutations int
	fail      bool
}

func (r *recordingReconcileRepository) BatchMutate(ctx context.Context, batch MutationBatch) (MutationResult, error) {
	r.mutations++
	if r.fail {
		return MutationResult{}, errors.New("batch failed")
	}
	return r.MemoryRepository.BatchMutate(ctx, batch)
}

func TestReconcilerAdvancesGenerationAndMarksStaleOnlyAfterCompleteBatch(t *testing.T) {
	syncState := &fakeSyncState{previous: store.GraphSyncState{Generation: 7}}
	runs := &fakeReconcileRuns{}
	lease := fakeLeaseStore{}
	repo := &recordingReconcileRepository{MemoryRepository: *NewMemoryRepository()}
	marked := int64(0)
	reconciler := NewGraphReconciler(repo, lease, syncState, runs, func(_ context.Context, source, tenant, cluster string, generation int64) error {
		marked = generation
		return nil
	})
	err := reconciler.Reconcile(context.Background(), ReconcileRequest{Source: "kubernetes", TenantID: "tenant-a", ClusterID: "cluster-a", Watermark: "w8", Build: func(int64) (MutationBatch, error) {
		return MutationBatch{TenantID: "tenant-a", ClusterID: "cluster-a", Source: "kubernetes", Generation: 8}, nil
	}})
	if err != nil || syncState.started != 8 || syncState.finished.Generation != 8 || syncState.finished.Status != "success" || marked != 8 {
		t.Fatalf("err=%v sync=%+v marked=%d runs=%v", err, syncState.finished, marked, runs.statuses)
	}
}

func TestReconcilerFailureKeepsPreviousGenerationAndDoesNotMarkStale(t *testing.T) {
	syncState := &fakeSyncState{previous: store.GraphSyncState{Generation: 7}}
	runs := &fakeReconcileRuns{}
	repo := &recordingReconcileRepository{MemoryRepository: *NewMemoryRepository(), fail: true}
	marked := false
	reconciler := NewGraphReconciler(repo, fakeLeaseStore{}, syncState, runs, func(context.Context, string, string, string, int64) error { marked = true; return nil })
	err := reconciler.Reconcile(context.Background(), ReconcileRequest{Source: "kubernetes", TenantID: "tenant-a", ClusterID: "cluster-a", Build: func(int64) (MutationBatch, error) {
		return MutationBatch{TenantID: "tenant-a", ClusterID: "cluster-a", Source: "kubernetes", Generation: 8}, nil
	}})
	if err == nil || marked || syncState.finished.Generation != 7 || syncState.finished.Status != "failed" {
		t.Fatalf("err=%v marked=%v sync=%+v", err, marked, syncState.finished)
	}
	if RetryBackoff(9) != 300*time.Second {
		t.Fatalf("unexpected backoff")
	}
}
