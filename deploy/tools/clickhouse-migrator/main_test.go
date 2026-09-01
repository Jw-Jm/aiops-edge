package main

import (
	"reflect"
	"testing"
)

func TestSplitStatementsHandlesQuotedSemicolonsAndComments(t *testing.T) {
	sql := `-- comment with ;
CREATE TABLE x (message String DEFAULT 'a; b');
/* block ; comment */
ALTER TABLE x ADD COLUMN "quoted;name" String;
INSERT INTO x VALUES ('it''s; still one');
`
	want := []string{
		"CREATE TABLE x (message String DEFAULT 'a; b')",
		"ALTER TABLE x ADD COLUMN \"quoted;name\" String",
		"INSERT INTO x VALUES ('it''s; still one')",
	}
	if got := splitStatements(sql); !reflect.DeepEqual(got, want) {
		t.Fatalf("splitStatements() = %#v, want %#v", got, want)
	}
}

func TestSplitStatementsPreservesRegexAndMultipleDDL(t *testing.T) {
	sql := `INSERT INTO q SELECT multiIf(NOT match(event_id, '^[0-9a-f]{64}$'), 'invalid', 'ok') FROM events;
ALTER TABLE events DELETE WHERE event_id = '' SETTINGS mutations_sync = 1;
ALTER TABLE events MODIFY COLUMN event_id String;`
	got := splitStatements(sql)
	if len(got) != 3 {
		t.Fatalf("splitStatements() returned %d statements, want 3: %#v", len(got), got)
	}
	for _, stmt := range got {
		if stmt == "" {
			t.Fatal("splitStatements() returned an empty statement")
		}
	}
}

func TestIdentityMigrationTargetState(t *testing.T) {
	tests := []struct {
		id          string
		defaultKind string
		want        bool
	}{
		{identityDefaultMigrationID, "__none__", true},
		{identityDefaultMigrationID, "DEFAULT", false},
		{"0008_k8s_events_identity_cutover", "__none__", false},
	}
	for _, tt := range tests {
		got := tt.id == identityDefaultMigrationID && identityDefaultTargetSatisfied(tt.defaultKind)
		if got != tt.want {
			t.Fatalf("target state id=%q default_kind=%q = %v, want %v", tt.id, tt.defaultKind, got, tt.want)
		}
	}
}

func TestTopologyEngineTargetState(t *testing.T) {
	if !topologyEngineTargetSatisfied("SummingMergeTree\n") {
		t.Fatal("SummingMergeTree must be treated as the satisfied topology target")
	}
	if topologyEngineTargetSatisfied("ReplacingMergeTree") {
		t.Fatal("ReplacingMergeTree must require migration")
	}
}
