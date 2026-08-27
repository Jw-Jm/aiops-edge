package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

const (
	graphProjectorLeaseKey  = "graph-projector"
	graphProjectorLeaseTTL  = 15 * time.Second
	graphProjectorBatchSize = 100
	graphProjectorOwner     = "query-api-graph-projector"
)

type OutboxStore interface {
	ScanPending(int) ([]store.GraphProjectionOutbox, error)
	Claim(eventID, owner string, lease time.Duration) (bool, error)
	Done(eventID, owner string) error
	Retry(eventID, owner string, availableAt time.Time, lastError string) error
}

type GraphLeaseStore interface {
	Acquire(leaseKey, ownerID string, ttl time.Duration) (store.GraphWorkerLease, bool, error)
	Renew(lease store.GraphWorkerLease, ttl time.Duration) (bool, error)
}

type OutboxProjector struct {
	repository GraphRepository
	outbox     OutboxStore
	leases     GraphLeaseStore
	owner      string
}

func NewOutboxProjector(repository GraphRepository, outbox OutboxStore, leases GraphLeaseStore, owner string) *OutboxProjector {
	if owner == "" {
		owner = graphProjectorOwner
	}
	return &OutboxProjector{repository: repository, outbox: outbox, leases: leases, owner: owner}
}

func (p *OutboxProjector) RunOnce(ctx context.Context) error {
	if p == nil || p.repository == nil || p.outbox == nil || p.leases == nil {
		return errors.New("graph projector is not configured")
	}
	lease, acquired, err := p.leases.Acquire(graphProjectorLeaseKey, p.owner, graphProjectorLeaseTTL)
	if err != nil {
		return err
	} else if !acquired {
		return nil
	}
	return withLeaseRenewal(ctx, p.leases, lease, graphProjectorLeaseTTL, func(workCtx context.Context) error {
		items, err := p.outbox.ScanPending(graphProjectorBatchSize)
		if err != nil {
			return err
		}
		var firstErr error
		for _, item := range items {
			if err := workCtx.Err(); err != nil {
				return err
			}
			claimed, err := p.outbox.Claim(item.EventID, p.owner, 30*time.Second)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if !claimed {
				continue
			}
			if err := p.apply(workCtx, item); err != nil {
				next := time.Now().UTC().Add(RetryBackoff(item.RetryCount))
				if retryErr := p.outbox.Retry(item.EventID, p.owner, next, err.Error()); retryErr != nil && firstErr == nil {
					firstErr = retryErr
				}
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if err := p.outbox.Done(item.EventID, p.owner); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	})
}

func (p *OutboxProjector) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := p.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			// A single bad event must be retried by the state machine; keep the
			// loop alive so unrelated events can make progress. Wait for the
			// next poll interval instead of spinning on a failed database/backend.
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (p *OutboxProjector) apply(ctx context.Context, item store.GraphProjectionOutbox) error {
	var entity Entity
	var edge Edge
	switch item.MutationKind {
	case "upsert_vertex":
		if err := decodePayload(item.PayloadJSON, &entity, "vertex"); err != nil {
			return err
		}
		_, err := p.repository.BatchMutate(ctx, MutationBatch{TenantID: item.TenantID, ClusterID: item.ClusterID, Source: item.AggregateType, Vertices: []Entity{entity}})
		return err
	case "upsert_edge":
		if err := decodePayload(item.PayloadJSON, &edge, "edge"); err != nil {
			return err
		}
		_, err := p.repository.BatchMutate(ctx, MutationBatch{TenantID: item.TenantID, ClusterID: item.ClusterID, Source: item.AggregateType, Edges: []Edge{edge}})
		return err
	case "batch_mutate":
		var batch MutationBatch
		if err := json.Unmarshal([]byte(item.PayloadJSON), &batch); err != nil {
			return err
		}
		if batch.TenantID == "" {
			batch.TenantID = item.TenantID
		}
		if batch.ClusterID == "" {
			batch.ClusterID = item.ClusterID
		}
		_, err := p.repository.BatchMutate(ctx, batch)
		return err
	case "delete_vertex":
		deleter, ok := p.repository.(interface {
			DeleteEntity(context.Context, GraphScope, string) error
		})
		if !ok {
			return graphError(ErrGraphFeatureUnavailable, "repository does not support vertex deletion")
		}
		uid := item.EntityUID
		if uid == "" {
			uid = item.AggregateID
		}
		return deleter.DeleteEntity(ctx, GraphScope{TenantID: item.TenantID, ClusterIDs: map[string]struct{}{item.ClusterID: {}}}, uid)
	case "delete_edge":
		deleter, ok := p.repository.(interface {
			DeleteEdge(context.Context, GraphScope, string) error
		})
		if !ok {
			return graphError(ErrGraphFeatureUnavailable, "repository does not support edge deletion")
		}
		return deleter.DeleteEdge(ctx, GraphScope{TenantID: item.TenantID, ClusterIDs: map[string]struct{}{item.ClusterID: {}}}, item.EdgeUID)
	default:
		return fmt.Errorf("unsupported outbox mutation kind %q", item.MutationKind)
	}
}

func decodePayload(raw string, target interface{}, wrapper string) error {
	if err := json.Unmarshal([]byte(raw), target); err == nil {
		return nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return err
	}
	payload, ok := envelope[wrapper]
	if !ok {
		return fmt.Errorf("outbox payload missing %s", wrapper)
	}
	return json.Unmarshal(payload, target)
}

// RetryBackoff is the fixed 2,4,8,...,300 second policy from the production
// runbook. retryCount is the number of failures already recorded.
func RetryBackoff(retryCount int) time.Duration {
	if retryCount < 1 {
		retryCount = 1
	}
	seconds := math.Pow(2, float64(retryCount))
	if seconds > 300 {
		seconds = 300
	}
	return time.Duration(seconds) * time.Second
}
