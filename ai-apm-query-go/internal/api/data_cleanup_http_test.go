package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

type fakeDataCleanupOperationStore struct {
	operations map[string]store.DataCleanupOperation
}

type cleanupMutatorFunc func(context.Context, string, string) error

func (f cleanupMutatorFunc) Exec(ctx context.Context, sql, queryID string) error {
	return f(ctx, sql, queryID)
}

func (f *fakeDataCleanupOperationStore) Create(op store.DataCleanupOperation) error {
	if f.operations == nil {
		f.operations = map[string]store.DataCleanupOperation{}
	}
	f.operations[op.PreviewID] = op
	return nil
}

func (f *fakeDataCleanupOperationStore) GetByPreviewID(_, previewID string) (*store.DataCleanupOperation, error) {
	op, ok := f.operations[previewID]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return &op, nil
}

func (f *fakeDataCleanupOperationStore) GetByOperationID(_, operationID string) (*store.DataCleanupOperation, error) {
	for _, op := range f.operations {
		if op.OperationID == operationID {
			copy := op
			return &copy, nil
		}
	}
	return nil, sql.ErrNoRows
}

func (f *fakeDataCleanupOperationStore) ConsumePreview(_, previewID, _, _ string, now time.Time) (bool, error) {
	op, ok := f.operations[previewID]
	if !ok || op.Status != "preview" || !op.ExpiresAt.After(now) {
		return false, nil
	}
	op.Status, op.UpdatedAt = "queued", now
	f.operations[previewID] = op
	return true, nil
}

func (f *fakeDataCleanupOperationStore) MarkRunning(_, operationID string, now time.Time) (bool, error) {
	for key, op := range f.operations {
		if op.OperationID == operationID && op.Status == "queued" {
			op.Status, op.UpdatedAt = "running", now
			f.operations[key] = op
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeDataCleanupOperationStore) Finish(_, operationID, status string, result []byte, now time.Time) (bool, error) {
	for key, op := range f.operations {
		if op.OperationID == operationID {
			op.Status, op.ResultJSON, op.UpdatedAt = status, result, now
			f.operations[key] = op
			return true, nil
		}
	}
	return false, nil
}

func TestDataCleanupPreviewRejectsInvalidRequestBeforeBackendAccess(t *testing.T) {
	h := &Handler{cleanupService: &DataCleanupService{now: func() time.Time {
		return time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	}}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/data-cleanups/preview", strings.NewReader(`{"scopes":[],"cutoff_at":"2026-08-01T00:00:00Z"}`))
	req = withAuthorizationContext(req, AuthorizationContext{UserID: "admin-1", TenantID: "tenant-1"})
	rec := httptest.NewRecorder()
	h.DataCleanupPreview(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestDataCleanupPreviewReturnsPreviewAndOneTimeToken(t *testing.T) {
	// This test intentionally exercises the HTTP contract with a fake backend;
	// the SQL-backed DAO expectations are added with the implementation.
	h := &Handler{cleanupService: &DataCleanupService{
		now:      func() time.Time { return time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC) },
		queryer:  cleanupQueryerFunc(func(_ context.Context, _ string) ([]byte, error) { return []byte("3\n"), nil }),
		dao:      &fakeDataCleanupOperationStore{},
		newID:    func() string { return "preview-1" },
		newToken: func() string { return "confirm-1" },
	}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/data-cleanups/preview", bytes.NewBufferString(`{"scopes":["clickhouse_telemetry"],"cutoff_at":"2026-08-01T00:00:00Z","idempotency_key":"idem-1"}`))
	req = withAuthorizationContext(req, AuthorizationContext{UserID: "admin-1", TenantID: "tenant-1"})
	rec := httptest.NewRecorder()
	h.DataCleanupPreview(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["preview_id"] != "preview-1" || body["confirmation_token"] != "confirm-1" {
		t.Fatalf("preview response = %+v", body)
	}
}

func TestDataCleanupExecuteRejectsWrongConfirmation(t *testing.T) {
	h := &Handler{cleanupService: &DataCleanupService{now: time.Now, dao: &fakeDataCleanupOperationStore{}}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/data-cleanups/execute", strings.NewReader(`{"preview_id":"preview-1","request_digest":"digest-1","confirmation_token":"wrong","idempotency_key":"idem-1"}`))
	req = withAuthorizationContext(req, AuthorizationContext{UserID: "admin-1", TenantID: "tenant-1"})
	rec := httptest.NewRecorder()
	h.DataCleanupExecute(rec, req)
	if rec.Code != http.StatusConflict && rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want confirmation conflict/not-found; body=%s", rec.Code, rec.Body.String())
	}
}

func TestDataCleanupExecuteConsumesConfirmationAndDoesNotRepeatMutation(t *testing.T) {
	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	normalized, err := normalizeDataCleanupRequest(DataCleanupRequest{
		Scopes: []string{"clickhouse_telemetry"}, CutoffAt: "2026-08-01T00:00:00Z", TenantID: "tenant-1", IdempotencyKey: "idem-1",
	}, "tenant-1", now)
	if err != nil {
		t.Fatal(err)
	}
	storeFake := &fakeDataCleanupOperationStore{operations: map[string]store.DataCleanupOperation{
		"preview-1": {
			OperationID: "op-1", PreviewID: "preview-1", TenantID: "tenant-1", UserID: "admin-1",
			RequestDigest: normalized.RequestDigest, ConfirmationHash: dataCleanupTokenHash("confirm-1"),
			IdempotencyKey: "idem-1", CanonicalRequest: normalized.CanonicalJSON, Status: "preview",
			ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now, UpdatedAt: now,
		},
	}}
	mutations := 0
	h := &Handler{cleanupService: &DataCleanupService{
		dao: storeFake, mutator: cleanupMutatorFunc(func(_ context.Context, _, _ string) error { mutations++; return nil }),
		now: func() time.Time { return now }, goFunc: func(fn func()) { fn() },
	}}
	payload := `{"preview_id":"preview-1","request_digest":"` + normalized.RequestDigest + `","confirmation_token":"confirm-1","idempotency_key":"idem-1"}`
	for attempt := 0; attempt < 2; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/data-cleanups/execute", strings.NewReader(payload))
		req = withAuthorizationContext(req, AuthorizationContext{UserID: "admin-1", TenantID: "tenant-1"})
		rec := httptest.NewRecorder()
		h.DataCleanupExecute(rec, req)
		if attempt == 0 && rec.Code != http.StatusAccepted {
			t.Fatalf("first status = %d, body=%s", rec.Code, rec.Body.String())
		}
		if attempt == 1 && rec.Code != http.StatusOK {
			t.Fatalf("replay status = %d, body=%s", rec.Code, rec.Body.String())
		}
	}
	if mutations != 7 {
		t.Fatalf("mutations = %d, want 7 on first execute only", mutations)
	}
}

func TestDataCleanupSessionClientUsesDirectionalTokenAndOperationDigest(t *testing.T) {
	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	normalized, err := normalizeDataCleanupRequest(DataCleanupRequest{
		Scopes: []string{"ai_sessions"}, CutoffAt: "2026-08-01T00:00:00Z", TenantID: "tenant-1", IdempotencyKey: "idem-1",
	}, "tenant-1", now)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/data-cleanups/ai-sessions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("X-Internal-Token") != "directional-token" || r.Header.Get("X-Cleanup-Request-Digest") != normalized.RequestDigest {
			t.Fatalf("headers = %+v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"scope":"ai_sessions","table":"sessions","estimated_rows":4}`))
	}))
	defer server.Close()
	client := &dataCleanupSessionClient{baseURL: server.URL, token: "directional-token", client: server.Client()}
	item, err := client.PreviewAISessions(context.Background(), normalized)
	if err != nil {
		t.Fatalf("PreviewAISessions() error = %v", err)
	}
	if item.EstimatedRows != 4 || item.Table != "sessions" {
		t.Fatalf("item = %+v", item)
	}
}

func TestDataCleanupRoutesAreCanonicalProtected(t *testing.T) {
	for _, path := range []string{
		"/api/v1/admin/data-cleanups/preview",
		"/api/v1/admin/data-cleanups/execute",
		"/api/v1/admin/data-cleanups/op-1",
	} {
		if !isCanonicalProtectedRoute(path) {
			t.Fatalf("cleanup path %q is not canonical-protected", path)
		}
	}
}
