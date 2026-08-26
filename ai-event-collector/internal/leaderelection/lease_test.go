package leaderelection

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeKube 模拟 coordination.k8s.io/v1 Lease API（get/create/update）。
type fakeKube struct {
	mu    sync.Mutex
	lease map[string]string // key: name, value: holderIdentity (""=不存在)
	calls []string
}

func (f *fakeKube) serve(t *testing.T) *httptest.Server {
	if f.lease == nil {
		f.lease = map[string]string{}
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		// 真实 K8s API 语义：POST 到集合 URL /.../leases（创建），
		// GET/PUT 到单资源 URL /.../leases/{name}。此前 fake server 把集合 URL 的
		// POST 也当成单资源（name 解析为空串），掩盖了真实 API 的 405 create 缺陷。
		p := strings.TrimPrefix(r.URL.Path, "/apis/coordination.k8s.io/v1/namespaces/default/leases")
		isCollection := r.Method == http.MethodPost && (p == "" || p == "/")
		name := ""
		if !isCollection {
			parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
			name = parts[len(parts)-1]
		}
		switch r.Method {
		case http.MethodPost:
			// 集合 URL：从 body 的 metadata.name 取 name
			var l kubeLease
			_ = json.NewDecoder(r.Body).Decode(&l)
			if l.Metadata.Name == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			name = l.Metadata.Name
			f.lease[name] = l.Spec.HolderIdentity
			f.calls = append(f.calls, "POST "+name)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"name": name, "resourceVersion": "101"}})
		case http.MethodGet:
			f.calls = append(f.calls, "GET "+name)
			if _, ok := f.lease[name]; !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"metadata": map[string]any{"name": name, "resourceVersion": "100"},
				"spec": map[string]any{
					"holderIdentity":       f.lease[name],
					"leaseDurationSeconds": 15,
					"renewTime":            "2026-08-20T10:00:00Z",
				},
			})
		case http.MethodPut:
			f.calls = append(f.calls, "PUT "+name)
			var l kubeLease
			_ = json.NewDecoder(r.Body).Decode(&l)
			if l.Metadata.ResourceVersion == "" {
				// Real Kubernetes Update requests must carry the current
				// metadata.resourceVersion; otherwise an expired Lease can never
				// be reclaimed by a restarted collector.
				w.WriteHeader(http.StatusConflict)
				return
			}
			f.lease[name] = l.Spec.HolderIdentity
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"resourceVersion": "102"}})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
}

func TestLeaseClientCreateAndUpdate(t *testing.T) {
	fk := &fakeKube{}
	srv := fk.serve(t)
	defer srv.Close()
	c := NewLeaseClient(srv.URL)
	ctx := context.Background()

	if err := c.Create(ctx, "default", "aiops-events", "pod-a"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	info, err := c.Get(ctx, "default", "aiops-events")
	if err != nil {
		t.Fatalf("Get after create: %v", err)
	}
	if info.Holder != "pod-a" {
		t.Fatalf("holderIdentity = %q, want pod-a", info.Holder)
	}
	if info.RenewTime.IsZero() {
		t.Fatal("expected renewTime parsed from lease")
	}
	if err := c.Update(ctx, "default", "aiops-events", "pod-a"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	info, _ = c.Get(ctx, "default", "aiops-events")
	if info.Holder != "pod-a" {
		t.Fatalf("holderIdentity after update = %q", info.Holder)
	}
}

func TestLeaseClientGetMissingReturnsEmpty(t *testing.T) {
	fk := &fakeKube{}
	srv := fk.serve(t)
	defer srv.Close()
	c := NewLeaseClient(srv.URL)
	info, err := c.Get(context.Background(), "default", "missing")
	if err != nil {
		t.Fatalf("Get missing: %v", err)
	}
	if info.Holder != "" {
		t.Fatalf("missing lease holderIdentity = %q, want empty", info.Holder)
	}
}
