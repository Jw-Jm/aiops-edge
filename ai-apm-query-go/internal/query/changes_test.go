package query

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mockChangesCH(t *testing.T, dispatch map[string]string) *ChangeRepository {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("query")
		for needle, out := range dispatch {
			if strings.Contains(q, needle) {
				_, _ = w.Write([]byte(out))
				return
			}
		}
		_, _ = w.Write([]byte(""))
	}))
	t.Cleanup(srv.Close)
	return NewChangeRepository(NewClickHouseRepo(srv.URL, nil))
}

func TestChangeRepoListByService(t *testing.T) {
	rows := "" +
		`{"change_id":"ch-1","service_name":"checkout","change_type":"deploy","start_time":"2026-08-20 10:00:00","actor":"alice","summary":"v1.2.3","revision":"abc123"}` + "\n" +
		`{"change_id":"ch-2","service_name":"checkout","change_type":"config","start_time":"2026-08-20 11:00:00","actor":"bob","summary":"tune pool","revision":"def456"}` + "\n"
	r := mockChangesCH(t, map[string]string{"FROM observability.change_records": rows})

	changes, err := r.List(context.Background(), ChangeScope{TenantID: "t1"}, "checkout", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("expected 2 changes, got %d", len(changes))
	}
	if changes[0].ChangeID != "ch-1" || changes[0].Service != "checkout" || changes[0].ChangeType != "deploy" ||
		changes[0].Actor != "alice" || changes[0].Summary != "v1.2.3" || changes[0].Revision != "abc123" {
		t.Fatalf("changes[0] = %+v", changes[0])
	}
}

func TestChangeRepositoryQualifiesDateTimeSortColumn(t *testing.T) {
	var gotQ string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQ = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}\n"))
	}))
	defer srv.Close()
	r := NewChangeRepository(NewClickHouseRepo(srv.URL, nil))
	if _, err := r.List(context.Background(), ChangeScope{TenantID: "t1"}, "", ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotQ, "ORDER BY observability.change_records.start_time DESC") {
		t.Fatalf("change query sort is not qualified: %s", gotQ)
	}
}

func TestChangeRepoSQLOwnershipScope(t *testing.T) {
	var gotQ string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQ = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(""))
	}))
	defer srv.Close()
	r := NewChangeRepository(NewClickHouseRepo(srv.URL, nil))
	r.List(context.Background(), ChangeScope{
		TenantID:  "3f3c3b3a-0000-4000-8000-000000000001",
		ClusterID: "3f3c3b3a-0000-4000-8000-000000000002",
	}, "checkout", "2026-08-20 00:00:00")
	for _, want := range []string{
		"tenant_id='3f3c3b3a-0000-4000-8000-000000000001'",
		"cluster_id='3f3c3b3a-0000-4000-8000-000000000002'",
		"service_name='checkout'",
		"observability.change_records.start_time >= '2026-08-20 00:00:00'",
	} {
		if !strings.Contains(gotQ, want) {
			t.Errorf("repo SQL missing %q; got: %s", want, gotQ)
		}
	}
}

func TestChangeRepoNoData(t *testing.T) {
	r := mockChangesCH(t, map[string]string{"FROM observability.change_records": ""})
	_, err := r.List(context.Background(), ChangeScope{TenantID: "t1"}, "checkout", "")
	if err == nil {
		t.Fatal("expected no_data for empty change_records")
	}
	var qe *QueryError
	if !errors.As(err, &qe) || qe.Code != NoDataCode {
		t.Fatalf("expected no_data, got %v", err)
	}
}
