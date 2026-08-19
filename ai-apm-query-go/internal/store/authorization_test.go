package store

import (
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

const (
	testUserID    = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	testSessionID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

func TestAuthorizationDAOAuthorizeAllowsCurrentMySQLState(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(prev) })

	expectIdentity(mock, 1, "active")
	expectTenantMembership(mock, true)
	expectCluster(mock, testClusterID)
	mock.ExpectQuery("SELECT 1 FROM user_roles").WithArgs(testUserID, testTenantID, "kubernetes.read").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectQuery("SELECT 1 FROM user_roles ur JOIN role_permissions rp").
		WithArgs(testUserID, testTenantID, testClusterID, "production", "deployment", "orders", "kubernetes.read", "kubernetes.read").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

	decision, err := (&AuthorizationDAO{}).Authorize(AuthorizationQuery{
		UserID: testUserID, SessionID: testSessionID, TenantRef: testTenantID, ClusterRef: "production",
		Namespace: "production", ResourceType: "deployment", ResourceName: "orders", Action: "kubernetes.read",
	})
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if !decision.Allowed || decision.UserID != testUserID || decision.TenantID != testTenantID || decision.ClusterID != testClusterID || decision.Action != "kubernetes.read" || decision.DenialCode != "" {
		t.Fatalf("Authorize() = %+v, want allowed canonical decision", decision)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizationDAOAuthorizeFailsClosedForDisabledIdentityAndTenantMismatch(t *testing.T) {
	for _, tc := range []struct {
		name          string
		userStatus    int
		sessionStatus string
		member        bool
		want          string
	}{
		{"disabled user", 0, "active", true, DenialUserDisabled},
		{"disabled session", 1, "disabled", true, DenialSessionDisabled},
		{"requested tenant is not a membership", 1, "active", false, DenialTenantMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			prev := GetDB()
			SetDB(db)
			t.Cleanup(func() { SetDB(prev) })
			expectIdentity(mock, tc.userStatus, tc.sessionStatus)
			if tc.userStatus == 1 && tc.sessionStatus == "active" {
				expectTenantMembership(mock, tc.member)
			}

			decision, err := (&AuthorizationDAO{}).Authorize(testAuthorizationQuery())
			if err != nil {
				t.Fatalf("Authorize() error = %v", err)
			}
			if decision.Allowed || decision.DenialCode != tc.want {
				t.Fatalf("Authorize() = %+v, want denial %q", decision, tc.want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAuthorizationDAOAuthorizeDeniesClusterActionAndScopeMismatches(t *testing.T) {
	for _, tc := range []struct {
		name       string
		permission bool
		scope      bool
		cluster    bool
		want       string
	}{
		{"cluster mismatch", false, false, false, DenialClusterMismatch},
		{"action denied", false, false, true, DenialActionDenied},
		{"namespace resource scope denied", true, false, true, DenialScopeDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			prev := GetDB()
			SetDB(db)
			t.Cleanup(func() { SetDB(prev) })
			expectIdentity(mock, 1, "active")
			expectTenantMembership(mock, true)
			if tc.cluster {
				expectCluster(mock, testClusterID)
			} else {
				mock.ExpectQuery("SELECT id, cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status").
					WithArgs(testTenantID, "production").WillReturnRows(sqlmock.NewRows(clusterRegistryColumns))
			}
			if tc.cluster {
				rows := sqlmock.NewRows([]string{"1"})
				if tc.permission {
					rows.AddRow(1)
				}
				mock.ExpectQuery("SELECT 1 FROM user_roles").WithArgs(testUserID, testTenantID, "kubernetes.read").WillReturnRows(rows)
				if tc.permission {
					scopeRows := sqlmock.NewRows([]string{"1"})
					if tc.scope {
						scopeRows.AddRow(1)
					}
					mock.ExpectQuery("SELECT 1 FROM user_roles ur JOIN role_permissions rp").
						WithArgs(testUserID, testTenantID, testClusterID, "production", "deployment", "orders", "kubernetes.read", "kubernetes.read").
						WillReturnRows(scopeRows)
				}
			}

			decision, err := (&AuthorizationDAO{}).Authorize(testAuthorizationQuery())
			if err != nil {
				t.Fatalf("Authorize() error = %v", err)
			}
			if decision.Allowed || decision.DenialCode != tc.want {
				t.Fatalf("Authorize() = %+v, want denial %q", decision, tc.want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAuthorizationDAOAuthorizeRejectsExpiredAndRevokedSessions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		expires time.Time
		revoked interface{}
	}{
		{"expired", time.Now().Add(-time.Hour), nil},
		{"revoked", time.Now().Add(time.Hour), time.Now().Add(-time.Minute)},
		{"missing expiry", time.Time{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			prev := GetDB()
			SetDB(db)
			t.Cleanup(func() { SetDB(prev) })
			expectIdentityWithSessionTimes(mock, 1, "active", tc.expires, tc.revoked)

			decision, err := (&AuthorizationDAO{}).Authorize(testAuthorizationQuery())
			if err != nil {
				t.Fatalf("Authorize() error = %v", err)
			}
			if decision.Allowed || decision.DenialCode != DenialSessionDisabled {
				t.Fatalf("Authorize() = %+v, want expired/revoked session denial", decision)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAuthorizationDAOAuthorizeDoesNotCombinePermissionAndScopeFromDifferentRoles(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(prev) })
	expectIdentity(mock, 1, "active")
	expectTenantMembership(mock, true)
	expectCluster(mock, testClusterID)
	mock.ExpectQuery("SELECT 1 FROM user_roles").WithArgs(testUserID, testTenantID, "kubernetes.read").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	// Role A can perform the action and role B has the target scope. Neither role
	// grants both, so the role-bound query must return no authorization row.
	mock.ExpectQuery("SELECT 1 FROM user_roles ur JOIN role_permissions rp").
		WithArgs(testUserID, testTenantID, testClusterID, "production", "deployment", "orders", "kubernetes.read", "kubernetes.read").
		WillReturnRows(sqlmock.NewRows([]string{"1"}))

	decision, err := (&AuthorizationDAO{}).Authorize(testAuthorizationQuery())
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if decision.Allowed || decision.DenialCode != DenialScopeDenied {
		t.Fatalf("Authorize() = %+v, want role-bound scope denial", decision)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizationSchemaStatementsCreateCurrentAuthorityRecords(t *testing.T) {
	joined := strings.Join(authorizationSchemaStatements(), "\n")
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS user_sessions",
		"CREATE TABLE IF NOT EXISTS user_tenants",
		"CREATE TABLE IF NOT EXISTS roles",
		"CREATE TABLE IF NOT EXISTS permissions",
		"CREATE TABLE IF NOT EXISTS user_roles",
		"CREATE TABLE IF NOT EXISTS role_permissions",
		"CREATE TABLE IF NOT EXISTS scope_assignments",
	} {
		if !strings.Contains(joined, required) {
			t.Errorf("authorization schema does not create %q", required)
		}
	}
}

func testAuthorizationQuery() AuthorizationQuery {
	return AuthorizationQuery{
		UserID: testUserID, SessionID: testSessionID, TenantRef: testTenantID, ClusterRef: "production",
		Namespace: "production", ResourceType: "deployment", ResourceName: "orders", Action: "kubernetes.read",
	}
}

func expectIdentity(mock sqlmock.Sqlmock, userStatus int, sessionStatus string) {
	expectIdentityWithSessionTimes(mock, userStatus, sessionStatus, time.Now().Add(time.Hour), nil)
}

func expectIdentityWithSessionTimes(mock sqlmock.Sqlmock, userStatus int, sessionStatus string, expires time.Time, revoked interface{}) {
	mock.ExpectQuery("SELECT u.user_uuid, u.status, s.status, s.expires_at, s.revoked_at FROM users u JOIN user_sessions s").
		WithArgs(testUserID, testSessionID).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid", "user_status", "session_status", "expires_at", "revoked_at"}).AddRow(testUserID, userStatus, sessionStatus, expires, revoked))
}

func expectTenantMembership(mock sqlmock.Sqlmock, member bool) {
	rows := sqlmock.NewRows([]string{"id"})
	if member {
		rows.AddRow(testTenantID)
	}
	mock.ExpectQuery("SELECT t.id FROM tenants t JOIN user_tenants ut").WithArgs(testUserID, testTenantID).WillReturnRows(rows)
}

func expectCluster(mock sqlmock.Sqlmock, clusterID string) {
	mock.ExpectQuery("SELECT id, cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status").
		WithArgs(testTenantID, "production").
		WillReturnRows(sqlmock.NewRows(clusterRegistryColumns).AddRow(
			int64(42), clusterID, testTenantID, "production", "orders", "prod", "cn-shanghai", "secret://clusters/orders", "ready", nil, nil,
		))
}
