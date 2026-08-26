package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestK8sWatcherListKeepsContextUntilResponseBodyRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(20 * time.Millisecond)
		_, _ = fmt.Fprint(w, `{"metadata":{"resourceVersion":"42"},"items":[]}`)
	}))
	defer srv.Close()

	kw := &k8sWatcher{client: srv.Client(), baseURL: srv.URL}
	rv, events, err := kw.listFromPath(context.Background(), "/api/v1/events")
	if err != nil {
		t.Fatalf("listFromPath returned error: %v", err)
	}
	if rv != "42" || len(events) != 0 {
		t.Fatalf("listFromPath = rv %q, events %d; want rv 42 and no events", rv, len(events))
	}
}
