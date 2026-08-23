package store

import (
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestAIRunEventAppend(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	mock.ExpectBegin()
	// 1) 幂等：先查 sequence（event_id 不存在 → no rows）
	mock.ExpectQuery(regexp.QuoteMeta("SELECT sequence FROM ai_run_events")).
		WillReturnRows(sqlmock.NewRows([]string{"sequence"}))
	// 2) 锁 owner 递增
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_runs SET last_event_sequence = last_event_sequence + 1")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT last_event_sequence FROM ai_runs")).
		WillReturnRows(sqlmock.NewRows([]string{"last_event_sequence"}).AddRow(int64(1)))
	// 3) 插入
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_run_events")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	d := &AIRunEventDAO{}
	ev, created, err := d.Append(AIRunEvent{EventID: "e1", RunID: "r1", EventType: "status", Payload: []byte(`{}`)})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if !created || ev.Sequence != 1 {
		t.Fatalf("expected created seq=1, got created=%v seq=%d", created, ev.Sequence)
	}
}

func TestAIRunEventAppendIdempotentOnDuplicate(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	// P1-2：先查 event_id → 已存在（sequence=2），返回既有，**不递增 sequence**（无 gap）。
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT sequence FROM ai_run_events")).
		WillReturnRows(sqlmock.NewRows([]string{"sequence"}).AddRow(int64(2)))
	mock.ExpectCommit()

	d := &AIRunEventDAO{}
	ev, created, err := d.Append(AIRunEvent{EventID: "e1", RunID: "r1", EventType: "status"})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if created {
		t.Fatalf("expected existing (!created) on duplicate event_id")
	}
	if ev.EventID != "e1" || ev.Sequence != 2 {
		t.Fatalf("expected existing seq=2, got %+v", ev)
	}
}

func TestAIRunEventReplayAfter(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	rows := sqlmock.NewRows([]string{"run_id", "sequence", "event_id", "event_type", "payload_json", "created_at"}).
		AddRow("r1", int64(2), "e2", "status", []byte(`{}`), time.Now())
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, sequence, event_id")).
		WillReturnRows(rows)
	d := &AIRunEventDAO{}
	evs, err := d.ReplayAfter("r1", 1)
	if err != nil {
		t.Fatalf("ReplayAfter: %v", err)
	}
	if len(evs) != 1 || evs[0].Sequence != 2 || evs[0].EventID != "e2" {
		t.Fatalf("got %+v", evs)
	}
}

func TestAIRunEventLastSequence(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT last_event_sequence FROM ai_runs")).
		WillReturnRows(sqlmock.NewRows([]string{"last_event_sequence"}).AddRow(int64(5)))
	d := &AIRunEventDAO{}
	seq, err := d.LastSequence("r1")
	if err != nil || seq != 5 {
		t.Fatalf("seq=%d err=%v", seq, err)
	}
}
