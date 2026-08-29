package api

import (
	"context"
	"strings"
	"testing"
	"time"
)

type cleanupQueryerFunc func(context.Context, string) ([]byte, error)

func (f cleanupQueryerFunc) Query(ctx context.Context, sql string) ([]byte, error) {
	return f(ctx, sql)
}

func TestNormalizeDataCleanupRequestCanonicalizesScopeAndCutoff(t *testing.T) {
	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	got, err := normalizeDataCleanupRequest(DataCleanupRequest{
		Scopes:         []string{"clickhouse_telemetry", "alert_events", "clickhouse_telemetry"},
		CutoffAt:       "2026-08-01T08:00:00+08:00",
		TenantID:       "tenant-1",
		ClusterID:      "cluster-1",
		IdempotencyKey: "idem-1",
	}, "tenant-1", now)
	if err != nil {
		t.Fatalf("normalizeDataCleanupRequest() error = %v", err)
	}
	if got.CutoffAt.Format(time.RFC3339) != "2026-08-01T00:00:00Z" {
		t.Fatalf("CutoffAt = %s, want UTC", got.CutoffAt.Format(time.RFC3339))
	}
	if strings.Join(got.Scopes, ",") != "alert_events,clickhouse_telemetry" {
		t.Fatalf("Scopes = %v, want sorted unique scopes", got.Scopes)
	}
	if got.TenantID != "tenant-1" || got.ClusterID != "cluster-1" {
		t.Fatalf("scope = %+v", got)
	}
	if got.RequestDigest == "" || len(got.RequestDigest) != 64 {
		t.Fatalf("RequestDigest = %q, want sha256 hex", got.RequestDigest)
	}
}

func TestNormalizeDataCleanupRequestRejectsUnsafeInputs(t *testing.T) {
	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		req  DataCleanupRequest
		want string
	}{
		{
			name: "empty scopes",
			req:  DataCleanupRequest{CutoffAt: "2026-08-01T00:00:00Z", IdempotencyKey: "idem"},
			want: "scope",
		},
		{
			name: "unknown scope",
			req:  DataCleanupRequest{Scopes: []string{"drop_database"}, CutoffAt: "2026-08-01T00:00:00Z", IdempotencyKey: "idem"},
			want: "scope",
		},
		{
			name: "future cutoff",
			req:  DataCleanupRequest{Scopes: []string{"alert_events"}, CutoffAt: "2026-08-30T00:00:00Z", IdempotencyKey: "idem"},
			want: "cutoff",
		},
		{
			name: "missing timezone",
			req:  DataCleanupRequest{Scopes: []string{"alert_events"}, CutoffAt: "2026-08-01 00:00:00", IdempotencyKey: "idem"},
			want: "cutoff",
		},
		{
			name: "missing idempotency",
			req:  DataCleanupRequest{Scopes: []string{"alert_events"}, CutoffAt: "2026-08-01T00:00:00Z", ClusterID: "cluster-1"},
			want: "idempotency",
		},
		{
			name: "cross tenant",
			req:  DataCleanupRequest{Scopes: []string{"alert_events"}, CutoffAt: "2026-08-01T00:00:00Z", TenantID: "tenant-2", IdempotencyKey: "idem"},
			want: "tenant",
		},
		{
			name: "alerts without cluster",
			req:  DataCleanupRequest{Scopes: []string{"alert_events"}, CutoffAt: "2026-08-01T00:00:00Z", IdempotencyKey: "idem"},
			want: "cluster",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := normalizeDataCleanupRequest(tt.req, "tenant-1", now)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestBuildDataCleanupStatementsUsesOnlyWhitelistedTablesAndColumns(t *testing.T) {
	req := normalizedDataCleanupRequest{
		Scopes:    []string{"alert_events", "clickhouse_telemetry"},
		CutoffAt:  time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		TenantID:  "tenant-1",
		ClusterID: "cluster-1",
	}
	statements := buildDataCleanupStatements(req)
	if len(statements) != 8 {
		t.Fatalf("got %d table statements, want 8", len(statements))
	}
	for _, statement := range statements {
		if !strings.HasPrefix(statement.CountSQL, "SELECT count() FROM observability.") {
			t.Fatalf("unsafe count SQL: %s", statement.CountSQL)
		}
		if !strings.HasPrefix(statement.DeleteSQL, "ALTER TABLE observability.") {
			t.Fatalf("unsafe delete SQL: %s", statement.DeleteSQL)
		}
		if strings.Contains(statement.CountSQL, "DROP") || strings.Contains(statement.DeleteSQL, "DROP") {
			t.Fatalf("destructive DDL leaked into cleanup SQL: %+v", statement)
		}
		if !strings.Contains(statement.CountSQL, "tenant_id='tenant-1'") && statement.Scope != "alert_events" {
			t.Fatalf("telemetry SQL is not tenant-scoped: %s", statement.CountSQL)
		}
		if statement.Scope == "alert_events" && !strings.Contains(statement.CountSQL, "status='resolved'") {
			t.Fatalf("alert SQL is not resolved-only: %s", statement.CountSQL)
		}
		if !strings.Contains(statement.CountSQL, "< '2026-08-01 00:00:00'") && !strings.Contains(statement.CountSQL, "< toDate('2026-08-01')") {
			t.Fatalf("cutoff missing from count SQL: %s", statement.CountSQL)
		}
	}
}

func TestDataCleanupDigestIsStableForEquivalentRequests(t *testing.T) {
	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	a, err := normalizeDataCleanupRequest(DataCleanupRequest{
		Scopes: []string{"alert_events", "clickhouse_telemetry"}, CutoffAt: "2026-08-01T00:00:00Z", TenantID: "tenant-1", ClusterID: "cluster-1", IdempotencyKey: "idem",
	}, "tenant-1", now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := normalizeDataCleanupRequest(DataCleanupRequest{
		Scopes: []string{"clickhouse_telemetry", "alert_events"}, CutoffAt: "2026-08-01T08:00:00+08:00", TenantID: "tenant-1", ClusterID: "cluster-1", IdempotencyKey: "idem",
	}, "tenant-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if a.RequestDigest != b.RequestDigest {
		t.Fatalf("equivalent requests have different digests: %s != %s", a.RequestDigest, b.RequestDigest)
	}
}

func TestCollectDataCleanupPlanCountsEachWhitelistedTable(t *testing.T) {
	req := normalizedDataCleanupRequest{
		Scopes:    []string{"alert_events", "clickhouse_telemetry"},
		CutoffAt:  time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		TenantID:  "tenant-1",
		ClusterID: "cluster-1",
	}
	seen := 0
	items, err := collectDataCleanupPlan(context.Background(), cleanupQueryerFunc(func(_ context.Context, sql string) ([]byte, error) {
		seen++
		if !strings.HasPrefix(sql, "SELECT count() FROM observability.") {
			t.Fatalf("unexpected count SQL: %s", sql)
		}
		return []byte("7\n"), nil
	}), req)
	if err != nil {
		t.Fatalf("collectDataCleanupPlan() error = %v", err)
	}
	if seen != 8 || len(items) != 8 {
		t.Fatalf("queries/items = %d/%d, want 8/8", seen, len(items))
	}
	for _, item := range items {
		if item.EstimatedRows != 7 || item.DeleteSQL == "" {
			t.Fatalf("invalid plan item: %+v", item)
		}
	}
}

func TestCollectDataCleanupPlanRejectsMalformedCount(t *testing.T) {
	req := normalizedDataCleanupRequest{
		Scopes:   []string{"clickhouse_telemetry"},
		CutoffAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		TenantID: "tenant-1",
	}
	_, err := collectDataCleanupPlan(context.Background(), cleanupQueryerFunc(func(_ context.Context, _ string) ([]byte, error) {
		return []byte("not-a-count\n"), nil
	}), req)
	if err == nil || !strings.Contains(err.Error(), "count") {
		t.Fatalf("error = %v, want malformed count error", err)
	}
}
