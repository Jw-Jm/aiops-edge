package api

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// fakeAlertPolicies is an in-memory policy reader.
type fakeAlertPolicies struct {
	policy *store.AlertInvestigationPolicy
}

func (f fakeAlertPolicies) Get(tenantID, clusterID string) (*store.AlertInvestigationPolicy, error) {
	if f.policy == nil {
		fallback := store.DefaultAlertInvestigationPolicy(tenantID, clusterID)
		return &fallback, nil
	}
	return f.policy, nil
}

// fakeAlertLinks is an in-memory link store that also proves idempotency under
// concurrent triggers.
type fakeAlertLinks struct {
	mu    sync.Mutex
	links map[string]store.AlertRunLink
}

func newFakeAlertLinks() *fakeAlertLinks {
	return &fakeAlertLinks{links: map[string]store.AlertRunLink{}}
}

func alertLinkKey(tenantID, clusterID, eventID string) string {
	return tenantID + "|" + clusterID + "|" + eventID
}

func (f *fakeAlertLinks) Get(tenantID, clusterID, alertEventID string) (*store.AlertRunLink, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if link, ok := f.links[alertLinkKey(tenantID, clusterID, alertEventID)]; ok {
		return &link, nil
	}
	return nil, nil
}

func (f *fakeAlertLinks) Insert(link store.AlertRunLink) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := alertLinkKey(link.TenantID, link.ClusterID, link.AlertEventID)
	if _, ok := f.links[key]; ok {
		return nil // duplicate insert is a no-op replay
	}
	f.links[key] = link
	return nil
}

func (f *fakeAlertLinks) CountActiveAutoRuns(tenantID, clusterID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, link := range f.links {
		if link.TenantID == tenantID && link.ClusterID == clusterID && link.Decision == store.AlertRunCreated {
			count++
		}
	}
	return count, nil
}

func (f *fakeAlertLinks) CountRecentLinks(tenantID, clusterID string, window time.Duration) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, link := range f.links {
		if link.TenantID == tenantID && link.ClusterID == clusterID {
			count++
		}
	}
	return count, nil
}

// fakeAlertRuns records created runs and mirrors the production transaction by
// writing the link in the same critical section.
type fakeAlertRuns struct {
	mu    sync.Mutex
	runs  []store.AIRun
	links *fakeAlertLinks
}

func (f *fakeAlertRuns) CreateRunWithAlertLink(r store.AIRun, o store.AIRunOutbox, link store.AlertRunLink) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// 模拟真实事务：run_id/request_id 冲突 → created=false（幂等重放路径）。
	for _, existing := range f.runs {
		if existing.RunID == r.RunID || existing.RequestID == r.RequestID {
			return false, nil
		}
	}
	f.runs = append(f.runs, r)
	if f.links != nil {
		_ = f.links.Insert(link)
	}
	return true, nil
}

func newTestAlertService(policy *store.AlertInvestigationPolicy, links *fakeAlertLinks, runs *fakeAlertRuns) *AlertInvestigationService {
	if runs != nil && runs.links == nil {
		runs.links = links
	}
	return &AlertInvestigationService{policies: fakeAlertPolicies{policy: policy}, links: links, runs: runs,
		now: func() time.Time { return time.Date(2026, 9, 11, 8, 0, 0, 0, time.UTC) }, newID: randomUUID}
}

func alertEventForInvestigation(id string) AlertEvent {
	return AlertEvent{
		ID: id, TenantID: "tenant-1", Cluster: "cluster-a", RuleID: "rule-1", RuleName: "Pod 未就绪",
		Service: "kubernetes", Severity: "critical", Status: "firing", Object: "pod/orders-api",
		FirstTimestamp: "2026-09-11T07:50:00Z", LastTimestamp: "2026-09-11T07:58:00Z", Signature: "sig-1",
	}
}

func TestOnAlertWithoutPolicyRowIsStrictlyManual(t *testing.T) {
	service := newTestAlertService(nil, newFakeAlertLinks(), &fakeAlertRuns{})
	projection, err := service.OnAlert(context.Background(), alertEventForInvestigation("event-1"))
	if err != nil {
		t.Fatalf("OnAlert: %v", err)
	}
	if projection.Mode != "manual" || projection.Status != "none" {
		t.Fatalf("projection = %+v, want manual/none", projection)
	}
}

func TestOnAlertManualNeverCreatesLinkOrRun(t *testing.T) {
	links := newFakeAlertLinks()
	runs := &fakeAlertRuns{}
	service := newTestAlertService(&store.AlertInvestigationPolicy{TenantID: "tenant-1", ClusterID: "cluster-a",
		Mode: store.AlertInvestigationManual, MinimumSeverity: "critical", MaxConcurrent: 2, MaxPerHour: 10}, links, runs)
	if _, err := service.OnAlert(context.Background(), alertEventForInvestigation("event-1")); err != nil {
		t.Fatalf("OnAlert: %v", err)
	}
	if len(links.links) != 0 || len(runs.runs) != 0 {
		t.Fatalf("manual mode created state: links=%d runs=%d", len(links.links), len(runs.runs))
	}
}

func TestOnAlertDraftRecordsLinkWithoutRun(t *testing.T) {
	links := newFakeAlertLinks()
	runs := &fakeAlertRuns{}
	service := newTestAlertService(&store.AlertInvestigationPolicy{TenantID: "tenant-1", ClusterID: "cluster-a",
		Mode: store.AlertInvestigationDraft, MinimumSeverity: "critical", MaxConcurrent: 2, MaxPerHour: 10}, links, runs)
	projection, err := service.OnAlert(context.Background(), alertEventForInvestigation("event-1"))
	if err != nil {
		t.Fatalf("OnAlert: %v", err)
	}
	if projection.Status != "draft" || projection.RunID != "" {
		t.Fatalf("draft projection = %+v", projection)
	}
	if len(runs.runs) != 0 {
		t.Fatalf("draft mode must not create a run, got %d", len(runs.runs))
	}
}

func TestOnAlertAutoReadonlyCreatesSystemReadOnlyRun(t *testing.T) {
	links := newFakeAlertLinks()
	runs := &fakeAlertRuns{}
	service := newTestAlertService(&store.AlertInvestigationPolicy{TenantID: "tenant-1", ClusterID: "cluster-a",
		Mode: store.AlertInvestigationAutoReadonly, MinimumSeverity: "critical", MaxConcurrent: 2, MaxPerHour: 10}, links, runs)
	projection, err := service.OnAlert(context.Background(), alertEventForInvestigation("event-1"))
	if err != nil {
		t.Fatalf("OnAlert: %v", err)
	}
	if projection.Status != "run" || projection.RunID == "" {
		t.Fatalf("projection = %+v", projection)
	}
	if len(runs.runs) != 1 {
		t.Fatalf("runs = %d, want exactly one", len(runs.runs))
	}
	run := runs.runs[0]
	if run.PrincipalType != "system" || run.ActionMode != "read_only" {
		t.Fatalf("auto run = %+v, want system + read_only", run)
	}
	if run.PrimaryClusterID != "cluster-a" {
		t.Fatalf("auto run must stay on the alert cluster, got %s", run.PrimaryClusterID)
	}
	if run.TimeRangeStart == nil || run.TimeRangeEnd == nil {
		t.Fatal("auto run must freeze an absolute evidence window")
	}
	if run.TimeRangeEnd.Sub(*run.TimeRangeStart) > 24*time.Hour {
		t.Fatalf("frozen window exceeds 24h: %v", run.TimeRangeEnd.Sub(*run.TimeRangeStart))
	}
}

func TestOnAlertSkipsBelowSeverityAndRecordsReason(t *testing.T) {
	links := newFakeAlertLinks()
	runs := &fakeAlertRuns{}
	service := newTestAlertService(&store.AlertInvestigationPolicy{TenantID: "tenant-1", ClusterID: "cluster-a",
		Mode: store.AlertInvestigationAutoReadonly, MinimumSeverity: "critical", MaxConcurrent: 2, MaxPerHour: 10}, links, runs)
	event := alertEventForInvestigation("event-1")
	event.Severity = "warning"
	projection, err := service.OnAlert(context.Background(), event)
	if err != nil {
		t.Fatalf("OnAlert: %v", err)
	}
	if projection.Status != "skipped" || projection.ReasonCode != store.AlertInvestigationBelowSeverity {
		t.Fatalf("projection = %+v", projection)
	}
	if len(runs.runs) != 0 {
		t.Fatalf("below-severity alert must not create a run")
	}
}

func TestOnAlertEnforcesConcurrencyAndRateLimits(t *testing.T) {
	links := newFakeAlertLinks()
	runs := &fakeAlertRuns{}
	service := newTestAlertService(&store.AlertInvestigationPolicy{TenantID: "tenant-1", ClusterID: "cluster-a",
		Mode: store.AlertInvestigationAutoReadonly, MinimumSeverity: "critical", MaxConcurrent: 1, MaxPerHour: 1}, links, runs)

	first, err := service.OnAlert(context.Background(), alertEventForInvestigation("event-1"))
	if err != nil || first.Status != "run" {
		t.Fatalf("first alert = %+v, %v", first, err)
	}
	// Concurrency limit is reached while the first run is still open.
	second, err := service.OnAlert(context.Background(), alertEventForInvestigation("event-2"))
	if err != nil {
		t.Fatalf("second alert: %v", err)
	}
	if second.Status != "skipped" || second.ReasonCode != store.AlertInvestigationConcurrencyLimited {
		t.Fatalf("second projection = %+v", second)
	}
	if len(runs.runs) != 1 {
		t.Fatalf("runs = %d, concurrency gate failed", len(runs.runs))
	}
}

func TestOnAlertIsIdempotentForTheSameEvent(t *testing.T) {
	links := newFakeAlertLinks()
	runs := &fakeAlertRuns{}
	service := newTestAlertService(&store.AlertInvestigationPolicy{TenantID: "tenant-1", ClusterID: "cluster-a",
		Mode: store.AlertInvestigationAutoReadonly, MinimumSeverity: "critical", MaxConcurrent: 8, MaxPerHour: 100}, links, runs)

	first, err := service.OnAlert(context.Background(), alertEventForInvestigation("event-1"))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	for i := 0; i < 50; i++ {
		replay, err := service.OnAlert(context.Background(), alertEventForInvestigation("event-1"))
		if err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
		if replay.RunID != first.RunID {
			t.Fatalf("replay %d produced run %s, want %s", i, replay.RunID, first.RunID)
		}
	}
	if len(runs.runs) != 1 {
		t.Fatalf("runs = %d, want exactly one across replays", len(runs.runs))
	}
}

// TestOnAlertConcurrentTriggersProduceSingleRun 是并发幂等门禁：50 个并发触发
// 只产生一个 link、一个 Run。必须以 -race 运行。
func TestOnAlertConcurrentTriggersProduceSingleRun(t *testing.T) {
	links := newFakeAlertLinks()
	runs := &fakeAlertRuns{}
	service := newTestAlertService(&store.AlertInvestigationPolicy{TenantID: "tenant-1", ClusterID: "cluster-a",
		Mode: store.AlertInvestigationAutoReadonly, MinimumSeverity: "critical", MaxConcurrent: 8, MaxPerHour: 100}, links, runs)

	const workers = 50
	runIDs := make(chan string, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			projection, err := service.OnAlert(context.Background(), alertEventForInvestigation("event-concurrent"))
			if err != nil {
				t.Errorf("concurrent OnAlert: %v", err)
				return
			}
			runIDs <- projection.RunID
		}()
	}
	wg.Wait()
	close(runIDs)

	unique := map[string]struct{}{}
	for id := range runIDs {
		if id != "" {
			unique[id] = struct{}{}
		}
	}
	if len(unique) != 1 {
		t.Fatalf("concurrent triggers produced %d distinct runs: %v", len(unique), unique)
	}
	if len(runs.runs) != 1 {
		t.Fatalf("runs = %d, want exactly one", len(runs.runs))
	}
}

func TestOnAlertFailsClosedWhenPersistenceUnavailable(t *testing.T) {
	service := &AlertInvestigationService{
		policies: failingPolicyReader{}, links: newFakeAlertLinks(), runs: &fakeAlertRuns{},
		now: func() time.Time { return time.Now().UTC() }, newID: randomUUID,
	}
	projection, err := service.OnAlert(context.Background(), alertEventForInvestigation("event-1"))
	if err == nil {
		t.Fatal("persistence failure must surface an error")
	}
	if projection.ReasonCode != store.AlertInvestigationPersistenceUnavailable {
		t.Fatalf("reason code = %s", projection.ReasonCode)
	}
}

type failingPolicyReader struct{}

func (failingPolicyReader) Get(string, string) (*store.AlertInvestigationPolicy, error) {
	return nil, errors.New("mysql unavailable")
}

func TestOnAlertRejectsInvalidScope(t *testing.T) {
	service := newTestAlertService(nil, newFakeAlertLinks(), &fakeAlertRuns{})
	event := alertEventForInvestigation("event-1")
	event.Cluster = ""
	if _, err := service.OnAlert(context.Background(), event); err == nil {
		t.Fatal("alert without cluster must fail closed")
	}
}

func TestAlertTargetTypeMapping(t *testing.T) {
	cases := map[string]string{
		"pod/orders-api": "pod", "deployment/orders": "deployment", "node/worker-1": "node",
		"vm/shared-vm": "vm", "pvc/data-1": "resource", "": "node",
	}
	for object, want := range cases {
		if got := alertTargetType(object, "kubernetes"); got != want {
			t.Fatalf("alertTargetType(%q) = %s, want %s", object, got, want)
		}
	}
	if got := alertTargetType("", "order-service"); got != "alert" {
		t.Fatalf("non-kubernetes fallback = %s", got)
	}
}

func TestAlertInvestigationProjectionJSONOmitsEmpty(t *testing.T) {
	if AlertInvestigationProjectionJSON(AlertInvestigationProjection{}) != "" {
		t.Fatal("empty projection must render as an empty string")
	}
	raw := AlertInvestigationProjectionJSON(AlertInvestigationProjection{Mode: "auto_readonly", Status: "run", RunID: "r"})
	for _, key := range []string{`"mode":"auto_readonly"`, `"status":"run"`, `"run_id":"r"`} {
		if !strings.Contains(raw, key) {
			t.Fatalf("projection JSON missing %s: %s", key, raw)
		}
	}
}

func (f *fakeAlertLinks) MarkSourceResolved(tenantID, clusterID, alertEventID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := alertLinkKey(tenantID, clusterID, alertEventID)
	link, ok := f.links[key]
	if !ok {
		return nil
	}
	link.SourceResolved = true
	f.links[key] = link
	return nil
}

func (f fakeAlertPolicies) Upsert(policy store.AlertInvestigationPolicy) error {
	f.policy = &policy
	return nil
}

func (failingPolicyReader) Upsert(store.AlertInvestigationPolicy) error { return errors.New("mysql unavailable") }
