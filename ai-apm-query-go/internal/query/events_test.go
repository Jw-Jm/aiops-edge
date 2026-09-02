package query

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestKubernetesEventRepositoryScopesAndParses(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tenant_id":"tenant-1","cluster_id":"11111111-1111-4111-8111-111111111111","timestamp":"2026-09-02 00:01:00.000000000","namespace":"observability","kind":"Event","name":"warning-1","reason":"BackOff","type":"Warning","message":"marker","involved_object":"Pod/query-api","source_component":"kubelet","source":"k8s","node":"node-a","event_id":"event-1"}` + "\n"))
	}))
	defer srv.Close()

	start := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	repo := NewKubernetesEventRepository(NewClickHouseRepo(srv.URL, nil))
	events, err := repo.List(context.Background(), "tenant-1", "11111111-1111-4111-8111-111111111111",
		[]string{"query-api"}, &start, &end, 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 1 || events[0].InvolvedObject != "Pod/query-api" || events[0].Type != "Warning" {
		t.Fatalf("events = %+v", events)
	}
	for _, want := range []string{
		"tenant_id='tenant-1'",
		"cluster_id='11111111-1111-4111-8111-111111111111'",
		"ts >=",
		"ts <",
		"type IN ('Warning','Error')",
		"involved_object LIKE '%/query-api%'",
		"FROM observability.k8s_events FINAL",
	} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query missing %q: %s", want, gotQuery)
		}
	}
}

func TestKubernetesEventRepositoryRequiresFrozenScope(t *testing.T) {
	repo := NewKubernetesEventRepository(NewClickHouseRepo("http://127.0.0.1:1", nil))
	if _, err := repo.List(context.Background(), "tenant-1", "cluster-1", nil, nil, nil, 20, 0); err == nil {
		t.Fatal("expected missing frozen window to fail closed")
	}
}
