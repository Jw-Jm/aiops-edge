package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestDefaultFixtureHasOneMillionUniqueEndpointPairs(t *testing.T) {
	const vertices, edges = 200000, 1000000
	pairs := make(map[string]struct{}, edges)
	for index := 0; index < edges; index++ {
		spec, local := fixtureEdgeSpec(index)
		source, target := edgeEndpointsForSpec(spec, local, vertices)
		if source == target {
			t.Fatalf("self edge at index %d", index)
		}
		key := fmt.Sprintf("%s:%d:%d", spec.RelationType, source, target)
		pairs[key] = struct{}{}
	}
	if len(pairs) != edges {
		t.Fatalf("unique endpoint pairs=%d, want %d", len(pairs), edges)
	}
}

func TestDefaultFixtureUsesRequiredOntologyMix(t *testing.T) {
	counts := fixtureTypeCounts(200000)
	for entityType, minimum := range map[string]int{
		"service": 20000, "pod": 50000, "container": 50000,
		"vm": 5000, "vmi": 5000, "k8s_node": 4000,
		"physical_server": 3000, "dimm": 3000,
	} {
		if counts[entityType] < minimum {
			t.Fatalf("fixture %s=%d, want at least %d", entityType, counts[entityType], minimum)
		}
	}
	total := 0
	for _, count := range counts {
		total += count
	}
	if total != 200000 {
		t.Fatalf("fixture vertex total=%d, want 200000", total)
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
