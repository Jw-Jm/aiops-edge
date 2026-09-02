package api

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

func chatInternalRequest() *internalQueryRequest {
	return &internalQueryRequest{
		WorkloadKind: "chat", ChatSessionID: "66666666-6666-4666-8666-666666666666",
		ChatTurnID:     "77777777-7777-4777-8777-777777777777",
		ChatToolCallID: "88888888-8888-4888-8888-888888888888",
		ChatToolName:   "query_metrics.v1", PrincipalID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		PrincipalType: "user", SessionID: "55555555-5555-4555-8555-555555555555",
		RouteCapability: "observability.metrics.read", Service: "checkout", Minutes: 5,
	}
}

func TestBeginChatToolRunRequiresDurableIdentity(t *testing.T) {
	h := &Handler{chatToolDAO: &store.AIChatToolRunDAO{}}
	_, _, err := h.beginToolRun(&internalQueryRequest{WorkloadKind: "chat"},
		"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	if err == nil || !strings.Contains(err.Error(), "chat tool") {
		t.Fatalf("beginToolRun() error = %v, want chat identity validation", err)
	}
}

func TestBeginChatToolRunStartsBeforeDataSource(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(previous) })
	req := chatInternalRequest()
	mock.ExpectQuery(`SELECT user_uuid,tenant_id,cluster_id FROM ai_chat_sessions WHERE session_id=\?`).
		WithArgs(req.ChatSessionID).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid", "tenant_id", "cluster_id"}).AddRow(req.PrincipalID, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "cccccccc-cccc-4ccc-8ccc-cccccccccccc"))
	mock.ExpectExec(`INSERT INTO ai_chat_tool_runs`).WillReturnResult(sqlmock.NewResult(0, 1))
	req.ChatToolRunID = "99999999-9999-4999-8999-999999999999"
	h := &Handler{chatToolDAO: &store.AIChatToolRunDAO{}}
	ctx, replay, err := h.beginToolRun(req, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	if err != nil || replay || ctx == nil || ctx.ChatAudit == nil {
		t.Fatalf("beginToolRun() = ctx=%#v replay=%v err=%v, want durable chat audit", ctx, replay, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestEndChatToolRunSuppressesDataWhenAuditFinishFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(previous) })
	mock.ExpectExec(`UPDATE ai_chat_tool_runs SET status =`).WillReturnError(errors.New("audit write failed"))
	h := &Handler{chatToolDAO: &store.AIChatToolRunDAO{}}
	ctx := &toolRunContext{ChatAudit: &store.AIChatToolRun{ChatToolRunID: "99999999-9999-4999-8999-999999999999", PrincipalID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", SessionID: "55555555-5555-4555-8555-555555555555", TenantID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", ClusterID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc"}}
	_, err = h.endToolRun(ctx, "success", []byte(`{"points":[1]}`), "")
	if err == nil {
		t.Fatal("endToolRun() error = nil, want audit persistence failure")
	}
}

func TestRespondChatToolReplayUsesDurableStatus(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     string
		wantStatus int
	}{
		{name: "running", status: "running", wantStatus: 202},
		{name: "complete", status: "complete", wantStatus: 200},
		{name: "failed", status: "failed", wantStatus: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h := &Handler{}
			h.respondToolReplay(rec, &toolRunContext{ChatAudit: &store.AIChatToolRun{
				ChatToolRunID: "99999999-9999-4999-8999-999999999999",
				Status:        tc.status,
			}})
			if rec.Code != tc.wantStatus {
				t.Fatalf("status=%d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestChatToolReplayEnvelopeMarksRunningAsPartialWithoutRawData(t *testing.T) {
	env := toolReplayEnvelope(&toolRunContext{ChatAudit: &store.AIChatToolRun{
		ChatToolRunID: "99999999-9999-4999-8999-999999999999",
		Status:        "running",
		ResultCount:   4,
	}})
	if env.Quality != "partial" || !env.Replay || string(env.Data) != "{}" || env.Count != 4 {
		t.Fatalf("running replay envelope = %+v, want partial/empty/replay", env)
	}
	if len(env.SourceErrors) != 1 || env.SourceErrors[0] != "TOOL_RUNNING" {
		t.Fatalf("running replay errors = %#v, want TOOL_RUNNING", env.SourceErrors)
	}
}

func TestStableChatAuditErrorNeverPersistsProviderDetails(t *testing.T) {
	got := stableChatAuditError("provider=https://10.0.0.7/v1 key=super-secret sql=SELECT password")
	if got != "CHAT_TOOL_FAILED" {
		t.Fatalf("stableChatAuditError() = %q, want stable code", got)
	}
}
