package store

import (
	"strings"
	"testing"
)

func TestClusterAuthorityBackfillLeavesUnmappedTenantUnset(t *testing.T) {
	joined := strings.Join(clusterAuthorityBackfillStatements(), "\n")
	if strings.Contains(joined, "tenant_id='default'") || strings.Contains(joined, "tenant_id = 'default'") {
		t.Fatalf("cluster authority backfill assigns a default tenant: %s", joined)
	}
	if strings.Contains(joined, "tenant_id=") || strings.Contains(joined, "tenant_id =") {
		t.Fatalf("cluster authority backfill must leave tenant_id unresolved: %s", joined)
	}
}
