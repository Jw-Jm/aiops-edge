package graph

import (
	"fmt"
	"sort"
	"time"
)

type ShadowCompareReport struct {
	SampleCount     int     `json:"sample_count"`
	Structural      int     `json:"structural_mismatch"`
	Identity        int     `json:"identity_mismatch"`
	ScopeLeak       int     `json:"scope_leak"`
	LagSeconds      float64 `json:"lag_seconds"`
	AgeSeconds      float64 `json:"age_seconds"`
	P95Milliseconds float64 `json:"p95_ms"`
}

type ShadowGateThresholds struct {
	MaxStructural int
	MaxIdentity   int
	MaxScopeLeak  int
	MaxLagSeconds float64
	MaxAgeSeconds float64
	MaxP95Millis  float64
}

func CompareSubgraphs(primary, secondary Subgraph, scope GraphScope, p95Millis float64) ShadowCompareReport {
	report := ShadowCompareReport{P95Milliseconds: p95Millis}
	primaryVertices, secondaryVertices := entityMap(primary.Vertices), entityMap(secondary.Vertices)
	primaryEdges, secondaryEdges := edgeMap(primary.Edges), edgeMap(secondary.Edges)
	report.SampleCount = len(primaryVertices) + len(primaryEdges)
	for uid, entity := range primaryVertices {
		other, ok := secondaryVertices[uid]
		if !ok || entity.EntityType != other.EntityType {
			report.Structural++
		}
		if ok && entity.TenantID != other.TenantID {
			report.Identity++
		}
		if ok && !scope.Allows(other) {
			report.ScopeLeak++
		}
	}
	for uid := range secondaryVertices {
		if _, ok := primaryVertices[uid]; !ok {
			report.Identity++
		}
	}
	for uid := range primaryEdges {
		if _, ok := secondaryEdges[uid]; !ok {
			report.Structural++
		}
	}
	for uid := range secondaryEdges {
		if _, ok := primaryEdges[uid]; !ok {
			report.Structural++
		}
	}
	if generatedLag(primary.Meta.GeneratedAt, secondary.Meta.GeneratedAt) > 0 {
		report.LagSeconds = generatedLag(primary.Meta.GeneratedAt, secondary.Meta.GeneratedAt)
	}
	if age := time.Since(parseGeneratedAt(primary.Meta.GeneratedAt)); age > 0 {
		report.AgeSeconds = age.Seconds()
	}
	return report
}

func (r ShadowCompareReport) Gate(t ShadowGateThresholds) error {
	if r.Structural > t.MaxStructural || r.Identity > t.MaxIdentity || r.ScopeLeak > t.MaxScopeLeak {
		return fmt.Errorf("shadow identity/structural/scope gate failed: %+v", r)
	}
	if t.MaxLagSeconds > 0 && r.LagSeconds > t.MaxLagSeconds {
		return fmt.Errorf("shadow lag gate failed: %.3fs", r.LagSeconds)
	}
	if t.MaxAgeSeconds > 0 && r.AgeSeconds > t.MaxAgeSeconds {
		return fmt.Errorf("shadow age gate failed: %.3fs", r.AgeSeconds)
	}
	if t.MaxP95Millis > 0 && r.P95Milliseconds > t.MaxP95Millis {
		return fmt.Errorf("shadow p95 gate failed: %.3fms", r.P95Milliseconds)
	}
	return nil
}

func entityMap(items []Entity) map[string]Entity {
	out := make(map[string]Entity, len(items))
	for _, item := range items {
		out[item.EntityUID] = item
	}
	return out
}
func edgeMap(items []Edge) map[string]Edge {
	out := make(map[string]Edge, len(items))
	for _, item := range items {
		out[item.EdgeUID] = item
	}
	return out
}
func parseGeneratedAt(raw string) time.Time {
	value, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return value
}
func generatedLag(primary, secondary string) float64 {
	p, s := parseGeneratedAt(primary), parseGeneratedAt(secondary)
	if p.IsZero() || s.IsZero() {
		return 0
	}
	d := s.Sub(p).Seconds()
	if d < 0 {
		return -d
	}
	return d
}

// Keep deterministic output helpers available for reports/tests.
func sortedUIDs[T any](items map[string]T) []string {
	out := make([]string, 0, len(items))
	for uid := range items {
		out = append(out, uid)
	}
	sort.Strings(out)
	return out
}
