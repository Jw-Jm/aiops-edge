package query

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClickHouseRepoQueryOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("t\tcall_count\n2026-08-20 10:00:00\t42\n"))
	}))
	defer srv.Close()
	repo := NewClickHouseRepo(srv.URL, nil)
	body, err := repo.Query(context.Background(), "SELECT ...")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("expected non-empty body")
	}
}

func TestClickHouseRepoQueryNoData(t *testing.T) {
	// CH 返回 200 但空 body → 应为 NoData，不是 generic 500。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	repo := NewClickHouseRepo(srv.URL, nil)
	_, err := repo.Query(context.Background(), "SELECT ...")
	if err == nil {
		t.Fatal("expected NoData error")
	}
	var qe *QueryError
	if !errors.As(err, &qe) || qe.Code != NoDataCode {
		t.Fatalf("expected NoData, got %v", err)
	}
}

func TestClickHouseRepoQueryBackendDown(t *testing.T) {
	// CH 返回 500 → Unavailable（503），可重试。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	repo := NewClickHouseRepo(srv.URL, nil)
	_, err := repo.Query(context.Background(), "SELECT ...")
	var qe *QueryError
	if !errors.As(err, &qe) || qe.Code != UnavailableCode {
		t.Fatalf("expected Unavailable, got %v", err)
	}
}

func TestClickHouseRepoQueryTimeout(t *testing.T) {
	// CH 响应慢于 ctx 超时 → Timeout（504）。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	repo := NewClickHouseRepo(srv.URL, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := repo.Query(ctx, "SELECT ...")
	if err == nil {
		t.Fatal("expected Timeout error")
	}
}

func TestClickHouseRepoWithCHAuthSetsBasicAuth(t *testing.T) {
	// AUTH 修复（503 ClickHouse）：WithCHAuth 后 Query 请求必须带 Basic Auth（default/dev-ch-pass）。
	var gotUser, gotPass string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok {
			t.Fatalf("request missing Basic Auth")
		}
		gotUser, gotPass = u, p
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("1\n"))
	}))
	defer srv.Close()
	repo := NewClickHouseRepo(srv.URL, nil).WithCHAuth("default", "dev-ch-pass")
	if _, err := repo.Query(context.Background(), "SELECT 1"); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if gotUser != "default" || gotPass != "dev-ch-pass" {
		t.Fatalf("got BasicAuth %q/%q, want default/dev-ch-pass", gotUser, gotPass)
	}
}

func TestClickHouseRepoWithoutAuthNoBasicAuth(t *testing.T) {
	// 未配置凭据（dev 兼容）→ 请求不带 Basic Auth。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := r.BasicAuth(); ok {
			t.Fatalf("request should NOT have Basic Auth without WithCHAuth")
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("1\n"))
	}))
	defer srv.Close()
	repo := NewClickHouseRepo(srv.URL, nil)
	if _, err := repo.Query(context.Background(), "SELECT 1"); err != nil {
		t.Fatalf("Query: %v", err)
	}
}
