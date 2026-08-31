package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

func TestGenerateChatReportUsesScopedMySQLTranscript(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := store.GetDB()
	store.SetDB(db)
	defer store.SetDB(prev)

	sid := "11111111-1111-4111-8111-111111111111"
	uid := "91480408-9c2d-11f1-8271-bea176fe9f9f"
	tenant := "7ed01afc-cc79-4ecd-8767-a2befa6168ad"
	cluster := "91771a6e-9c2d-11f1-8271-bea176fe9f9f"
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT session_id,intent,service,created_at,updated_at")).
		WithArgs(sid, uid, tenant, cluster).
		WillReturnRows(sqlmock.NewRows([]string{"session_id", "intent", "service", "created_at", "updated_at"}).
			AddRow(sid, "diagnosis", "orders", now, now))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id,role,kind,content,metadata_json,created_at")).
		WithArgs(sid).
		WillReturnRows(sqlmock.NewRows([]string{"id", "role", "kind", "content", "metadata_json", "created_at"}).
			AddRow(1, "user", "", "latency is high", nil, now).
			AddRow(2, "assistant", "", "root cause is queue saturation", nil, now))

	var body bytes.Buffer
	_ = json.NewEncoder(&body).Encode(map[string]string{"session_id": sid})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/final_report", &body)
	req = withAuthorizationContext(req, AuthorizationContext{UserID: uid, TenantID: tenant, ActiveClusterID: cluster})
	rec := httptest.NewRecorder()
	(&Handler{}).GenerateChatReport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	report, _ := out["report"].(string)
	if !containsAll(report, "latency is high", "root cause is queue saturation", sid) {
		t.Fatalf("report did not contain scoped transcript: %s", report)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func containsAll(value string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			return false
		}
	}
	return true
}
