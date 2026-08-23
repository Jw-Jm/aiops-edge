package biz

import (
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

const (
	resolverTenantID  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	resolverClusterID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

func TestResourceResolverResolveCanonicalizesUUIDAndSlug(t *testing.T) {
	for _, ref := range []string{resolverClusterID, "production"} {
		t.Run(ref, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			previous := store.GetDB()
			store.SetDB(db)
			t.Cleanup(func() { store.SetDB(previous) })

			mock.ExpectQuery("SELECT id, cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status").
				WithArgs(resolverTenantID, ref).
				WillReturnRows(sqlmock.NewRows([]string{"id", "cluster_id", "tenant_id", "slug", "name", "environment", "region", "credential_ref", "lifecycle_status", "created_at", "updated_at"}).
					AddRow(int64(1), resolverClusterID, resolverTenantID, "production", "orders", "prod", "cn", "secret://orders", "ready", nil, nil))

			got, err := (ResourceResolver{}).Resolve(ResourceQuery{
				TenantID: resolverTenantID, ClusterRef: ref, ResourceType: "deployment", Namespace: "production", Name: "orders",
			})
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got.TenantID != resolverTenantID || got.ClusterID != resolverClusterID || got.ResourceType != "deployment" || got.Name != "orders" || got.Namespace == nil || *got.Namespace != "production" {
				t.Fatalf("Resolve() = %+v, want canonical provenance", got)
			}
			// V9.2 §10: canonical resource_id does NOT include tenant_id.
			if got.ResourceID != "deployment:"+resolverClusterID+":production:orders" {
				t.Fatalf("Resolve() resource id = %q", got.ResourceID)
			}
			if strings.Contains(got.ResourceID, resolverTenantID) {
				t.Fatalf("resource_id must NOT include tenant: %q", got.ResourceID)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestResourceResolverResolveKeepsSameNamedResourcesSeparateByCluster(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(previous) })

	secondClusterID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	for _, cluster := range []string{resolverClusterID, secondClusterID} {
		mock.ExpectQuery("SELECT id, cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status").
			WithArgs(resolverTenantID, cluster).
			WillReturnRows(sqlmock.NewRows([]string{"id", "cluster_id", "tenant_id", "slug", "name", "environment", "region", "credential_ref", "lifecycle_status", "created_at", "updated_at"}).
				AddRow(int64(1), cluster, resolverTenantID, "production", "orders", "prod", "cn", "secret://orders", "ready", nil, nil))
	}

	resolver := ResourceResolver{}
	first, err := resolver.Resolve(ResourceQuery{TenantID: resolverTenantID, ClusterRef: resolverClusterID, ResourceType: "deployment", Namespace: "production", Name: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := resolver.Resolve(ResourceQuery{TenantID: resolverTenantID, ClusterRef: secondClusterID, ResourceType: "deployment", Namespace: "production", Name: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	if first.ResourceID == second.ResourceID {
		t.Fatalf("same-named resources in different clusters collapsed to %q", first.ResourceID)
	}
}

func TestResourceResolverResolveRejectsImplicitClusterTargets(t *testing.T) {
	for _, ref := range []string{"", "all", "1", "default"} {
		t.Run(ref, func(t *testing.T) {
			_, err := (ResourceResolver{}).Resolve(ResourceQuery{TenantID: resolverTenantID, ClusterRef: ref, ResourceType: "deployment", Namespace: "production", Name: "orders"})
			if !errors.Is(err, ErrInvalidContext) {
				t.Fatalf("Resolve(%q) error = %v, want invalid context", ref, err)
			}
		})
	}
}
