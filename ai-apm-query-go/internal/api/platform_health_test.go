package api

import (
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

func TestProjectClusterOperationalStateDoesNotTreatRegistrationAsHealth(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	active := store.Cluster{Status: "active", LifecycleStatus: "ready", UpdatedAt: now.Add(-30 * time.Second)}
	if got := projectClusterOperationalState(active, now); got.Health != "unknown" || !got.Covered {
		t.Fatalf("active registration projection=%+v", got)
	}
	stale := store.Cluster{Status: "healthy", LifecycleStatus: "ready", UpdatedAt: now.Add(-6 * time.Minute)}
	if got := projectClusterOperationalState(stale, now); got.Health != "unknown" || !got.Stale || got.Covered {
		t.Fatalf("stale projection=%+v", got)
	}
	healthy := store.Cluster{Status: "healthy", LifecycleStatus: "ready", UpdatedAt: now.Add(-30 * time.Second)}
	if got := projectClusterOperationalState(healthy, now); got.Health != "healthy" || !got.Covered {
		t.Fatalf("healthy projection=%+v", got)
	}
}

func TestProjectClusterOperationalStateSeparatesRegistrationFromUnknown(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		status     string
		registered string
		updated    time.Time
		wantHealth string
		wantReg    string
		wantStale  bool
		wantCover  bool
	}{
		{name: "no evidence", registered: "ready", wantHealth: "unknown", wantReg: "ready", wantCover: false},
		{name: "critical", status: "critical", registered: "ready", updated: now.Add(-10 * time.Second), wantHealth: "critical", wantReg: "ready", wantCover: true},
		{name: "down", status: "down", registered: "ready", updated: now.Add(-10 * time.Second), wantHealth: "critical", wantReg: "ready", wantCover: true},
		{name: "degraded", status: "degraded", registered: "ready", updated: now.Add(-10 * time.Second), wantHealth: "degraded", wantReg: "ready", wantCover: true},
		{name: "unhealthy", status: "unhealthy", registered: "ready", updated: now.Add(-10 * time.Second), wantHealth: "degraded", wantReg: "ready", wantCover: true},
		{name: "running only", status: "running", registered: "ready", updated: now.Add(-10 * time.Second), wantHealth: "unknown", wantReg: "ready", wantCover: true},
		{name: "ok only", status: "ok", registered: "active", updated: now.Add(-10 * time.Second), wantHealth: "unknown", wantReg: "active", wantCover: true},
		{name: "stale healthy", status: "healthy", registered: "ready", updated: now.Add(-10 * time.Minute), wantHealth: "unknown", wantReg: "ready", wantStale: true, wantCover: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := projectClusterOperationalState(store.Cluster{Status: tc.status, LifecycleStatus: tc.registered, UpdatedAt: tc.updated}, now)
			if got.Health != tc.wantHealth || got.RegistrationStatus != tc.wantReg || got.Stale != tc.wantStale || got.Covered != tc.wantCover {
				t.Fatalf("projection=%+v want health=%s reg=%s stale=%v covered=%v", got, tc.wantHealth, tc.wantReg, tc.wantStale, tc.wantCover)
			}
			if got.Reason == "" {
				t.Fatalf("projection must always carry a reason: %+v", got)
			}
		})
	}
}

func TestSummarizePlatformCapabilitiesUsesProbeRows(t *testing.T) {
	rows := []systemComponentResultView{{Name: "query-api", Status: "ok"}, {Name: "ingest", Status: "down"}, {Name: "minio", Status: "not_configured"}}
	got := summarizePlatformCapabilities(rows)
	if got.Healthy != 1 || got.Total != 2 || len(got.Issues) != 1 || got.Issues[0] != "ingest" {
		t.Fatalf("summary=%+v", got)
	}
}

func TestSummarizePlatformCapabilitiesReportsNothingWhenNoProbeRan(t *testing.T) {
	got := summarizePlatformCapabilities(nil)
	if got.Total != 0 || got.Healthy != 0 {
		t.Fatalf("no probe rows must not become a fabricated 0/0 healthy summary: %+v", got)
	}
}

func TestCollectSystemComponentResultsUsesInjectedProbe(t *testing.T) {
	probed := map[string]bool{}
	rows := collectSystemComponentResults(func(kind, addr string) bool {
		probed[addr] = true
		return false
	})
	if len(rows) == 0 {
		t.Fatal("collector must return the platform component catalog")
	}
	if len(probed) == 0 {
		t.Fatal("collector must actually probe configured components")
	}
	for _, row := range rows {
		if row.Name == "" || row.Status == "" {
			t.Fatalf("row must carry name and status: %+v", row)
		}
		if row.Status != "down" && row.Status != "not_configured" {
			t.Fatalf("failed probe must be reported as down, got %+v", row)
		}
	}
}
