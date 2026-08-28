package main

import "testing"

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
