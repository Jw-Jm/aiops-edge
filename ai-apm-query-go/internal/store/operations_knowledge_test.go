package store

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestKnowledgeScopeKeepsPlatformCommonAndClusterFactsBounded(t *testing.T) {
	scope := KnowledgeScope{TenantID: "tenant-1", ClusterID: "cluster-a"}
	if !scope.Allows(KnowledgePlatformCommon, "") {
		t.Fatal("platform_common should be visible from an authorized tenant cluster")
	}
	if !scope.Allows(KnowledgeCluster, "cluster-a") || scope.Allows(KnowledgeCluster, "cluster-b") {
		t.Fatal("cluster knowledge crossed the active cluster boundary")
	}
	if (KnowledgeScope{}).Allows(KnowledgePlatformCommon, "") {
		t.Fatal("empty tenant must fail closed")
	}
}

func TestKnowledgeInputRejectsInvalidScopeAndMissingFacts(t *testing.T) {
	scope := KnowledgeScope{TenantID: "tenant-1", ClusterID: "cluster-a"}
	valid := KnowledgeInput{ScopeType: KnowledgeCluster, ClusterID: "cluster-a", KnowledgeType: "runbook", Title: "Restart", Content: "steps"}
	if err := validateKnowledgeInput(scope, valid); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	for _, invalid := range []KnowledgeInput{
		{ScopeType: KnowledgePlatformCommon, ClusterID: "cluster-a", Title: "bad", Content: "steps"},
		{ScopeType: KnowledgeCluster, Title: "bad", Content: "steps"},
		{ScopeType: KnowledgeCluster, ClusterID: "cluster-a", Title: "", Content: "steps"},
	} {
		if err := validateKnowledgeInput(scope, invalid); err == nil {
			t.Fatalf("invalid input accepted: %+v", invalid)
		}
	}
}

func TestKnowledgeContentChecksumAndOutboxIdentityAreStable(t *testing.T) {
	if checksumKnowledge("same") != checksumKnowledge("same") || checksumKnowledge("same") == checksumKnowledge("other") {
		t.Fatal("knowledge checksum is not stable and content-addressed")
	}
	id := deterministicKnowledgeOutboxID("knowledge-1", "version-1")
	if len(id) != 36 || strings.Count(id, "-") != 4 || id != deterministicKnowledgeOutboxID("knowledge-1", "version-1") {
		t.Fatalf("outbox id=%q is not deterministic UUID-shaped", id)
	}
}

func TestKnowledgeReviewPublishesVersionAndOutboxAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	SetDB(db)
	defer SetDB(nil)
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT knowledge_id,tenant_id,scope_type,cluster_id,knowledge_type,title,summary,source_kind,source_revision,status,current_version_id,draft_version_id,created_by,updated_by,created_at,updated_at,disabled_at FROM operations_knowledge WHERE tenant_id=? AND (scope_type='platform_common' OR (scope_type='cluster' AND cluster_id=?)) AND knowledge_id=? FOR UPDATE")).WithArgs("tenant-1", "cluster-a", "k-1").WillReturnRows(sqlmock.NewRows([]string{"knowledge_id", "tenant_id", "scope_type", "cluster_id", "knowledge_type", "title", "summary", "source_kind", "source_revision", "status", "current_version_id", "draft_version_id", "created_by", "updated_by", "created_at", "updated_at", "disabled_at"}).AddRow("k-1", "tenant-1", "cluster", "cluster-a", "runbook", "Restart", "", "manual", nil, "pending_review", nil, "v-1", "author", "author", now, now, nil))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO operations_knowledge_reviews (knowledge_id,version_id,reviewer_id,decision,reason) VALUES (?,?,?,?,?)")).WithArgs("k-1", "v-1", "admin", "approve", "evidence complete").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE operations_knowledge SET status='published', current_version_id=?, draft_version_id=NULL, updated_by=?, updated_at=NOW(3) WHERE knowledge_id=?")).WithArgs("v-1", "admin", "k-1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO operations_knowledge_index_outbox (outbox_id,knowledge_id,version_id,event_kind,status) VALUES (?,?,?,'publish','pending')")).WithArgs(sqlmock.AnyArg(), "k-1", "v-1").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := (&OperationsKnowledgeDAO{}).Review(context.Background(), KnowledgeScope{TenantID: "tenant-1", ClusterID: "cluster-a"}, "k-1", "admin", "approve", "evidence complete"); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("review transaction expectations: %v", err)
	}
}
