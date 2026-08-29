package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/query"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

const (
	dataCleanupScopeAISessions      = "ai_sessions"
	dataCleanupScopeAlertEvents     = "alert_events"
	dataCleanupScopeClickHouse      = "clickhouse_telemetry"
	dataCleanupPreviewTTL           = 10 * time.Minute
	dataCleanupMaxIdempotencyLength = 128
)

// DataCleanupRequest is the public preview payload. CutoffAt remains a string
// at the transport boundary so timestamps without an explicit timezone can be
// rejected instead of silently interpreted in the server's local timezone.
type DataCleanupRequest struct {
	Scopes         []string `json:"scopes"`
	CutoffAt       string   `json:"cutoff_at"`
	TenantID       string   `json:"tenant_id,omitempty"`
	ClusterID      string   `json:"cluster_id,omitempty"`
	IdempotencyKey string   `json:"idempotency_key"`
}

type normalizedDataCleanupRequest struct {
	Scopes         []string
	CutoffAt       time.Time
	TenantID       string
	ClusterID      string
	IdempotencyKey string
	RequestDigest  string
	CanonicalJSON  []byte
}

type dataCleanupTableSpec struct {
	Scope           string
	Table           string
	TimeColumn      string
	DateColumn      bool
	HasTenant       bool
	RequiresCluster bool
	ResolvedOnly    bool
}

type dataCleanupStatement struct {
	Scope     string
	Table     string
	CountSQL  string
	DeleteSQL string
}

type dataCleanupQueryer interface {
	Query(context.Context, string) ([]byte, error)
}

type dataCleanupPlanItem struct {
	Scope         string `json:"scope"`
	Table         string `json:"table"`
	EstimatedRows int64  `json:"estimated_rows"`
	CountSQL      string `json:"-"`
	DeleteSQL     string `json:"-"`
}

type dataCleanupResultItem struct {
	Scope      string `json:"scope"`
	Table      string `json:"table"`
	Status     string `json:"status"`
	Rows       int64  `json:"rows"`
	MutationID string `json:"mutation_id,omitempty"`
	Error      string `json:"error,omitempty"`
}

type dataCleanupOperationStore interface {
	Create(store.DataCleanupOperation) error
	GetByPreviewID(string, string) (*store.DataCleanupOperation, error)
	GetByOperationID(string, string) (*store.DataCleanupOperation, error)
	ConsumePreview(string, string, string, string, time.Time) (bool, error)
	MarkRunning(string, string, time.Time) (bool, error)
	Finish(string, string, string, []byte, time.Time) (bool, error)
}

type dataCleanupMutator interface {
	Exec(context.Context, string, string) error
}

type dataCleanupSessionBackend interface {
	PreviewAISessions(context.Context, normalizedDataCleanupRequest) (dataCleanupPlanItem, error)
	DeleteAISessions(context.Context, string, string, normalizedDataCleanupRequest) (dataCleanupResultItem, error)
}

type dataCleanupSessionClient struct {
	baseURL string
	token   string
	client  *http.Client
}

// DataCleanupService owns the preview/confirmation/async execution lifecycle.
// Dependencies are interfaces so the destructive path is testable without
// touching a real ClickHouse, orchestrator or MySQL instance.
type DataCleanupService struct {
	dao      dataCleanupOperationStore
	queryer  dataCleanupQueryer
	mutator  dataCleanupMutator
	sessions dataCleanupSessionBackend
	audit    func(store.DataCleanupOperation, string, string, []byte) error
	now      func() time.Time
	newID    func() string
	newToken func() string
	goFunc   func(func())
}

type dataCleanupPreviewResponse struct {
	OperationID       string                `json:"operation_id"`
	PreviewID         string                `json:"preview_id"`
	RequestDigest     string                `json:"request_digest"`
	ConfirmationToken string                `json:"confirmation_token"`
	ExpiresAt         time.Time             `json:"expires_at"`
	Plan              []dataCleanupPlanItem `json:"plan"`
}

type dataCleanupExecuteRequest struct {
	PreviewID         string `json:"preview_id"`
	RequestDigest     string `json:"request_digest"`
	ConfirmationToken string `json:"confirmation_token"`
	IdempotencyKey    string `json:"idempotency_key"`
}

type dataCleanupStatusResponse struct {
	OperationID string                  `json:"operation_id"`
	PreviewID   string                  `json:"preview_id"`
	Status      string                  `json:"status"`
	Result      []dataCleanupResultItem `json:"result,omitempty"`
	CreatedAt   time.Time               `json:"created_at"`
	UpdatedAt   time.Time               `json:"updated_at"`
}

func newDataCleanupService(queryer dataCleanupQueryer, mutator dataCleanupMutator, sessions dataCleanupSessionBackend) *DataCleanupService {
	service := &DataCleanupService{
		dao:      &store.DataCleanupDAO{},
		queryer:  queryer,
		mutator:  mutator,
		sessions: sessions,
		now:      time.Now,
		newID:    randomDataCleanupToken,
		newToken: randomDataCleanupToken,
	}
	service.audit = func(operation store.DataCleanupOperation, action, result string, detail []byte) error {
		return (&store.DataCleanupDAO{}).RecordAudit(operation.TenantID, operation.UserID, action, result, detail, time.Now().UTC())
	}
	service.goFunc = func(fn func()) { go fn() }
	return service
}

func newDataCleanupSessionClient() *dataCleanupSessionClient {
	return &dataCleanupSessionClient{
		baseURL: orchestratorBase(),
		token:   strings.TrimSpace(os.Getenv("QUERY_TO_ORCHESTRATOR_TOKEN")),
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *dataCleanupSessionClient) PreviewAISessions(ctx context.Context, request normalizedDataCleanupRequest) (dataCleanupPlanItem, error) {
	body, err := c.call(ctx, request, "", true)
	if err != nil {
		return dataCleanupPlanItem{}, err
	}
	var response struct {
		EstimatedRows int64 `json:"estimated_rows"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.EstimatedRows < 0 {
		return dataCleanupPlanItem{}, errors.New("invalid ai_sessions preview response")
	}
	return dataCleanupPlanItem{Scope: dataCleanupScopeAISessions, Table: "sessions", EstimatedRows: response.EstimatedRows}, nil
}

func (c *dataCleanupSessionClient) DeleteAISessions(ctx context.Context, operationID, requestDigest string, request normalizedDataCleanupRequest) (dataCleanupResultItem, error) {
	body, err := c.call(ctx, request, operationID, false)
	if err != nil {
		return dataCleanupResultItem{}, err
	}
	var response struct {
		DeletedSessions    int64 `json:"deleted_sessions"`
		DeletedCheckpoints int64 `json:"deleted_checkpoints"`
		DeletedWrites      int64 `json:"deleted_writes"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.DeletedSessions < 0 || response.DeletedCheckpoints < 0 || response.DeletedWrites < 0 {
		return dataCleanupResultItem{}, errors.New("invalid ai_sessions cleanup response")
	}
	return dataCleanupResultItem{
		Scope: dataCleanupScopeAISessions, Table: "sessions", Status: "deleted", Rows: response.DeletedSessions,
		MutationID: operationID + "-ai_sessions",
	}, nil
}

func (c *dataCleanupSessionClient) call(ctx context.Context, request normalizedDataCleanupRequest, operationID string, preview bool) ([]byte, error) {
	if c == nil || c.baseURL == "" || c.token == "" {
		return nil, errors.New("ai_sessions backend is not configured")
	}
	payload := map[string]any{
		"preview": preview, "cutoff_at": request.CutoffAt.UTC().Format(time.RFC3339Nano),
		"tenant_id": request.TenantID, "cluster_id": request.ClusterID,
		"operation_id": operationID, "request_digest": request.RequestDigest,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.baseURL, "/")+"/internal/v1/data-cleanups/ai-sessions", strings.NewReader(string(encoded)))
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("X-Internal-Token", c.token)
	httpRequest.Header.Set("X-Cleanup-Operation-Id", operationID)
	httpRequest.Header.Set("X-Cleanup-Request-Digest", request.RequestDigest)
	client := c.client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if readErr != nil {
		return nil, readErr
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("ai_sessions backend returned %s: %s", response.Status, strings.TrimSpace(string(responseBody)))
	}
	return responseBody, nil
}

var dataCleanupTableSpecs = []dataCleanupTableSpec{
	{Scope: dataCleanupScopeAlertEvents, Table: "alert_events", TimeColumn: "last_timestamp", HasTenant: false, RequiresCluster: true, ResolvedOnly: true},
	{Scope: dataCleanupScopeClickHouse, Table: "log_records", TimeColumn: "timestamp", HasTenant: true},
	{Scope: dataCleanupScopeClickHouse, Table: "service_topology", TimeColumn: "time_bucket", HasTenant: true},
	{Scope: dataCleanupScopeClickHouse, Table: "change_records", TimeColumn: "start_time", HasTenant: true},
	{Scope: dataCleanupScopeClickHouse, Table: "trace_spans", TimeColumn: "start_time", HasTenant: true},
	{Scope: dataCleanupScopeClickHouse, Table: "trace_summary_state", TimeColumn: "date", DateColumn: true, HasTenant: true},
	{Scope: dataCleanupScopeClickHouse, Table: "trace_summary_index", TimeColumn: "time_bucket", HasTenant: true},
	{Scope: dataCleanupScopeClickHouse, Table: "k8s_events", TimeColumn: "time_bucket", HasTenant: true},
}

func normalizeDataCleanupRequest(request DataCleanupRequest, currentTenant string, now time.Time) (normalizedDataCleanupRequest, error) {
	currentTenant = strings.TrimSpace(currentTenant)
	if currentTenant == "" {
		return normalizedDataCleanupRequest{}, fmt.Errorf("tenant context is required")
	}

	scopeSet := make(map[string]struct{}, len(request.Scopes))
	for _, raw := range request.Scopes {
		scope := strings.TrimSpace(raw)
		if scope == "" {
			continue
		}
		if scope != dataCleanupScopeAISessions && scope != dataCleanupScopeAlertEvents && scope != dataCleanupScopeClickHouse {
			return normalizedDataCleanupRequest{}, fmt.Errorf("unknown scope %q", scope)
		}
		scopeSet[scope] = struct{}{}
	}
	if len(scopeSet) == 0 {
		return normalizedDataCleanupRequest{}, fmt.Errorf("at least one cleanup scope is required")
	}
	scopes := make([]string, 0, len(scopeSet))
	for scope := range scopeSet {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)

	cutoffRaw := strings.TrimSpace(request.CutoffAt)
	if cutoffRaw == "" {
		return normalizedDataCleanupRequest{}, fmt.Errorf("cutoff time is required")
	}
	cutoff, err := time.Parse(time.RFC3339, cutoffRaw)
	if err != nil {
		return normalizedDataCleanupRequest{}, fmt.Errorf("invalid cutoff time: RFC3339 timezone is required")
	}
	cutoff = cutoff.UTC()
	if cutoff.After(now.UTC()) {
		return normalizedDataCleanupRequest{}, fmt.Errorf("cutoff time cannot be in the future")
	}

	tenantID := strings.TrimSpace(request.TenantID)
	if tenantID == "" {
		tenantID = currentTenant
	}
	if tenantID != currentTenant {
		return normalizedDataCleanupRequest{}, fmt.Errorf("tenant scope does not match authorized tenant")
	}
	clusterID := strings.TrimSpace(request.ClusterID)
	if len(clusterID) > 128 || strings.ContainsRune(clusterID, 0) {
		return normalizedDataCleanupRequest{}, fmt.Errorf("invalid cluster scope")
	}
	if _, ok := scopeSet[dataCleanupScopeAlertEvents]; ok && clusterID == "" {
		return normalizedDataCleanupRequest{}, fmt.Errorf("alert_events scope requires cluster scope")
	}

	idempotencyKey := strings.TrimSpace(request.IdempotencyKey)
	if idempotencyKey == "" {
		return normalizedDataCleanupRequest{}, fmt.Errorf("idempotency key is required")
	}
	if len(idempotencyKey) > dataCleanupMaxIdempotencyLength || strings.ContainsRune(idempotencyKey, 0) {
		return normalizedDataCleanupRequest{}, fmt.Errorf("invalid idempotency key")
	}

	canonical := struct {
		Scopes         []string `json:"scopes"`
		CutoffAt       string   `json:"cutoff_at"`
		TenantID       string   `json:"tenant_id"`
		ClusterID      string   `json:"cluster_id,omitempty"`
		IdempotencyKey string   `json:"idempotency_key"`
	}{
		Scopes: scopes, CutoffAt: cutoff.Format(time.RFC3339Nano), TenantID: tenantID,
		ClusterID: clusterID, IdempotencyKey: idempotencyKey,
	}
	canonicalJSON, err := json.Marshal(canonical)
	if err != nil {
		return normalizedDataCleanupRequest{}, fmt.Errorf("canonical cleanup request: %w", err)
	}
	digest := sha256.Sum256(canonicalJSON)
	return normalizedDataCleanupRequest{
		Scopes: scopes, CutoffAt: cutoff, TenantID: tenantID, ClusterID: clusterID,
		IdempotencyKey: idempotencyKey, RequestDigest: hex.EncodeToString(digest[:]),
		CanonicalJSON: canonicalJSON,
	}, nil
}

func buildDataCleanupStatements(request normalizedDataCleanupRequest) []dataCleanupStatement {
	allowed := make(map[string]struct{}, len(request.Scopes))
	for _, scope := range request.Scopes {
		allowed[scope] = struct{}{}
	}
	cutoff := cleanupTimeLiteral(request.CutoffAt)
	statements := make([]dataCleanupStatement, 0, len(dataCleanupTableSpecs))
	for _, spec := range dataCleanupTableSpecs {
		if _, ok := allowed[spec.Scope]; !ok {
			continue
		}
		conditions := []string{}
		if spec.DateColumn {
			conditions = append(conditions, spec.TimeColumn+" < toDate("+cleanupStringLiteral(request.CutoffAt.UTC().Format("2006-01-02"))+")")
		} else {
			conditions = append(conditions, spec.TimeColumn+" < "+cutoff)
		}
		if spec.HasTenant {
			conditions = append(conditions, "tenant_id="+cleanupStringLiteral(request.TenantID))
		}
		if request.ClusterID != "" {
			conditions = append(conditions, "cluster_id="+cleanupStringLiteral(request.ClusterID))
		}
		if spec.ResolvedOnly {
			conditions = append(conditions, "status='resolved'")
		}
		where := strings.Join(conditions, " AND ")
		statements = append(statements, dataCleanupStatement{
			Scope: spec.Scope, Table: spec.Table,
			CountSQL:  "SELECT count() FROM observability." + spec.Table + " WHERE " + where,
			DeleteSQL: "ALTER TABLE observability." + spec.Table + " DELETE WHERE " + where,
		})
	}
	return statements
}

func collectDataCleanupPlan(ctx context.Context, queryer dataCleanupQueryer, request normalizedDataCleanupRequest) ([]dataCleanupPlanItem, error) {
	items := make([]dataCleanupPlanItem, 0)
	for _, statement := range buildDataCleanupStatements(request) {
		body, err := queryer.Query(ctx, statement.CountSQL)
		if err != nil {
			var queryErr *query.QueryError
			if errors.As(err, &queryErr) && queryErr.Code == query.NoDataCode {
				body = []byte("0")
			} else {
				return nil, fmt.Errorf("count %s.%s: %w", statement.Scope, statement.Table, err)
			}
		}
		count, err := strconv.ParseInt(strings.TrimSpace(string(body)), 10, 64)
		if err != nil || count < 0 {
			return nil, fmt.Errorf("invalid count for %s.%s", statement.Scope, statement.Table)
		}
		items = append(items, dataCleanupPlanItem{
			Scope: statement.Scope, Table: statement.Table, EstimatedRows: count,
			CountSQL: statement.CountSQL, DeleteSQL: statement.DeleteSQL,
		})
	}
	return items, nil
}

func (h *Handler) DataCleanupPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	auth, ok := requestAuthorizationContext(r)
	if !ok || auth.TenantID == "" {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "permission_denied"})
		return
	}
	if h.cleanupService == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "cleanup_unavailable"})
		return
	}
	var request DataCleanupRequest
	if err := decodeDataCleanupJSON(r, &request); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "validation_failed", "message": err.Error()})
		return
	}
	normalized, err := normalizeDataCleanupRequest(request, auth.TenantID, h.cleanupService.currentTime())
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "validation_failed", "message": err.Error()})
		return
	}
	if h.cleanupService.dao == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "cleanup_unavailable"})
		return
	}
	plan, err := h.cleanupService.preview(r.Context(), normalized)
	if err != nil {
		respondJSON(w, http.StatusBadGateway, map[string]string{"error": "cleanup_preview_failed", "message": err.Error()})
		return
	}
	previewID := h.cleanupService.id()
	operationID := h.cleanupService.id()
	confirmationToken := h.cleanupService.token()
	expiresAt := h.cleanupService.currentTime().UTC().Add(dataCleanupPreviewTTL)
	planJSON, err := json.Marshal(plan)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "cleanup_preview_failed"})
		return
	}
	createdAt := h.cleanupService.currentTime().UTC()
	operation := store.DataCleanupOperation{
		OperationID: operationID, PreviewID: previewID, TenantID: auth.TenantID, UserID: auth.UserID,
		RequestDigest: normalized.RequestDigest, ConfirmationHash: dataCleanupTokenHash(confirmationToken),
		IdempotencyKey: normalized.IdempotencyKey, CanonicalRequest: normalized.CanonicalJSON, PlanJSON: planJSON,
		Status: "preview", ExpiresAt: expiresAt, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	if err := h.cleanupService.dao.Create(operation); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			respondJSON(w, http.StatusConflict, map[string]string{"error": "idempotency_key_reused"})
			return
		}
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "cleanup_persistence_unavailable"})
		return
	}
	h.cleanupService.recordAudit(operation, "data_cleanup.preview", "success", normalized.CanonicalJSON)
	respondJSON(w, http.StatusCreated, dataCleanupPreviewResponse{
		OperationID: operationID, PreviewID: previewID, RequestDigest: normalized.RequestDigest,
		ConfirmationToken: confirmationToken, ExpiresAt: expiresAt, Plan: plan,
	})
}

func (h *Handler) DataCleanupExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	auth, ok := requestAuthorizationContext(r)
	if !ok || auth.TenantID == "" {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "permission_denied"})
		return
	}
	if h.cleanupService == nil || h.cleanupService.dao == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "cleanup_unavailable"})
		return
	}
	var request dataCleanupExecuteRequest
	if err := decodeDataCleanupJSON(r, &request); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "validation_failed", "message": err.Error()})
		return
	}
	request.PreviewID = strings.TrimSpace(request.PreviewID)
	request.RequestDigest = strings.TrimSpace(request.RequestDigest)
	request.ConfirmationToken = strings.TrimSpace(request.ConfirmationToken)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if request.PreviewID == "" || request.RequestDigest == "" || request.ConfirmationToken == "" || request.IdempotencyKey == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "confirmation_fields_required"})
		return
	}
	op, err := h.cleanupService.dao.GetByPreviewID(auth.TenantID, request.PreviewID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondJSON(w, http.StatusNotFound, map[string]string{"error": "preview_not_found"})
			return
		}
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "cleanup_persistence_unavailable"})
		return
	}
	confirmationHash := dataCleanupTokenHash(request.ConfirmationToken)
	if op.RequestDigest != request.RequestDigest || op.IdempotencyKey != request.IdempotencyKey ||
		subtle.ConstantTimeCompare([]byte(op.ConfirmationHash), []byte(confirmationHash)) != 1 {
		respondJSON(w, http.StatusConflict, map[string]string{"error": "confirmation_mismatch"})
		return
	}
	if op.Status != "preview" {
		h.respondExistingCleanupOperation(w, op)
		return
	}
	now := h.cleanupService.currentTime().UTC()
	normalized, err := normalizedRequestFromCanonical(op.CanonicalRequest, auth.TenantID, now)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "invalid_persisted_request"})
		return
	}
	consumed, err := h.cleanupService.dao.ConsumePreview(auth.TenantID, op.PreviewID, request.RequestDigest, confirmationHash, now)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "cleanup_persistence_unavailable"})
		return
	}
	if !consumed {
		latest, latestErr := h.cleanupService.dao.GetByPreviewID(auth.TenantID, op.PreviewID)
		if latestErr == nil && latest.Status != "preview" {
			h.respondExistingCleanupOperation(w, latest)
			return
		}
		respondJSON(w, http.StatusConflict, map[string]string{"error": "preview_expired_or_consumed"})
		return
	}
	h.cleanupService.recordAudit(*op, "data_cleanup.execute", "accepted", op.CanonicalRequest)
	op.Status = "queued"
	h.cleanupService.launch(*op, normalized)
	respondJSON(w, http.StatusAccepted, dataCleanupStatusResponse{
		OperationID: op.OperationID, PreviewID: op.PreviewID, Status: "queued",
		CreatedAt: op.CreatedAt, UpdatedAt: now,
	})
}

func (h *Handler) DataCleanupStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	auth, ok := requestAuthorizationContext(r)
	if !ok || auth.TenantID == "" || h.cleanupService == nil || h.cleanupService.dao == nil {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "permission_denied"})
		return
	}
	operationID := strings.TrimPrefix(r.URL.Path, "/api/v1/admin/data-cleanups/")
	operationID = strings.TrimSpace(strings.TrimSuffix(operationID, "/"))
	if operationID == "" || operationID == "preview" || operationID == "execute" {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "operation_not_found"})
		return
	}
	op, err := h.cleanupService.dao.GetByOperationID(auth.TenantID, operationID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondJSON(w, http.StatusNotFound, map[string]string{"error": "operation_not_found"})
			return
		}
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "cleanup_persistence_unavailable"})
		return
	}
	respondJSON(w, http.StatusOK, h.cleanupService.statusResponse(op))
}

func (h *Handler) respondExistingCleanupOperation(w http.ResponseWriter, operation *store.DataCleanupOperation) {
	status := http.StatusOK
	if operation.Status == "queued" || operation.Status == "running" {
		status = http.StatusAccepted
	}
	respondJSON(w, status, h.cleanupService.statusResponse(operation))
}

func (s *DataCleanupService) preview(ctx context.Context, request normalizedDataCleanupRequest) ([]dataCleanupPlanItem, error) {
	items := make([]dataCleanupPlanItem, 0)
	if containsDataCleanupScope(request.Scopes, dataCleanupScopeAISessions) {
		if s.sessions == nil {
			return nil, errors.New("ai_sessions backend unavailable")
		}
		item, err := s.sessions.PreviewAISessions(ctx, request)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if containsDataCleanupScope(request.Scopes, dataCleanupScopeAlertEvents) || containsDataCleanupScope(request.Scopes, dataCleanupScopeClickHouse) {
		if s.queryer == nil {
			return nil, errors.New("clickhouse backend unavailable")
		}
		clickhouseItems, err := collectDataCleanupPlan(ctx, s.queryer, request)
		if err != nil {
			return nil, err
		}
		items = append(items, clickhouseItems...)
	}
	return items, nil
}

func (s *DataCleanupService) launch(operation store.DataCleanupOperation, request normalizedDataCleanupRequest) {
	launch := s.goFunc
	if launch == nil {
		launch = func(fn func()) { go fn() }
	}
	launch(func() { s.run(context.Background(), operation, request) })
}

func (s *DataCleanupService) run(ctx context.Context, operation store.DataCleanupOperation, request normalizedDataCleanupRequest) {
	now := s.currentTime().UTC()
	if s.dao == nil {
		return
	}
	if ok, err := s.dao.MarkRunning(operation.TenantID, operation.OperationID, now); err != nil || !ok {
		return
	}
	results := make([]dataCleanupResultItem, 0)
	failed := false
	if containsDataCleanupScope(request.Scopes, dataCleanupScopeAISessions) {
		if s.sessions == nil {
			results = append(results, dataCleanupResultItem{Scope: dataCleanupScopeAISessions, Table: "sessions", Status: "failed", Error: "ai_sessions backend unavailable"})
			failed = true
		} else if result, err := s.sessions.DeleteAISessions(ctx, operation.OperationID, request.RequestDigest, request); err != nil {
			results = append(results, dataCleanupResultItem{Scope: dataCleanupScopeAISessions, Table: "sessions", Status: "failed", Error: err.Error()})
			failed = true
		} else {
			results = append(results, result)
		}
	}
	if s.mutator != nil {
		for _, statement := range buildDataCleanupStatements(request) {
			if statement.Scope == dataCleanupScopeAlertEvents && !containsDataCleanupScope(request.Scopes, dataCleanupScopeAlertEvents) {
				continue
			}
			if statement.Scope == dataCleanupScopeClickHouse && !containsDataCleanupScope(request.Scopes, dataCleanupScopeClickHouse) {
				continue
			}
			if err := s.mutator.Exec(ctx, statement.DeleteSQL, operation.OperationID+"-"+statement.Table); err != nil {
				var queryErr *query.QueryError
				if errors.As(err, &queryErr) && queryErr.Code == query.NoDataCode {
					// ClickHouse returns an empty body for a successfully submitted mutation.
				} else {
					results = append(results, dataCleanupResultItem{Scope: statement.Scope, Table: statement.Table, Status: "failed", Error: err.Error()})
					failed = true
					continue
				}
			}
			results = append(results, dataCleanupResultItem{Scope: statement.Scope, Table: statement.Table, Status: "submitted", MutationID: operation.OperationID + "-" + statement.Table})
		}
	}
	finalStatus := "succeeded"
	if failed {
		finalStatus = "failed"
	}
	resultJSON, _ := json.Marshal(results)
	_, _ = s.dao.Finish(operation.TenantID, operation.OperationID, finalStatus, resultJSON, s.currentTime().UTC())
	s.recordAudit(operation, "data_cleanup.execute", finalStatus, resultJSON)
}

func (s *DataCleanupService) statusResponse(operation *store.DataCleanupOperation) dataCleanupStatusResponse {
	var result []dataCleanupResultItem
	if len(operation.ResultJSON) > 0 {
		_ = json.Unmarshal(operation.ResultJSON, &result)
	}
	return dataCleanupStatusResponse{
		OperationID: operation.OperationID, PreviewID: operation.PreviewID, Status: operation.Status,
		Result: result, CreatedAt: operation.CreatedAt, UpdatedAt: operation.UpdatedAt,
	}
}

func (s *DataCleanupService) currentTime() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

func (s *DataCleanupService) recordAudit(operation store.DataCleanupOperation, action, result string, detail []byte) {
	if s.audit != nil {
		_ = s.audit(operation, action, result, detail)
	}
}

func (s *DataCleanupService) id() string {
	if s.newID == nil {
		return randomDataCleanupToken()
	}
	return s.newID()
}

func (s *DataCleanupService) token() string {
	if s.newToken == nil {
		return randomDataCleanupToken()
	}
	return s.newToken()
}

func containsDataCleanupScope(scopes []string, wanted string) bool {
	for _, scope := range scopes {
		if scope == wanted {
			return true
		}
	}
	return false
}

func decodeDataCleanupJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 128*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func normalizedRequestFromCanonical(raw []byte, tenantID string, now time.Time) (normalizedDataCleanupRequest, error) {
	var request DataCleanupRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return normalizedDataCleanupRequest{}, err
	}
	return normalizeDataCleanupRequest(request, tenantID, now)
}

func dataCleanupTokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func randomDataCleanupToken() string {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}

func cleanupTimeLiteral(value time.Time) string {
	value = value.UTC()
	if value.Nanosecond() == 0 {
		return cleanupStringLiteral(value.Format("2006-01-02 15:04:05"))
	}
	return cleanupStringLiteral(strings.TrimRight(strings.TrimRight(value.Format("2006-01-02 15:04:05.999999999"), "0"), "."))
}

func cleanupStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
