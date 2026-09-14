package store

import (
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// alertTestDB wires a sqlmock connection into the store package and returns the
// DAOs used by the alert investigation path.
type alertTestHandle struct {
	policies AlertInvestigationPolicyDAO
	runs     *AIRunDAO
}

func alertPolicyTestDB(t *testing.T) (alertTestHandle, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	previous := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(previous) })
	return alertTestHandle{policies: AlertInvestigationPolicyDAO{}, runs: &AIRunDAO{}}, mock
}

func TestAlertInvestigationPolicyDefaultsToManual(t *testing.T) {
	handle, mock := alertPolicyTestDB(t)
	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT tenant_id, cluster_id, COALESCE(mode,'manual'), COALESCE(minimum_severity,'critical'),")).
		WillReturnError(errors.New("no rows"))

	if _, err := handle.policies.Get("tenant-1", "cluster-a"); err == nil {
		// sqlmock returns the injected error; the DAO must surface it, never
		// silently downgrade a persistence failure to "manual".
		t.Fatal("persistence failure must not be downgraded to the manual default")
	}

	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT tenant_id, cluster_id, COALESCE(mode,'manual'), COALESCE(minimum_severity,'critical'),")).
		WillReturnRows(sqlmock.NewRows([]string{
			"tenant_id", "cluster_id", "mode", "minimum_severity", "max_concurrent", "max_per_hour", "updated_by", "created_at", "updated_at",
		}))

	policy, err := handle.policies.Get("tenant-1", "cluster-a")
	if err != nil {
		t.Fatalf("Get absent policy: %v", err)
	}
	if policy.Mode != AlertInvestigationManual {
		t.Fatalf("absent policy must be strictly manual, got %s", policy.Mode)
	}
	if policy.MinimumSeverity != "critical" || policy.MaxConcurrent != 2 || policy.MaxPerHour != 10 {
		t.Fatalf("absent policy = %+v", policy)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAlertInvestigationPolicyValidation(t *testing.T) {
	base := AlertInvestigationPolicy{TenantID: "t", ClusterID: "c", Mode: AlertInvestigationManual, MinimumSeverity: "critical", MaxConcurrent: 2, MaxPerHour: 10}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
	for name, mutate := range map[string]func(*AlertInvestigationPolicy){
		"invalid mode":     func(p *AlertInvestigationPolicy) { p.Mode = "auto_full_execute" },
		"empty tenant":     func(p *AlertInvestigationPolicy) { p.TenantID = "" },
		"zero concurrency": func(p *AlertInvestigationPolicy) { p.MaxConcurrent = 0 },
		"high concurrency": func(p *AlertInvestigationPolicy) { p.MaxConcurrent = 9 },
		"zero rate":        func(p *AlertInvestigationPolicy) { p.MaxPerHour = 0 },
		"high rate":        func(p *AlertInvestigationPolicy) { p.MaxPerHour = 101 },
		"invalid severity": func(p *AlertInvestigationPolicy) { p.MinimumSeverity = "sev1" },
		"missing cluster":  func(p *AlertInvestigationPolicy) { p.ClusterID = "" },
		"unknown severity": func(p *AlertInvestigationPolicy) { p.MinimumSeverity = "" },
	} {
		t.Run(name, func(t *testing.T) {
			policy := base
			mutate(&policy)
			if err := policy.Validate(); err == nil {
				t.Fatalf("invalid policy accepted: %+v", policy)
			}
		})
	}
	// Boundary values inside the documented ranges must be accepted.
	for _, concurrent := range []int{1, 8} {
		policy := base
		policy.MaxConcurrent = concurrent
		if err := policy.Validate(); err != nil {
			t.Fatalf("max_concurrent=%d rejected: %v", concurrent, err)
		}
	}
	for _, perHour := range []int{1, 100} {
		policy := base
		policy.MaxPerHour = perHour
		if err := policy.Validate(); err != nil {
			t.Fatalf("max_per_hour=%d rejected: %v", perHour, err)
		}
	}
}

func TestAlertRunLinkTransactionality(t *testing.T) {
	handle, mock := alertPolicyTestDB(t)
	now := time.Now().UTC()
	run := AIRun{
		RunID: "11111111-1111-4111-8111-111111111111", RequestID: "alert:cluster-a:event-1",
		TenantID: "tenant-1", Principal: "system", PrincipalType: "system",
		ScopeKind: "single_cluster", PrimaryClusterID: "cluster-a", Intent: "investigate alert",
		ActionMode: "read_only", TargetType: "alert", TargetResourceID: "event-1",
		Status: "created", StateVersion: 0, CreatedAt: now, UpdatedAt: now,
	}
	outbox := AIRunOutbox{InvocationID: "inv-1"}
	link := AlertRunLink{
		TenantID: "tenant-1", ClusterID: "cluster-a", AlertEventID: "event-1",
		AlertSignature: "sig", RunID: run.RunID, Decision: AlertRunCreated,
	}

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_runs")).WithArgs(
		run.RunID, run.RequestID, run.TenantID, run.Principal, run.PrincipalType,
		nil, run.ScopeKind, run.PrimaryClusterID, run.Intent, run.ActionMode,
		run.TargetType, run.TargetResourceID, nil, nil,
		run.Status, run.StateVersion, nil, run.CreatedAt, run.UpdatedAt,
	).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_run_outbox")).WithArgs(
		outbox.InvocationID, run.RunID, "pending", outbox.DispatchCount, run.CreatedAt, run.UpdatedAt,
	).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_alert_run_links")).WithArgs(
		link.TenantID, link.ClusterID, link.AlertEventID, link.AlertSignature,
		link.RunID, string(link.Decision), nil, 0,
	).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	created, err := handle.runs.CreateRunWithAlertLink(run, outbox, link)
	if err != nil || !created {
		t.Fatalf("CreateRunWithAlertLink = %v, %v", created, err)
	}

	// Outbox failure must roll back the Run and never write the link.
	failing, fmock := alertPolicyTestDB(t)
	fmock.ExpectBegin()
	fmock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_runs")).WillReturnResult(sqlmock.NewResult(1, 1))
	fmock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_run_outbox")).WillReturnError(errors.New("outbox down"))
	fmock.ExpectRollback()
	if _, err := failing.runs.CreateRunWithAlertLink(run, outbox, link); err == nil {
		t.Fatal("outbox failure must roll back the whole transaction")
	}

	// Link failure must roll back the Run and the outbox entry.
	failing2, lmock := alertPolicyTestDB(t)
	lmock.ExpectBegin()
	lmock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_runs")).WillReturnResult(sqlmock.NewResult(1, 1))
	lmock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_run_outbox")).WillReturnResult(sqlmock.NewResult(1, 1))
	lmock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_alert_run_links")).WillReturnError(errors.New("link down"))
	lmock.ExpectRollback()
	if _, err := failing2.runs.CreateRunWithAlertLink(run, outbox, link); err == nil {
		t.Fatal("link failure must roll back the whole transaction")
	}
}

func TestAlertRunLinkRejectsInconsistentDecisions(t *testing.T) {
	handle, _ := alertPolicyTestDB(t)
	now := time.Now().UTC()
	run := AIRun{RunID: "r", RequestID: "q", TenantID: "t", CreatedAt: now, UpdatedAt: now}

	cases := []struct {
		name string
		link AlertRunLink
	}{
		{"missing decision", AlertRunLink{TenantID: "t", ClusterID: "c", AlertEventID: "e"}},
		{"created without run", AlertRunLink{TenantID: "t", ClusterID: "c", AlertEventID: "e", Decision: AlertRunCreated}},
		{"skipped with run", AlertRunLink{TenantID: "t", ClusterID: "c", AlertEventID: "e", Decision: AlertRunSkipped, RunID: "run-1"}},
		{"draft with run", AlertRunLink{TenantID: "t", ClusterID: "c", AlertEventID: "e", Decision: AlertRunDraft, RunID: "run-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := handle.runs.CreateRunWithAlertLink(run, AIRunOutbox{}, tc.link); err == nil {
				t.Fatalf("inconsistent link accepted: %+v", tc.link)
			}
		})
	}
}
