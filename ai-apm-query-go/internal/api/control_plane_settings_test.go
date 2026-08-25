package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestControlPlaneRecoveryPolicyGet(t *testing.T) {
	c := newCPHandler(t)
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT value FROM platform_settings WHERE config_key=?")).
		WithArgs("recovery_policy").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"allow":["kubectl scale"],"deny":[]}`))

	req := c.cpReq(t, http.MethodGet, "/internal/v1/control-plane/settings/recovery-policy",
		"control_plane.settings.read", "", nil)
	rec := httptest.NewRecorder()
	c.h.InternalControlPlaneRecoveryPolicy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["policy"] == nil {
		t.Fatalf("expected policy in response: %v", body)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations not met: %v", err)
	}
}

func TestControlPlaneRecoveryPolicyPut(t *testing.T) {
	c := newCPHandler(t)
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO platform_settings (config_key, value) VALUES (?, ?) ON DUPLICATE KEY UPDATE value=VALUES(value)")).
		WithArgs("recovery_policy", `{"allow":["kubectl scale"],"deny":["rm -rf"]}`).
		WillReturnResult(sqlmock.NewResult(1, 1))

	req := c.cpReq(t, http.MethodPut, "/internal/v1/control-plane/settings/recovery-policy",
		"control_plane.settings.write",
		`{"policy":{"allow":["kubectl scale"],"deny":["rm -rf"]}}`, nil)
	rec := httptest.NewRecorder()
	c.h.InternalControlPlaneRecoveryPolicy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations not met: %v", err)
	}
}

func TestControlPlaneRecoveryPolicyRejectsWrongCapability(t *testing.T) {
	c := newCPHandler(t)
	req := c.cpReq(t, http.MethodGet, "/internal/v1/control-plane/settings/recovery-policy",
		"control_plane.settings.write", "", nil)
	rec := httptest.NewRecorder()
	c.h.InternalControlPlaneRecoveryPolicy(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("expected capability rejection, got %d", rec.Code)
	}
}
