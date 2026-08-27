package graph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

type ReconcileRequest struct {
	Source    string
	TenantID  string
	ClusterID string
	Watermark string
	Build     func(generation int64) (MutationBatch, error)
}

type GraphSyncStateStore interface {
	Get(source, tenantID, scopeClusterID string) (*store.GraphSyncState, error)
	Start(source, tenantID, scopeClusterID string, generation int64) error
	Finish(source, tenantID, scopeClusterID, watermark string, generation int64, status, lastError string) error
}

type ReconcileRunStore interface {
	Start(run store.GraphReconcileRun) error
	Finish(runID, status, message string, verticesSeen, edgesSeen, verticesStaled, edgesStaled int64) error
}

type StaleGenerationMarker func(ctx context.Context, source, tenantID, clusterID string, generation int64) error

type GraphReconciler struct {
	repository GraphRepository
	leases     GraphLeaseStore
	syncState  GraphSyncStateStore
	runs       ReconcileRunStore
	markStale  StaleGenerationMarker
	owner      string
}

func NewGraphReconciler(repository GraphRepository, leases GraphLeaseStore, syncState GraphSyncStateStore, runs ReconcileRunStore, markStale StaleGenerationMarker) *GraphReconciler {
	return &GraphReconciler{repository: repository, leases: leases, syncState: syncState, runs: runs, markStale: markStale, owner: graphProjectorOwner}
}

func (r *GraphReconciler) Reconcile(ctx context.Context, request ReconcileRequest) error {
	if r == nil || r.repository == nil || r.leases == nil || r.syncState == nil || r.runs == nil || request.Build == nil {
		return errors.New("graph reconciler is not configured")
	}
	if strings.TrimSpace(request.Source) == "" || strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.ClusterID) == "" {
		return errors.New("source, tenant and cluster are required")
	}
	previousGeneration := int64(0)
	previous, err := r.syncState.Get(request.Source, request.TenantID, request.ClusterID)
	if err == nil && previous != nil {
		previousGeneration = previous.Generation
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	generation := previousGeneration + 1
	leaseKey := fmt.Sprintf("graph-reconcile:%s:%s:%s", request.Source, request.TenantID, request.ClusterID)
	ttl := reconcileLeaseTTL()
	lease, acquired, err := r.leases.Acquire(leaseKey, r.owner, ttl)
	if err != nil {
		return err
	} else if !acquired {
		return nil
	}
	return withLeaseRenewal(ctx, r.leases, lease, ttl, func(workCtx context.Context) error {
		runID := SHA256Parts(leaseKey, strconv.FormatInt(generation, 10), request.Watermark)
		if err := r.runs.Start(store.GraphReconcileRun{ReconcileRunID: runID, Source: request.Source, TenantID: request.TenantID, ScopeClusterID: request.ClusterID, Generation: generation, Status: "running"}); err != nil {
			return err
		}
		if err := r.syncState.Start(request.Source, request.TenantID, request.ClusterID, generation); err != nil {
			_ = r.runs.Finish(runID, "failed", err.Error(), 0, 0, 0, 0)
			return err
		}
		batch, err := request.Build(generation)
		if err == nil {
			_, err = r.repository.BatchMutate(workCtx, batch)
		}
		if err == nil && r.markStale != nil {
			err = r.markStale(workCtx, request.Source, request.TenantID, request.ClusterID, generation)
		}
		if err != nil {
			// Preserve the previous successful generation. Incomplete source facts
			// must never trigger stale/delete processing.
			_ = r.syncState.Finish(request.Source, request.TenantID, request.ClusterID, request.Watermark, previousGeneration, "failed", err.Error())
			_ = r.runs.Finish(runID, "failed", err.Error(), 0, 0, 0, 0)
			return err
		}
		if err := r.syncState.Finish(request.Source, request.TenantID, request.ClusterID, request.Watermark, generation, "success", ""); err != nil {
			_ = r.runs.Finish(runID, "failed", err.Error(), 0, 0, 0, 0)
			return err
		}
		if err := r.runs.Finish(runID, "success", "", int64(len(batch.Vertices)), int64(len(batch.Edges)), 0, 0); err != nil {
			return err
		}
		return nil
	})
}

func reconcileLeaseTTL() time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(os.Getenv("GRAPH_RECONCILE_LEASE_TTL_SECONDS")))
	if err != nil || seconds < 30 {
		seconds = 120
	}
	return time.Duration(seconds) * time.Second
}
