package query

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAlertRepoListEvents(t *testing.T) {
	// JSONEachRow 响应
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("default_format") != "JSONEachRow" {
			t.Errorf("expected default_format=JSONEachRow")
		}
		w.Header().Set("Content-Type", "application/json")
		body := `{"id":"e1","rule_name":"high-cpu","service":"checkout","severity":"warning","last_timestamp":"2026-08-20 10:00:00"}
{"id":"e2","rule_name":"high-lat","service":"payments","severity":"critical","last_timestamp":"2026-08-20 10:01:00"}
`
		w.Write([]byte(body))
	}))
	defer srv.Close()

	repo := &AlertRepository{ch: NewClickHouseRepo(srv.URL, nil)}
	events, err := repo.ListEvents(context.Background(), "", 20, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].RuleName != "high-cpu" || events[0].Service != "checkout" || events[0].Severity != "warning" {
		t.Fatalf("event0 = %+v", events[0])
	}
}

func TestAlertRepoListEventsSQLOwnership(t *testing.T) {
	var gotQ string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQ = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(""))
	}))
	defer srv.Close()

	repo := &AlertRepository{ch: NewClickHouseRepo(srv.URL, nil)}
	repo.ListEvents(context.Background(), "checkout", 10, 0)
	for _, want := range []string{
		"FROM observability.alert_events FINAL",
		"service = 'checkout'",
		"ORDER BY last_timestamp DESC",
	} {
		if !strings.Contains(gotQ, want) {
			t.Errorf("repo SQL missing %q; got: %s", want, gotQ)
		}
	}
}
