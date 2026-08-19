package store

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

const (
	testTenantID  = "tenant-a"
	testClusterID = "11111111-1111-4111-8111-111111111111"
)

var clusterRegistryColumns = []string{
	"id", "cluster_id", "tenant_id", "slug", "name", "environment", "region", "credential_ref", "lifecycle_status", "created_at", "updated_at",
}

func TestClusterDAOResolveRefReturnsCanonicalClusterForUUIDAndSlug(t *testing.T) {
	for _, ref := range []string{testClusterID, "production"} {
		t.Run(ref, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			prev := GetDB()
			SetDB(db)
			t.Cleanup(func() { SetDB(prev) })

			mock.ExpectQuery("SELECT id, cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status").
				WithArgs(testTenantID, ref).
				WillReturnRows(sqlmock.NewRows(clusterRegistryColumns).AddRow(
					int64(42), testClusterID, testTenantID, "production", "orders", "prod", "cn-shanghai", "secret://clusters/orders", "ready", nil, nil,
				))

			got, err := (&ClusterDAO{}).ResolveRef(testTenantID, ref)
			if err != nil {
				t.Fatalf("ResolveRef() error = %v", err)
			}
			if got.ClusterID != testClusterID || got.Slug != "production" || got.TenantID != testTenantID {
				t.Fatalf("ResolveRef() = %+v, want canonical cluster metadata", got)
			}
			if got.Kubeconfig != "" {
				t.Fatalf("ResolveRef() exposed kubeconfig: %+v", got)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestClusterDAOResolveRefKeepsSameNameClustersDistinct(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(prev) })

	for _, tc := range []struct {
		ref string
		id  string
	}{
		{"orders-east", "11111111-1111-4111-8111-111111111111"},
		{"orders-west", "22222222-2222-4222-8222-222222222222"},
	} {
		mock.ExpectQuery("SELECT id, cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status").
			WithArgs(testTenantID, tc.ref).
			WillReturnRows(sqlmock.NewRows(clusterRegistryColumns).AddRow(
				int64(1), tc.id, testTenantID, tc.ref, "orders", "prod", "cn-shanghai", "secret://clusters/"+tc.ref, "ready", nil, nil,
			))
		got, err := (&ClusterDAO{}).ResolveRef(testTenantID, tc.ref)
		if err != nil {
			t.Fatalf("ResolveRef(%q) error = %v", tc.ref, err)
		}
		if got.ClusterID != tc.id || got.Name != "orders" {
			t.Fatalf("ResolveRef(%q) = %+v", tc.ref, got)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestClusterDAOResolveRefRejectsUnknownAndAmbiguousReferences(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(prev) })

	mock.ExpectQuery("SELECT id, cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status").
		WithArgs(testTenantID, "missing").
		WillReturnRows(sqlmock.NewRows(clusterRegistryColumns))
	if _, err := (&ClusterDAO{}).ResolveRef(testTenantID, "missing"); !errors.Is(err, ErrClusterNotFound) {
		t.Fatalf("ResolveRef(missing) error = %v, want ErrClusterNotFound", err)
	}

	mock.ExpectQuery("SELECT id, cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status").
		WithArgs(testTenantID, "ambiguous").
		WillReturnRows(sqlmock.NewRows(clusterRegistryColumns).
			AddRow(int64(1), testClusterID, testTenantID, "ambiguous", "orders", "prod", "cn", "secret://one", "ready", nil, nil).
			AddRow(int64(2), "22222222-2222-4222-8222-222222222222", testTenantID, "ambiguous", "orders", "prod", "cn", "secret://two", "ready", nil, nil))
	if _, err := (&ClusterDAO{}).ResolveRef(testTenantID, "ambiguous"); !errors.Is(err, ErrClusterAmbiguous) {
		t.Fatalf("ResolveRef(ambiguous) error = %v, want ErrClusterAmbiguous", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestClusterDAOResolveRefRejectsLegacyIntegerAndAllReferences(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(prev) })

	for _, ref := range []string{"1", "all", ""} {
		if _, err := (&ClusterDAO{}).ResolveRef(testTenantID, ref); !errors.Is(err, ErrInvalidClusterRef) {
			t.Errorf("ResolveRef(%q) error = %v, want ErrInvalidClusterRef", ref, err)
		}
	}
}
