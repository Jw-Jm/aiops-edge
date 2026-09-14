package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// AlertInvestigationMode is the per-cluster policy that decides how a firing
// alert may open an investigation. The default (and the value used whenever no
// row exists) is manual: nothing runs without a human.
type AlertInvestigationMode string

const (
	// AlertInvestigationManual requires an explicit user action for every run.
	AlertInvestigationManual AlertInvestigationMode = "manual"
	// AlertInvestigationDraft records a draft link but never creates a Run and
	// never consumes AI quota; a user must click "start investigation".
	AlertInvestigationDraft AlertInvestigationMode = "draft"
	// AlertInvestigationAutoReadonly creates a system, read-only Run when the
	// alert passes severity/concurrency/rate gates. It can never create Actions.
	AlertInvestigationAutoReadonly AlertInvestigationMode = "auto_readonly"
)

// Reason codes are stable identifiers surfaced in the UI; they must never be
// silently swallowed.
const (
	AlertInvestigationPolicyManual          = "policy_manual"
	AlertInvestigationBelowSeverity         = "below_severity"
	AlertInvestigationConcurrencyLimited    = "concurrency_limited"
	AlertInvestigationRateLimited           = "rate_limited"
	AlertInvestigationInvalidScope          = "invalid_scope"
	AlertInvestigationPersistenceUnavailable = "persistence_unavailable"
)

// AlertInvestigationPolicy is one row per (tenant, cluster).
type AlertInvestigationPolicy struct {
	TenantID        string
	ClusterID       string
	Mode            AlertInvestigationMode
	MinimumSeverity string
	MaxConcurrent   int
	MaxPerHour      int
	UpdatedBy       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// DefaultAlertInvestigationPolicy is the fail-closed fallback: manual.
func DefaultAlertInvestigationPolicy(tenantID, clusterID string) AlertInvestigationPolicy {
	return AlertInvestigationPolicy{
		TenantID: tenantID, ClusterID: clusterID,
		Mode: AlertInvestigationManual, MinimumSeverity: "critical",
		MaxConcurrent: 2, MaxPerHour: 10,
	}
}

// Validate rejects policy values that would silently widen the automation.
func (p AlertInvestigationPolicy) Validate() error {
	if strings.TrimSpace(p.TenantID) == "" || strings.TrimSpace(p.ClusterID) == "" {
		return errors.New("tenant and cluster are required")
	}
	mode := AlertInvestigationMode(strings.ToLower(strings.TrimSpace(string(p.Mode))))
	switch mode {
	case AlertInvestigationManual, AlertInvestigationDraft, AlertInvestigationAutoReadonly:
	default:
		return errors.New("mode must be manual, draft or auto_readonly")
	}
	if p.MaxConcurrent < 1 || p.MaxConcurrent > 8 {
		return errors.New("max_concurrent must be between 1 and 8")
	}
	if p.MaxPerHour < 1 || p.MaxPerHour > 100 {
		return errors.New("max_per_hour must be between 1 and 100")
	}
	switch strings.ToLower(strings.TrimSpace(p.MinimumSeverity)) {
	case "critical", "warning", "info":
	default:
		return errors.New("minimum_severity must be critical, warning or info")
	}
	return nil
}

// AlertInvestigationPolicyDAO reads and writes per-cluster policies.
type AlertInvestigationPolicyDAO struct{}

// Get returns the stored policy, or the strict manual default when absent.
// A persistence error is returned to the caller instead of being downgraded to
// "manual" — the caller decides how to fail closed.
func (AlertInvestigationPolicyDAO) Get(tenantID, clusterID string) (*AlertInvestigationPolicy, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	row := conn.QueryRow(
		`SELECT tenant_id, cluster_id, COALESCE(mode,'manual'), COALESCE(minimum_severity,'critical'),
		        COALESCE(max_concurrent,2), COALESCE(max_per_hour,10), COALESCE(updated_by,''),
		        created_at, updated_at
		 FROM ai_alert_investigation_policies WHERE tenant_id = ? AND cluster_id = ?`,
		tenantID, clusterID,
	)
	var policy AlertInvestigationPolicy
	var mode string
	err := row.Scan(&policy.TenantID, &policy.ClusterID, &mode, &policy.MinimumSeverity,
		&policy.MaxConcurrent, &policy.MaxPerHour, &policy.UpdatedBy, &policy.CreatedAt, &policy.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		fallback := DefaultAlertInvestigationPolicy(tenantID, clusterID)
		return &fallback, nil
	}
	if err != nil {
		return nil, err
	}
	policy.Mode = AlertInvestigationMode(mode)
	return &policy, nil
}

// Upsert stores a validated policy.
func (AlertInvestigationPolicyDAO) Upsert(policy AlertInvestigationPolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(
		`INSERT INTO ai_alert_investigation_policies
		   (tenant_id, cluster_id, mode, minimum_severity, max_concurrent, max_per_hour, updated_by, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, NOW(3), NOW(3))
		 ON DUPLICATE KEY UPDATE mode = VALUES(mode), minimum_severity = VALUES(minimum_severity),
		   max_concurrent = VALUES(max_concurrent), max_per_hour = VALUES(max_per_hour),
		   updated_by = VALUES(updated_by)`,
		policy.TenantID, policy.ClusterID, string(policy.Mode), policy.MinimumSeverity,
		policy.MaxConcurrent, policy.MaxPerHour, policy.UpdatedBy,
	)
	return err
}

// AlertRunLinkDecision records what the policy decided for one alert event.
type AlertRunLinkDecision string

const (
	// AlertRunSkipped means no investigation was opened; ReasonCode explains why.
	AlertRunSkipped AlertRunLinkDecision = "skipped"
	// AlertRunDraft records an accepted draft without a Run.
	AlertRunDraft AlertRunLinkDecision = "draft"
	// AlertRunCreated records the single Run created for the alert.
	AlertRunCreated AlertRunLinkDecision = "run"
)

// AlertRunLink is the idempotent join between one alert event and at most one Run.
type AlertRunLink struct {
	TenantID       string
	ClusterID      string
	AlertEventID   string
	AlertSignature string
	RunID          string
	Decision       AlertRunLinkDecision
	ReasonCode     string
	SourceResolved bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// AlertRunLinkDAO persists the alert → Run association.
type AlertRunLinkDAO struct{}

// Get returns the existing link for an alert event, or nil when absent.
func (AlertRunLinkDAO) Get(tenantID, clusterID, alertEventID string) (*AlertRunLink, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	row := conn.QueryRow(
		`SELECT tenant_id, cluster_id, alert_event_id, alert_signature, run_id, decision,
		        COALESCE(reason_code,''), source_resolved, created_at, updated_at
		 FROM ai_alert_run_links WHERE tenant_id = ? AND cluster_id = ? AND alert_event_id = ?`,
		tenantID, clusterID, alertEventID,
	)
	return scanAlertRunLink(row)
}

// CountActiveAutoRuns returns how many auto read-only Runs are still open.
func (AlertRunLinkDAO) CountActiveAutoRuns(tenantID, clusterID string) (int, error) {
	conn := GetDB()
	if conn == nil {
		return 0, errors.New("mysql unavailable")
	}
	var count int
	err := conn.QueryRow(
		`SELECT COUNT(*) FROM ai_alert_run_links l JOIN ai_runs r ON r.run_id = l.run_id
		 WHERE l.tenant_id = ? AND l.cluster_id = ? AND l.decision = 'run'
		   AND r.status NOT IN ('success','partial','failed','regressed','cancelled')`,
		tenantID, clusterID,
	).Scan(&count)
	return count, err
}

// CountRecentLinks returns how many links were created in the last hour.
func (AlertRunLinkDAO) CountRecentLinks(tenantID, clusterID string, window time.Duration) (int, error) {
	conn := GetDB()
	if conn == nil {
		return 0, errors.New("mysql unavailable")
	}
	var count int
	err := conn.QueryRow(
		`SELECT COUNT(*) FROM ai_alert_run_links
		 WHERE tenant_id = ? AND cluster_id = ? AND created_at >= ?`,
		tenantID, clusterID, time.Now().UTC().Add(-window),
	).Scan(&count)
	return count, err
}

// InsertTx writes the link inside the Run creation transaction so that a link
// without a Run (or the reverse) can never be observed.
func (AlertRunLinkDAO) InsertTx(tx *sql.Tx, link AlertRunLink) error {
	if link.TenantID == "" || link.ClusterID == "" || link.AlertEventID == "" {
		return errors.New("alert link scope is required")
	}
	_, err := tx.Exec(
		`INSERT INTO ai_alert_run_links
		   (tenant_id, cluster_id, alert_event_id, alert_signature, run_id, decision, reason_code, source_resolved, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, NOW(3), NOW(3))
		 ON DUPLICATE KEY UPDATE alert_signature = VALUES(alert_signature)`,
		link.TenantID, link.ClusterID, link.AlertEventID, link.AlertSignature,
		nullableStr(link.RunID), string(link.Decision), nullableStr(link.ReasonCode), alertLinkBool(link.SourceResolved),
	)
	return err
}

// Insert writes a skipped/draft link outside a Run transaction.
func (d AlertRunLinkDAO) Insert(link AlertRunLink) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := d.InsertTx(tx, link); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkSourceResolved records that the alert recovered. It never rewrites the
// frozen window and never cancels a started investigation.
func (AlertRunLinkDAO) MarkSourceResolved(tenantID, clusterID, alertEventID string) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(
		`UPDATE ai_alert_run_links SET source_resolved = 1
		 WHERE tenant_id = ? AND cluster_id = ? AND alert_event_id = ?`,
		tenantID, clusterID, alertEventID,
	)
	return err
}

func scanAlertRunLink(row *sql.Row) (*AlertRunLink, error) {	var link AlertRunLink
	var decision string
	var runID sql.NullString
	var resolved int
	err := row.Scan(&link.TenantID, &link.ClusterID, &link.AlertEventID, &link.AlertSignature,
		&runID, &decision, &link.ReasonCode, &resolved, &link.CreatedAt, &link.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	link.RunID = runID.String
	link.Decision = AlertRunLinkDecision(decision)
	link.SourceResolved = resolved == 1
	return &link, nil
}

func alertLinkBool(value bool) int {
	if value {
		return 1
	}
	return 0
}

// CreateRunWithAlertLink creates the Run, its outbox invocation and the alert
// link inside one transaction. Any failure rolls all three back so a link can
// never point at a Run that does not exist, and a Run can never be orphaned
// from its alert decision.
// Returns created=false (with nil error) when the Run already existed, which is
// the idempotent replay path.
func (d *AIRunDAO) CreateRunWithAlertLink(r AIRun, o AIRunOutbox, link AlertRunLink) (bool, error) {
	if link.Decision == "" {
		return false, errors.New("alert link decision is required")
	}
	if link.Decision == AlertRunCreated && strings.TrimSpace(link.RunID) == "" {
		return false, errors.New("a created link must reference its run")
	}
	if link.Decision != AlertRunCreated && strings.TrimSpace(link.RunID) != "" {
		return false, errors.New("only a created link may reference a run")
	}
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	scope := r.ScopeKind
	if scope == "" {
		scope = "single_cluster"
	}
	status := r.Status
	if status == "" {
		status = "created"
	}
	if _, err := tx.Exec(
		`INSERT INTO ai_runs (run_id, request_id, tenant_id, principal, principal_type,
		   session_id, scope_kind, primary_cluster_id, intent, action_mode,
		   target_type, target_resource_id, time_range_start, time_range_end,
		   status, state_version, parent_run_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.RunID, r.RequestID, r.TenantID, r.Principal, r.PrincipalType,
		nullableStr(r.SessionID), scope, nullableStr(r.PrimaryClusterID), r.Intent, r.ActionMode,
		nullableStr(r.TargetType), nullableStr(r.TargetResourceID),
		nullableTime(r.TimeRangeStart), nullableTime(r.TimeRangeEnd),
		status, r.StateVersion, nullableStr(r.ParentRunID), r.CreatedAt, r.UpdatedAt,
	); err != nil {
		if isDuplicateKey(err) {
			return false, nil
		}
		return false, err
	}

	obs := o.Status
	if obs == "" {
		obs = "pending"
	}
	if o.CreatedAt.IsZero() {
		o.CreatedAt = r.CreatedAt
	}
	if o.UpdatedAt.IsZero() {
		o.UpdatedAt = r.UpdatedAt
	}
	if _, err := tx.Exec(
		`INSERT INTO ai_run_outbox (invocation_id, run_id, status, dispatch_count, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		o.InvocationID, r.RunID, obs, o.DispatchCount, o.CreatedAt, o.UpdatedAt,
	); err != nil {
		return false, err
	}

	var linkDAO AlertRunLinkDAO
	if err := linkDAO.InsertTx(tx, link); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
