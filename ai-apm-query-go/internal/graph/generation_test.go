package graph

import (
	"context"
	"testing"
	"time"
)

func TestGenerationAndStaleGrace(t *testing.T) {
	if got := NextGeneration(4); got != 5 {
		t.Fatalf("generation=%d", got)
	}
	now := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	if !StaleEligible("kubernetes", now.Add(-16*time.Minute), now) {
		t.Fatal("kubernetes should be stale after grace")
	}
	if StaleEligible("catalog", now.Add(-24*time.Hour), now) {
		t.Fatal("catalog must not use stale grace")
	}
}

type backfillSourceFunc func(context.Context) error

func (f backfillSourceFunc) Backfill(ctx context.Context) error { return f(ctx) }

func TestBackfillOrder(t *testing.T) {
	got := []string{}
	sources := map[string]BackfillSource{}
	for _, name := range BackfillOrder {
		n := name
		sources[name] = backfillSourceFunc(func(context.Context) error { got = append(got, n); return nil })
	}
	if _, err := RunBackfill(context.Background(), sources); err != nil {
		t.Fatal(err)
	}
	for i, name := range BackfillOrder {
		if got[i] != name {
			t.Fatalf("order=%v", got)
		}
	}
}
