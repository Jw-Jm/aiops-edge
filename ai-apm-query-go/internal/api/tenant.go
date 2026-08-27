package api

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// Tenant represents a platform tenant
type Tenant struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	QuotaAI int    `json:"quota_ai_calls"` // max AI calls per day, 0=unlimited
	Enabled bool   `json:"enabled"`
}

var (
	tenants   = make(map[string]*Tenant)
	tenantsMu sync.RWMutex
)

func init() {
	loadTenants()
}

func loadTenants() {
	tenantsMu.Lock()
	defer tenantsMu.Unlock()
	d := &store.TenantDAO{}
	rows, err := d.LoadAll()
	if err != nil || len(rows) == 0 {
		// default tenant（MySQL 空则初始化并持久化）
		tenants = map[string]*Tenant{"default": {ID: "default", Name: "默认租户", QuotaAI: 0, Enabled: true}}
		if err == nil {
			_ = d.ReplaceAll([]store.Tenant{{ID: "default", Name: "默认租户", QuotaAI: 0, Enabled: true}})
		}
		return
	}
	tenants = make(map[string]*Tenant, len(rows))
	for i := range rows {
		t := rows[i]
		tenants[t.ID] = &Tenant{ID: t.ID, Name: t.Name, QuotaAI: t.QuotaAI, Enabled: t.Enabled}
	}
}

// saveTenants 将内存租户表持久化到 MySQL。
// 注意：调用方必须已持有 tenantsMu 写锁（sync.RWMutex 不可重入，此处不再内部加锁，
// 否则持有写锁时再取读锁会永久死锁）。
func saveTenants() error {
	d := &store.TenantDAO{}
	list := make([]store.Tenant, 0, len(tenants))
	for _, t := range tenants {
		list = append(list, store.Tenant{ID: t.ID, Name: t.Name, QuotaAI: t.QuotaAI, Enabled: t.Enabled})
	}
	return d.ReplaceAll(list)
}

// ListTenants handles GET /api/v1/tenants
func (h *Handler) ListTenants(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondJSON(w, 405, map[string]interface{}{"error": "method not allowed"})
		return
	}
	tenantsMu.RLock()
	defer tenantsMu.RUnlock()
	list := make([]Tenant, 0, len(tenants))
	for _, t := range tenants {
		list = append(list, *t)
	}
	respondJSON(w, 200, map[string]interface{}{"tenants": list, "total": len(list)})
}

// CreateTenant handles POST /api/v1/tenants
func (h *Handler) CreateTenant(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, 405, map[string]interface{}{"error": "method not allowed"})
		return
	}
	// 安全(P2-2)：租户写接口 admin 门禁（与 users/catalog 等写分支一致）。
	if !isAdmin(w, r) {
		return
	}
	body, _ := io.ReadAll(r.Body)
	var t Tenant
	if json.Unmarshal(body, &t) != nil || t.ID == "" {
		respondJSON(w, 400, map[string]interface{}{"error": "invalid tenant data, id is required"})
		return
	}
	if t.Name == "" {
		t.Name = t.ID
	}
	t.Enabled = true

	tenantsMu.Lock()
	tenants[t.ID] = &t
	saveTenants()
	tenantsMu.Unlock()

	auditWrite(r, "tenant.create", t.ID, "创建租户 name="+t.Name)
	respondJSON(w, 201, map[string]interface{}{"tenant": t})
}

// DeleteTenant handles DELETE /api/v1/tenants/{id}
func (h *Handler) DeleteTenant(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		respondJSON(w, 405, map[string]interface{}{"error": "method not allowed"})
		return
	}
	// 安全(P2-2)：租户写接口 admin 门禁。
	if !isAdmin(w, r) {
		return
	}
	id := r.URL.Path[len("/api/v1/tenants/"):]
	if id == "default" {
		respondJSON(w, 400, map[string]interface{}{"error": "cannot delete default tenant"})
		return
	}
	tenantsMu.Lock()
	delete(tenants, id)
	saveTenants()
	tenantsMu.Unlock()
	auditWrite(r, "tenant.delete", id, "删除租户")
	respondJSON(w, 200, map[string]interface{}{"message": "deleted", "id": id})
}
