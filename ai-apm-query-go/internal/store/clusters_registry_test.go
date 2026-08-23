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

// clusterRegistryColumnsWithIdentity adds the P3.10c-final authoritative
// Kubernetes identity column used by GetByClusterID / FindActiveByKubeSystemUID.
var clusterRegistryColumnsWithIdentity = []string{
	"id", "cluster_id", "tenant_id", "slug", "name", "environment", "region", "credential_ref", "lifecycle_status", "kubernetes_identity_uid", "created_at", "updated_at",
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

func TestClusterDAORegisterClusterRecordsCanonicalFieldsAndTenantOwnership(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(prev) })

	// Identity check: no ACTIVE cluster already owns this Kubernetes identity.
	mock.ExpectQuery("SELECT id, cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status, kubernetes_identity_uid").
		WithArgs("uid-kube-system-sg").
		WillReturnRows(sqlmock.NewRows(clusterRegistryColumnsWithIdentity))

	mock.ExpectExec("INSERT INTO clusters").
		WithArgs(testClusterID, testTenantID, "prod-sg-01", "Singapore", "prod", "cn-singapore", "k8s-secret://aiops/cluster-a", "active", "kubernetes", "logs,trace", `{"env":"prod"}`, "uid-kube-system-sg").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT IGNORE INTO tenant_clusters").
		WithArgs(testTenantID, testClusterID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	cluster := &Cluster{
		ClusterID:             testClusterID,
		TenantID:              testTenantID,
		Slug:                  "prod-sg-01",
		Name:                  "Singapore",
		Environment:           "prod",
		Region:                "cn-singapore",
		CredentialRef:         "k8s-secret://aiops/cluster-a",
		Status:                "active",
		Type:                  "kubernetes",
		Capabilities:          "logs,trace",
		Labels:                `{"env":"prod"}`,
		KubernetesIdentityUID: "uid-kube-system-sg",
	}
	if err := (&ClusterDAO{}).RegisterCluster(cluster); err != nil {
		t.Fatalf("RegisterCluster() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestClusterDAORegisterClusterRejectsDuplicateActiveIdentity(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(prev) })

	// An existing ACTIVE cluster already owns the same Kubernetes identity.
	mock.ExpectQuery("SELECT id, cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status, kubernetes_identity_uid").
		WithArgs("uid-kube-system-sg").
		WillReturnRows(sqlmock.NewRows(clusterRegistryColumnsWithIdentity).AddRow(
			int64(1), testClusterID, testTenantID, "prod-sg-01", "Singapore", "prod", "cn-singapore", "k8s-secret://aiops/cluster-a", "ready", "uid-kube-system-sg", nil, nil,
		))

	cluster := &Cluster{
		ClusterID:             "33333333-3333-4333-8333-333333333333",
		TenantID:              testTenantID,
		Slug:                  "prod-sg-02",
		Name:                  "Singapore-2",
		Environment:           "prod",
		Region:                "cn-singapore",
		CredentialRef:         "k8s-secret://aiops/cluster-2",
		Status:                "active",
		KubernetesIdentityUID: "uid-kube-system-sg",
	}
	if err := (&ClusterDAO{}).RegisterCluster(cluster); !errors.Is(err, ErrClusterIdentityDuplicate) {
		t.Fatalf("RegisterCluster(duplicate identity) error = %v, want ErrClusterIdentityDuplicate", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestClusterDAORegisterClusterRejectsNonUUIDOrMissingTenant(t *testing.T) {
	prev := GetDB()
	SetDB(nil)
	t.Cleanup(func() { SetDB(prev) })

	if err := (&ClusterDAO{}).RegisterCluster(&Cluster{ClusterID: "prod-sg-01", TenantID: testTenantID, Slug: "x"}); !errors.Is(err, ErrInvalidClusterRef) {
		t.Fatalf("RegisterCluster(slug id) error = %v, want ErrInvalidClusterRef", err)
	}
	if err := (&ClusterDAO{}).RegisterCluster(&Cluster{ClusterID: testClusterID, TenantID: "", Slug: "x"}); !errors.Is(err, ErrInvalidClusterRef) {
		t.Fatalf("RegisterCluster(empty tenant) error = %v, want ErrInvalidClusterRef", err)
	}
}

func TestClusterDAOGetByClusterIDReturnsBoundKubernetesIdentity(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(prev) })

	mock.ExpectQuery("SELECT id, cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status, kubernetes_identity_uid").
		WithArgs(testClusterID).
		WillReturnRows(sqlmock.NewRows(clusterRegistryColumnsWithIdentity).AddRow(
			int64(42), testClusterID, testTenantID, "orbstack", "Orbstack", "prod", "cn-shanghai", "k8s-secret://aiops/cluster-a", "ready", "uid-kube-system-orbstack", nil, nil,
		))

	got, err := (&ClusterDAO{}).GetByClusterID(testClusterID)
	if err != nil {
		t.Fatalf("GetByClusterID() error = %v", err)
	}
	if got.KubernetesIdentityUID != "uid-kube-system-orbstack" {
		t.Fatalf("GetByClusterID() identity = %q, want uid-kube-system-orbstack", got.KubernetesIdentityUID)
	}
	if got.CredentialRef != "k8s-secret://aiops/cluster-a" {
		t.Fatalf("GetByClusterID() credential_ref = %q", got.CredentialRef)
	}
	if got.Kubeconfig != "" {
		t.Fatalf("GetByClusterID() must not expose kubeconfig")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestClusterDAOGetByClusterIDRejectsNonCanonicalUUID(t *testing.T) {
	prev := GetDB()
	SetDB(nil)
	t.Cleanup(func() { SetDB(prev) })
	if _, err := (&ClusterDAO{}).GetByClusterID("orbstack"); !errors.Is(err, ErrInvalidClusterRef) {
		t.Fatalf("GetByClusterID(non-uuid) error = %v, want ErrInvalidClusterRef", err)
	}
}

func TestClusterDAOFindActiveByKubeSystemUIDReturnsNilWhenNoActiveOwner(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(prev) })

	mock.ExpectQuery("SELECT id, cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status, kubernetes_identity_uid").
		WithArgs("uid-unclaimed").
		WillReturnRows(sqlmock.NewRows(clusterRegistryColumnsWithIdentity))

	got, err := (&ClusterDAO{}).FindActiveByKubeSystemUID("uid-unclaimed")
	if err != nil {
		t.Fatalf("FindActiveByKubeSystemUID() error = %v, want nil", err)
	}
	if got != nil {
		t.Fatalf("FindActiveByKubeSystemUID() = %+v, want nil", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestClusterDAOFindActiveByKubeSystemUIDReturnsExistingOwner(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(prev) })

	mock.ExpectQuery("SELECT id, cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status, kubernetes_identity_uid").
		WithArgs("uid-kube-system-orbstack").
		WillReturnRows(sqlmock.NewRows(clusterRegistryColumnsWithIdentity).AddRow(
			int64(42), testClusterID, testTenantID, "orbstack", "Orbstack", "prod", "cn-shanghai", "k8s-secret://aiops/cluster-a", "ready", "uid-kube-system-orbstack", nil, nil,
		))

	got, err := (&ClusterDAO{}).FindActiveByKubeSystemUID("uid-kube-system-orbstack")
	if err != nil {
		t.Fatalf("FindActiveByKubeSystemUID() error = %v", err)
	}
	if got == nil || got.ClusterID != testClusterID {
		t.Fatalf("FindActiveByKubeSystemUID() = %+v, want cluster_id=%s", got, testClusterID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestClusterDAOEnsureTenantClusterRejectsNonUUID(t *testing.T) {
	prev := GetDB()
	SetDB(nil)
	t.Cleanup(func() { SetDB(prev) })
	if err := (&ClusterDAO{}).EnsureTenantCluster(testTenantID, "prod-sg-01"); !errors.Is(err, ErrInvalidClusterRef) {
		t.Fatalf("EnsureTenantCluster(slug) error = %v, want ErrInvalidClusterRef", err)
	}
}

func TestClusterDAOResolveRefRejectsInactiveLifecycleStates(t *testing.T) {
	for _, status := range []string{"registered", "disabled", "degraded", "deleted"} {
		t.Run(status, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			prev := GetDB()
			SetDB(db)
			t.Cleanup(func() { SetDB(prev) })
			mock.ExpectQuery("SELECT id, cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status").
				WithArgs(testTenantID, "production").
				WillReturnRows(sqlmock.NewRows(clusterRegistryColumns).AddRow(
					int64(42), testClusterID, testTenantID, "production", "orders", "prod", "cn-shanghai", "secret://clusters/orders", status, nil, nil,
				))

			if _, err := (&ClusterDAO{}).ResolveRef(testTenantID, "production"); !errors.Is(err, ErrClusterNotFound) {
				t.Fatalf("ResolveRef() error = %v, want ErrClusterNotFound for lifecycle %q", err, status)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
