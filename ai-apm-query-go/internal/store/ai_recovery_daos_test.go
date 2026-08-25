package store

import (
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestAIPlanStepCreateAndList(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_plan_steps")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	rows := sqlmock.NewRows([]string{"step_id", "run_id", "parent_step_id", "seq", "step_type",
		"status", "cluster_id", "description", "budget_used", "depends_on", "parameters",
		"attempt", "outcome", "result_ref", "started_at", "completed_at", "created_at", "updated_at"}).
		AddRow("s1", "run-1", nil, 1, "tool", "success", nil, "desc", 1,
			[]byte(`["s0"]`), []byte(`{"k":"v"}`), 1, "success", "ev-1", time.Now(), time.Now(),
			time.Now(), time.Now())
	mock.ExpectQuery(regexp.QuoteMeta("SELECT step_id, run_id, parent_step_id")).
		WillReturnRows(rows)

	d := &AIPlanStepDAO{}
	created, err := d.Create(AIPlanStep{StepID: "s1", RunID: "run-1", Seq: 1, StepType: "tool",
		DependsOn: []string{"s0"}})
	if err != nil || !created {
		t.Fatalf("Create: created=%v err=%v", created, err)
	}
	steps, err := d.ListByRun("run-1")
	if err != nil {
		t.Fatalf("ListByRun: %v", err)
	}
	if len(steps) != 1 || steps[0].StepID != "s1" || len(steps[0].DependsOn) != 1 {
		t.Fatalf("got %+v", steps)
	}
}

func TestAIToolRunCreateIdempotent(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_tool_runs")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	d := &AIToolRunDAO{}
	created, err := d.Create(AIToolRun{ToolRunID: "t1", RunID: "run-1", TenantID: "t",
		ClusterID: "c", ToolName: "metrics", IdempotencyKey: "k1"})
	if err != nil || !created {
		t.Fatalf("Create: created=%v err=%v", created, err)
	}
}

func TestAIActionCreateAndList(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_actions")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	rows := sqlmock.NewRows([]string{"action_id", "run_id", "tenant_id", "cluster_id",
		"action_type", "action_hash", "idempotency_key", "proposed_risk",
		"authoritative_risk", "status", "dry_run", "target_name", "target_uid",
		"resource_version", "namespace", "operation", "execution_status",
		"params_json", "result_json", "executed_at", "error_code",
		"created_at", "updated_at"}).
		AddRow("a1", "run-1", "t", "c", "restart", "abc123", "k1", "R2", "R3", "approved",
			1, "target-name", "uid-1", "rv-1", "ns", "restart", "proposed",
			[]byte(`{}`), []byte(`{}`), nil, "", time.Now(), time.Now())
	mock.ExpectQuery(regexp.QuoteMeta("SELECT action_id, run_id")).
		WillReturnRows(rows)

	d := &AIActionDAO{}
	created, err := d.Create(AIAction{ActionID: "a1", RunID: "run-1", TenantID: "t",
		ClusterID: "c", ActionType: "restart", ActionHash: "abc123", IdempotencyKey: "k1",
		ProposedRisk: "R2", AuthoritativeRisk: "R3", Status: "approved", DryRun: true})
	if err != nil || !created {
		t.Fatalf("Create: created=%v err=%v", created, err)
	}
	actions, err := d.ListByRun("run-1")
	if err != nil {
		t.Fatalf("ListByRun: %v", err)
	}
	if len(actions) != 1 || actions[0].ActionID != "a1" || !actions[0].DryRun {
		t.Fatalf("got %+v", actions)
	}
}

func TestAIControlCommandCreateAndGet(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_control_commands")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	rows := sqlmock.NewRows([]string{"command_id", "run_id", "operation", "payload_json",
		"payload_hash", "response_json", "status", "idempotency_key", "completed_at", "created_at"}).
		AddRow("cmd-1", "run-1", "cancel", []byte(`{}`), nil, nil, "pending", "k1", nil, time.Now())
	mock.ExpectQuery(regexp.QuoteMeta("SELECT command_id, run_id, operation")).
		WillReturnRows(rows)

	d := &AIControlCommandDAO{}
	created, err := d.Create(AIControlCommand{CommandID: "cmd-1", RunID: "run-1",
		Operation: "cancel", IdempotencyKey: "k1"})
	if err != nil || !created {
		t.Fatalf("Create: created=%v err=%v", created, err)
	}
	cmd, err := d.Get("cmd-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if cmd.CommandID != "cmd-1" || cmd.Operation != "cancel" {
		t.Fatalf("got %+v", cmd)
	}
}
