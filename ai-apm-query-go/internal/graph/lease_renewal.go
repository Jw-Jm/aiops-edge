package graph

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// withLeaseRenewal keeps a graph worker lease fenced while the supplied work
// is running.  Losing the lease cancels the work context so a slow backend
// call cannot continue mutating the projection after another worker owns the
// lease.
func withLeaseRenewal(
	ctx context.Context,
	leases GraphLeaseStore,
	lease store.GraphWorkerLease,
	ttl time.Duration,
	work func(context.Context) error,
) error {
	if leases == nil {
		return errors.New("graph lease store is not configured")
	}
	interval := ttl / 3
	if interval <= 0 {
		interval = time.Second
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	renewErr := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-workCtx.Done():
				return
			case <-ticker.C:
				ok, err := leases.Renew(lease, ttl)
				if err != nil {
					select {
					case renewErr <- err:
					default:
					}
					cancel()
					return
				}
				if !ok {
					select {
					case renewErr <- fmt.Errorf("graph lease lost: %s", lease.LeaseKey):
					default:
					}
					cancel()
					return
				}
			}
		}
	}()

	workResult := work(workCtx)
	close(done)
	select {
	case err := <-renewErr:
		return err
	default:
	}
	if workResult != nil {
		return workResult
	}
	return ctx.Err()
}
