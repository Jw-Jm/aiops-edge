package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// AlertInvestigationProjection is what the alert list exposes about the
// governed alert → investigation decision. Skipped reasons are never hidden.
type AlertInvestigationProjection struct {
	Mode       string `json:"mode"`
	Status     string `json:"status"`
	ReasonCode string `json:"reason_code,omitempty"`
	RunID      string `json:"run_id,omitempty"`
}

// The service depends on narrow interfaces so the decision logic can be proven
// without MySQL while production keeps using the real DAOs.
type AlertInvestigationPolicyReader interface {
	Get(tenantID, clusterID string) (*store.AlertInvestigationPolicy, error)
	Upsert(policy store.AlertInvestigationPolicy) error
}

type AlertRunLinkStore interface {
	Get(tenantID, clusterID, alertEventID string) (*store.AlertRunLink, error)
	Insert(link store.AlertRunLink) error
	MarkSourceResolved(tenantID, clusterID, alertEventID string) error
	CountActiveAutoRuns(tenantID, clusterID string) (int, error)
	CountRecentLinks(tenantID, clusterID string, window time.Duration) (int, error)
}

type AlertRunCreator interface {
	CreateRunWithAlertLink(r store.AIRun, o store.AIRunOutbox, link store.AlertRunLink) (bool, error)
}

// AlertInvestigationService is the single decision point between a firing alert
// and the existing Run/outbox state machine. It never creates an Action, an
// approval, or an executor request.
type AlertInvestigationService struct {
	policies AlertInvestigationPolicyReader
	links    AlertRunLinkStore
	runs     AlertRunCreator
	// now and newID are injectable for deterministic tests.
	now   func() time.Time
	newID func() string
}

// NewAlertInvestigationService wires the production dependencies.
func NewAlertInvestigationService() *AlertInvestigationService {
	return &AlertInvestigationService{
		policies: store.AlertInvestigationPolicyDAO{},
		links:    store.AlertRunLinkDAO{},
		runs:     &store.AIRunDAO{},
		now:      func() time.Time { return time.Now().UTC() },
		newID:    newAlertInvestigationRunID,
	}
}

// severityRank orders severities so a policy minimum can be compared.
func severityRank(severity string) int {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		return 3
	case "warning", "warn":
		return 2
	default:
		return 1
	}
}

// alertTargetType maps the alert object to a canonical Run target type. The
// fallback is "alert": the Run still investigates, it just does not claim a
// resource identity it cannot prove.
func alertTargetType(object, service string) string {
	object = strings.ToLower(strings.TrimSpace(object))
	switch {
	case strings.HasPrefix(object, "pod/"):
		return "pod"
	case strings.HasPrefix(object, "deployment/"):
		return "deployment"
	case strings.HasPrefix(object, "node/"):
		return "node"
	case strings.HasPrefix(object, "vm/"), strings.HasPrefix(object, "vmi/"):
		return "vm"
	case strings.TrimSpace(object) != "":
		return "resource"
	}
	if strings.EqualFold(strings.TrimSpace(service), "kubernetes") {
		return "node"
	}
	return "alert"
}

// frozenInvestigationWindow derives the read-only evidence window. The total
// span is capped at 24h so a long-running alert cannot freeze an unbounded query.
func frozenInvestigationWindow(first, last string, now time.Time) (time.Time, time.Time) {
	start := now.Add(-15 * time.Minute)
	if parsed, err := time.Parse(time.RFC3339, first); err == nil {
		start = parsed.Add(-15 * time.Minute)
	}
	end := now.Add(5 * time.Minute)
	if parsed, err := time.Parse(time.RFC3339, last); err == nil {
		end = parsed.Add(5 * time.Minute)
	}
	if end.Before(start) {
		end = start
	}
	if max := start.Add(24 * time.Hour); end.After(max) {
		end = max
	}
	return start, end
}

// OnAlert applies the cluster policy to one alert event.
//
// It is idempotent: the same event always yields the same decision, and the
// first created Run wins. Every skipped reason is persisted so the UI can show
// why nothing happened.
func (s *AlertInvestigationService) OnAlert(ctx context.Context, event AlertEvent) (AlertInvestigationProjection, error) {
	tenantID, clusterID := strings.TrimSpace(event.TenantID), strings.TrimSpace(event.Cluster)
	if tenantID == "" || clusterID == "" || strings.TrimSpace(event.ID) == "" {
		return AlertInvestigationProjection{Mode: string(store.AlertInvestigationManual), Status: "skipped", ReasonCode: store.AlertInvestigationInvalidScope},
			errorsNew("alert investigation requires tenant, cluster and event id")
	}

	policy, err := s.policies.Get(tenantID, clusterID)
	if err != nil || policy == nil {
		// A persistence failure must not silently disable automation; the caller
		// logs it and the compensation scan retries.
		return AlertInvestigationProjection{Mode: string(store.AlertInvestigationManual), Status: "skipped", ReasonCode: store.AlertInvestigationPersistenceUnavailable},
			errorsNew("alert investigation policy unavailable")
	}

	// manual: no link, no run. The user decides.
	if policy.Mode == store.AlertInvestigationManual {
		return AlertInvestigationProjection{Mode: string(policy.Mode), Status: "none", ReasonCode: store.AlertInvestigationPolicyManual}, nil
	}

	// Idempotency first: a replayed alert must not create a second link.
	existing, err := s.links.Get(tenantID, clusterID, event.ID)
	if err != nil {
		return AlertInvestigationProjection{Mode: string(policy.Mode), Status: "skipped", ReasonCode: store.AlertInvestigationPersistenceUnavailable}, err
	}
	if existing != nil {
		return AlertInvestigationProjection{
			Mode:       string(policy.Mode),
			Status:     mapDecisionStatus(existing.Decision),
			ReasonCode: existing.ReasonCode,
			RunID:      existing.RunID,
		}, nil
	}

	signature := strings.TrimSpace(event.Signature)
	if severityRank(event.Severity) < severityRank(policy.MinimumSeverity) {
		link := store.AlertRunLink{TenantID: tenantID, ClusterID: clusterID, AlertEventID: event.ID,
			AlertSignature: signature, Decision: store.AlertRunSkipped, ReasonCode: store.AlertInvestigationBelowSeverity}
		if linkErr := s.links.Insert(link); linkErr != nil {
			return AlertInvestigationProjection{Mode: string(policy.Mode), Status: "skipped", ReasonCode: store.AlertInvestigationPersistenceUnavailable}, linkErr
		}
		return AlertInvestigationProjection{Mode: string(policy.Mode), Status: "skipped", ReasonCode: store.AlertInvestigationBelowSeverity}, nil
	}

	if policy.Mode == store.AlertInvestigationDraft {
		// Draft: record the accepted draft only. No Run, no AI quota consumption.
		link := store.AlertRunLink{TenantID: tenantID, ClusterID: clusterID, AlertEventID: event.ID,
			AlertSignature: signature, Decision: store.AlertRunDraft}
		if linkErr := s.links.Insert(link); linkErr != nil {
			return AlertInvestigationProjection{Mode: string(policy.Mode), Status: "skipped", ReasonCode: store.AlertInvestigationPersistenceUnavailable}, linkErr
		}
		return AlertInvestigationProjection{Mode: string(policy.Mode), Status: "draft"}, nil
	}

	// auto_readonly: severity passed, now enforce concurrency and rate limits.
	active, err := s.links.CountActiveAutoRuns(tenantID, clusterID)
	if err != nil {
		return AlertInvestigationProjection{Mode: string(policy.Mode), Status: "skipped", ReasonCode: store.AlertInvestigationPersistenceUnavailable}, err
	}
	if active >= policy.MaxConcurrent {
		link := store.AlertRunLink{TenantID: tenantID, ClusterID: clusterID, AlertEventID: event.ID,
			AlertSignature: signature, Decision: store.AlertRunSkipped, ReasonCode: store.AlertInvestigationConcurrencyLimited}
		if linkErr := s.links.Insert(link); linkErr != nil {
			return AlertInvestigationProjection{Mode: string(policy.Mode), Status: "skipped", ReasonCode: store.AlertInvestigationPersistenceUnavailable}, linkErr
		}
		return AlertInvestigationProjection{Mode: string(policy.Mode), Status: "skipped", ReasonCode: store.AlertInvestigationConcurrencyLimited}, nil
	}
	recent, err := s.links.CountRecentLinks(tenantID, clusterID, time.Hour)
	if err != nil {
		return AlertInvestigationProjection{Mode: string(policy.Mode), Status: "skipped", ReasonCode: store.AlertInvestigationPersistenceUnavailable}, err
	}
	if recent >= policy.MaxPerHour {
		link := store.AlertRunLink{TenantID: tenantID, ClusterID: clusterID, AlertEventID: event.ID,
			AlertSignature: signature, Decision: store.AlertRunSkipped, ReasonCode: store.AlertInvestigationRateLimited}
		if linkErr := s.links.Insert(link); linkErr != nil {
			return AlertInvestigationProjection{Mode: string(policy.Mode), Status: "skipped", ReasonCode: store.AlertInvestigationPersistenceUnavailable}, linkErr
		}
		return AlertInvestigationProjection{Mode: string(policy.Mode), Status: "skipped", ReasonCode: store.AlertInvestigationRateLimited}, nil
	}

	now := s.now()
	// run_id 与 request_id 都必须确定性派生：同一告警事件的重放（并发、重启、
	// 补偿扫描）必须落在同一个 Run 上，而不是创建兄弟 Run。
	runID := idempotencyRequestID(tenantID, "alert-run:"+clusterID+":"+event.ID)
	start, end := frozenInvestigationWindow(event.FirstTimestamp, event.LastTimestamp, now)
	run := store.AIRun{
		RunID:            runID,
		RequestID:        idempotencyRequestID(tenantID, "alert:"+clusterID+":"+event.ID),
		TenantID:         tenantID,
		Principal:        "system",
		PrincipalType:    "system",
		ScopeKind:        "single_cluster",
		PrimaryClusterID: clusterID,
		Intent:           "告警自动只读调查：" + strings.TrimSpace(event.RuleName),
		ActionMode:       "read_only",
		TargetType:       alertTargetType(event.Object, event.Service),
		TargetResourceID: strings.TrimSpace(event.Object),
		TimeRangeStart:   &start,
		TimeRangeEnd:     &end,
		Status:           "created",
		StateVersion:     0,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	link := store.AlertRunLink{TenantID: tenantID, ClusterID: clusterID, AlertEventID: event.ID,
		AlertSignature: signature, RunID: runID, Decision: store.AlertRunCreated}
	created, err := s.runs.CreateRunWithAlertLink(run, store.AIRunOutbox{InvocationID: s.newID()}, link)
	if err != nil {
		return AlertInvestigationProjection{Mode: string(policy.Mode), Status: "skipped", ReasonCode: store.AlertInvestigationPersistenceUnavailable}, err
	}
	if !created {
		// Concurrent creation lost the race: surface the winning Run.
		if winner, getErr := s.links.Get(tenantID, clusterID, event.ID); getErr == nil && winner != nil {
			return AlertInvestigationProjection{Mode: string(policy.Mode), Status: mapDecisionStatus(winner.Decision), ReasonCode: winner.ReasonCode, RunID: winner.RunID}, nil
		}
	}
	return AlertInvestigationProjection{Mode: string(policy.Mode), Status: "run", RunID: runID}, nil
}

// MarkAlertSourceResolved records that the alert recovered. It never rewrites
// the frozen window and never cancels a started investigation.
func (s *AlertInvestigationService) MarkAlertSourceResolved(tenantID, clusterID, eventID string) error {
	return s.links.MarkSourceResolved(tenantID, clusterID, eventID)
}

// CompensationScan re-evaluates recent firing critical alerts through the same
// idempotent service. It is bounded and never blocks or fails alert ingestion.
func (s *AlertInvestigationService) CompensationScan(limit int) int {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	events := recentFiringCriticalAlerts(limit)
	applied := 0
	for _, event := range events {
		if _, err := s.OnAlert(context.Background(), event); err == nil {
			applied++
		}
	}
	return applied
}

// recentFiringCriticalAlerts returns the newest firing critical alerts from the
// in-memory authoritative event list.
func recentFiringCriticalAlerts(limit int) []AlertEvent {
	alertEventsMu.RLock()
	defer alertEventsMu.RUnlock()
	out := make([]AlertEvent, 0, limit)
	for i := len(alertEvents) - 1; i >= 0 && len(out) < limit; i-- {
		event := alertEvents[i]
		if !strings.EqualFold(event.Status, "firing") || !strings.EqualFold(event.Severity, "critical") {
			continue
		}
		out = append(out, event)
	}
	return out
}

func mapDecisionStatus(decision store.AlertRunLinkDecision) string {
	switch decision {
	case store.AlertRunCreated:
		return "run"
	case store.AlertRunDraft:
		return "draft"
	default:
		return "skipped"
	}
}

// AlertInvestigationProjectionJSON renders the projection for the alert payload.
func AlertInvestigationProjectionJSON(projection AlertInvestigationProjection) string {
	if projection.Mode == "" {
		return ""
	}
	raw, err := json.Marshal(projection)
	if err != nil {
		return ""
	}
	return string(raw)
}

// ── HTTP handlers ────────────────────────────────────────────────────────────

// AlertInvestigationPolicyRouter handles GET/PUT /api/v1/system/alert-investigation-policy.
func (h *Handler) AlertInvestigationPolicyRouter(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	auth, ok := requestAuthorizationContext(r)
	if !ok {
		respondAuthorizationError(w, authorizationFailure("permission_denied"))
		return
	}
	clusterID := strings.TrimSpace(r.URL.Query().Get("cluster_id"))
	if clusterID == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "cluster_id is required"})
		return
	}
	// 系统管理权限：仅 admin 可读/写自动化策略（写端点必须显式授权）。
	if !hasRole(r, "admin") {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden: admin role required"})
		return
	}
	service := NewAlertInvestigationService()
	switch r.Method {
	case http.MethodGet:
		policy, err := service.policies.Get(auth.TenantID, clusterID)
		if err != nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ALERT_INVESTIGATION_POLICY_UNAVAILABLE"})
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"tenant_id": policy.TenantID, "cluster_id": policy.ClusterID,
			"mode": string(policy.Mode), "minimum_severity": policy.MinimumSeverity,
			"max_concurrent": policy.MaxConcurrent, "max_per_hour": policy.MaxPerHour,
			"updated_by": policy.UpdatedBy,
		})
	case http.MethodPut:
		var body struct {
			Mode            string `json:"mode"`
			MinimumSeverity string `json:"minimum_severity"`
			MaxConcurrent   int    `json:"max_concurrent"`
			MaxPerHour      int    `json:"max_per_hour"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid policy body"})
			return
		}
		existing, err := service.policies.Get(auth.TenantID, clusterID)
		if err != nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ALERT_INVESTIGATION_POLICY_UNAVAILABLE"})
			return
		}
		policy := store.AlertInvestigationPolicy{
			TenantID: auth.TenantID, ClusterID: clusterID,
			Mode:            store.AlertInvestigationMode(strings.TrimSpace(body.Mode)),
			MinimumSeverity: orDefault(body.MinimumSeverity, existing.MinimumSeverity),
			MaxConcurrent:   orDefaultInt(body.MaxConcurrent, existing.MaxConcurrent),
			MaxPerHour:      orDefaultInt(body.MaxPerHour, existing.MaxPerHour),
			UpdatedBy:       auth.UserID,
		}
		if err := policy.Validate(); err != nil {
			respondJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		if err := service.policies.Upsert(policy); err != nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ALERT_INVESTIGATION_POLICY_UNAVAILABLE"})
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"cluster_id": clusterID, "mode": string(policy.Mode),
			"minimum_severity": policy.MinimumSeverity,
			"max_concurrent":   policy.MaxConcurrent, "max_per_hour": policy.MaxPerHour,
			"note": "自动调查仅只读，不会执行处置",
		})
	}
}

// AlertInvestigationSubrouter dispatches /api/v1/alerts/{event_id}/investigation.
// More specific /api/v1/alerts/{rules,events,silences} subtrees are registered
// separately and win over this catch-all.
func (h *Handler) AlertInvestigationSubrouter(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if strings.HasSuffix(path, "/investigation") {
		h.AcceptAlertInvestigation(w, r)
		return
	}
	respondJSON(w, http.StatusNotFound, map[string]string{"error": "ALERT_ROUTE_NOT_FOUND"})
}

// AcceptAlertInvestigation handles POST /api/v1/alerts/{event_id}/investigation.
// A user explicitly accepts a draft or opens an investigation for a manual alert.
func (h *Handler) AcceptAlertInvestigation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	auth, ok := requestAuthorizationContext(r)
	if !ok {
		respondAuthorizationError(w, authorizationFailure("permission_denied"))
		return
	}
	eventID := strings.TrimPrefix(r.URL.Path, "/api/v1/alerts/")
	eventID = strings.TrimSuffix(eventID, "/investigation")
	if eventID == "" {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "ALERT_NOT_FOUND"})
		return
	}

	alertEventsMu.RLock()
	var event *AlertEvent
	for i := range alertEvents {
		if alertEvents[i].ID == eventID {
			event = &alertEvents[i]
			break
		}
	}
	alertEventsMu.RUnlock()
	if event == nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "ALERT_NOT_FOUND"})
		return
	}
	clusterID := strings.TrimSpace(event.Cluster)
	if clusterID == "" || (auth.ActiveClusterID != "" && auth.ActiveClusterID != clusterID) {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "PERMISSION_DENIED"})
		return
	}
	if event.TenantID != "" && event.TenantID != auth.TenantID {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "PERMISSION_DENIED"})
		return
	}

	// A browser request can never claim the system principal: the service
	// derives it from the cluster policy, never from the payload.
	if event.TenantID == "" {
		event.TenantID = auth.TenantID
	}
	service := NewAlertInvestigationService()
	projection, err := service.OnAlert(r.Context(), *event)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ALERT_INVESTIGATION_UNAVAILABLE", "reason_code": projection.ReasonCode})
		return
	}
	if projection.Status == "skipped" && projection.ReasonCode == store.AlertInvestigationInvalidScope {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "INVALID_SCOPE"})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"alert_event_id": eventID, "cluster_id": clusterID,
		"mode": projection.Mode, "status": projection.Status,
		"reason_code": projection.ReasonCode, "run_id": projection.RunID,
		"note": "自动调查仅只读，不会执行处置",
	})
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func orDefaultInt(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

// dispatchAlertInvestigation applies the cluster policy off the alert lock path.
// The authoritative decision lives in MySQL (ai_alert_run_links); the in-memory
// projection only feeds the alert list payload.
func (h *Handler) dispatchAlertInvestigation(event AlertEvent) {
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("alert-investigation panic recovered for event %s: %v", event.ID, recovered)
			}
		}()
		service := NewAlertInvestigationService()
		projection, err := service.OnAlert(context.Background(), event)
		if err != nil {
			log.Printf("alert-investigation event=%s failed: %v (reason=%s)", event.ID, err, projection.ReasonCode)
			return
		}
		alertEventsMu.Lock()
		for i := range alertEvents {
			if alertEvents[i].ID == event.ID {
				link := projection
				alertEvents[i].InvestigationLink = &link
				break
			}
		}
		alertEventsMu.Unlock()
	}()
}

// markAlertSourceResolved records recovery without rewriting the frozen window
// or cancelling a started investigation.
func (h *Handler) markAlertSourceResolved(event AlertEvent) {
	service := NewAlertInvestigationService()
	if err := service.MarkAlertSourceResolved(strings.TrimSpace(event.TenantID), strings.TrimSpace(event.Cluster), event.ID); err != nil {
		log.Printf("alert-investigation resolve event=%s: %v", event.ID, err)
	}
}

// errorsNew keeps the service free of a direct fmt dependency for simple wraps.
func errorsNew(message string) error { return errors.New(message) }

// newAlertInvestigationRunID returns a canonical UUID-shaped run identifier.
func newAlertInvestigationRunID() string { return randomUUID() }
