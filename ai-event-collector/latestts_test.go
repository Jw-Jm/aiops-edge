package main

import (
	"strings"
	"testing"
)

func TestLatestTSQueryIncludesTenant(t *testing.T) {
	q := latestTSQuery("k8s", "3f3c3b3a-0000-4000-8000-000000000001", "3f3c3b3a-0000-4000-8000-000000000002")
	if !strings.Contains(q, "tenant_id = '3f3c3b3a-0000-4000-8000-000000000001'") {
		t.Errorf("query must filter tenant_id, got: %s", q)
	}
	if !strings.Contains(q, "cluster_id = '3f3c3b3a-0000-4000-8000-000000000002'") {
		t.Errorf("query must filter cluster_id, got: %s", q)
	}
	if !strings.Contains(q, "source = 'k8s'") {
		t.Errorf("query must filter source, got: %s", q)
	}
}
