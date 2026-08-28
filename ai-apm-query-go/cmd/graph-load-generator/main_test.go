package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestDefaultFixtureHasOneMillionUniqueEndpointPairs(t *testing.T) {
	const vertices, edges = 200000, 1000000
	pairs := make(map[[2]int]struct{}, edges)
	for index := 0; index < edges; index++ {
		source, target := edgeEndpoints(index, vertices)
		if source == target {
			t.Fatalf("self edge at index %d", index)
		}
		pairs[[2]int{source, target}] = struct{}{}
	}
	if len(pairs) != edges {
		t.Fatalf("unique endpoint pairs=%d, want %d", len(pairs), edges)
	}
}

func TestLoadBatchesCoversEachRangeOnce(t *testing.T) {
	var mu sync.Mutex
	seen := map[int]bool{}
	if err := loadBatches(context.Background(), 10, 3, 4, func(start, end int) error {
		mu.Lock()
		defer mu.Unlock()
		for index := start; index < end; index++ {
			if seen[index] {
				t.Fatalf("range index %d was scheduled more than once", index)
			}
			seen[index] = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 10 {
		t.Fatalf("covered %d items, want 10", len(seen))
	}
}

func TestLoadBatchesReturnsFirstError(t *testing.T) {
	want := fmt.Errorf("fixture failed")
	err := loadBatches(context.Background(), 10, 2, 3, func(start, end int) error {
		if start == 2 {
			return want
		}
		return nil
	})
	if err == nil || err.Error() != want.Error() {
		t.Fatalf("loadBatches error=%v, want %v", err, want)
	}
}
