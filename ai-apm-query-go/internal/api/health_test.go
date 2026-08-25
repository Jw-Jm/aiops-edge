package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLivezDoesNotRequireMySQL(t *testing.T) {
	h := NewHealthHandler(func() *sql.DB { return nil }, func(*sql.DB) error { return nil })
	rec := httptest.NewRecorder()
	h.Livez(rec, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("livez status = %d, want 200", rec.Code)
	}
}

func TestReadyzFailsWhenMySQLIsUnavailable(t *testing.T) {
	h := NewHealthHandler(func() *sql.DB { return nil }, func(*sql.DB) error { return nil })
	rec := httptest.NewRecorder()
	h.Readyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz status = %d, want 503", rec.Code)
	}
}
