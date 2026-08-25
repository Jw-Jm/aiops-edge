package store

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCanonicalBootstrapConfigRejectsLegacyOrIncompleteIdentity(t *testing.T) {
	tests := []struct {
		name string
		cfg  CanonicalBootstrapConfig
	}{
		{
			name: "missing tenant",
			cfg:  CanonicalBootstrapConfig{ClusterID: "91771a6e-9c2d-41f1-8271-bea176fe9f9f", ClusterSlug: "local-cluster"},
		},
		{
			name: "legacy tenant",
			cfg:  CanonicalBootstrapConfig{TenantID: "default", ClusterID: "91771a6e-9c2d-41f1-8271-bea176fe9f9f", ClusterSlug: "local-cluster"},
		},
		{
			name: "legacy cluster",
			cfg:  CanonicalBootstrapConfig{TenantID: "7ed01afc-cc79-4ecd-8767-a2befa6168ad", ClusterID: "default", ClusterSlug: "local-cluster"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.Validate(); !errors.Is(err, ErrInvalidCanonicalBootstrap) {
				t.Fatalf("Validate() error = %v, want ErrInvalidCanonicalBootstrap", err)
			}
		})
	}
}

func TestEnsureCanonicalBootstrapDataWritesTenantMembershipAndClusterOwnership(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := CanonicalBootstrapConfig{
		TenantID:           "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
		TenantName:         "AIOps Tenant",
		ClusterID:          "91771a6e-9c2d-11f1-8271-bea176fe9f9f",
		ClusterSlug:        "local-cluster",
		ClusterName:        "kubernetes-cluster",
		ClusterEnvironment: "local",
		ClusterRegion:      "local",
	}

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO tenants (id, name, quota_ai, enabled)")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT user_uuid FROM users WHERE username = 'admin' LIMIT 1")).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid"}).AddRow("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO user_tenants (user_uuid, tenant_id, status)")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO roles (role_id, name, status)")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO permissions (permission_id, action, status)")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO user_roles (user_uuid, tenant_id, role_id, status)")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO role_permissions (role_id, permission_id)")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO clusters").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO tenant_clusters (tenant_id, cluster_id) VALUES (?, ?)")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	if err := EnsureCanonicalBootstrapData(db, cfg); err != nil {
		t.Fatalf("EnsureCanonicalBootstrapData() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
