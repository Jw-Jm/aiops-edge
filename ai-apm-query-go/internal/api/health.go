package api

import (
	"database/sql"
	"net/http"
)

// HealthHandler separates process liveness from authoritative dependency
// readiness. The dependency and schema functions are injected so probes can be
// tested without opening a network listener or a real MySQL connection.
type HealthHandler struct {
	dbFn     func() *sql.DB
	schemaFn func(*sql.DB) error
}

func NewHealthHandler(dbFn func() *sql.DB, schemaFn func(*sql.DB) error) *HealthHandler {
	return &HealthHandler{dbFn: dbFn, schemaFn: schemaFn}
}

// Livez only answers whether the HTTP process is alive.
func (h *HealthHandler) Livez(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// Readyz answers whether the authoritative MySQL dependency and schema are
// usable. A missing or unhealthy dependency is never reported as Ready.
func (h *HealthHandler) Readyz(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.dbFn == nil {
		http.Error(w, "mysql readiness check is not configured", http.StatusServiceUnavailable)
		return
	}
	db := h.dbFn()
	if db == nil {
		http.Error(w, "mysql unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := db.PingContext(r.Context()); err != nil {
		http.Error(w, "mysql unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	if h.schemaFn != nil {
		if err := h.schemaFn(db); err != nil {
			http.Error(w, "schema not ready: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}
