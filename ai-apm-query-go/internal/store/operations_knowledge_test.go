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

// 回归（真实环境验证发现的 S1 缺陷家族）：Create 的 INSERT 列数与值数不匹配，
// 参数整体错位导致 status 写入 source_revision。Submit/Review/Disable 必须保持
// 占位符与参数一一对应，否则生命周期在生产环境静默失效（0 行受影响）。
func TestKnowledgeSubmitUpdatesDraftWithinScope(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	SetDB(db)
	defer SetDB(nil)

	mock.ExpectExec(regexp.QuoteMeta("UPDATE operations_knowledge SET status='pending_review', updated_by=?, updated_at=NOW(3) WHERE tenant_id=? AND (scope_type='platform_common' OR (scope_type='cluster' AND cluster_id=?)) AND knowledge_id=? AND status='draft'")).
		WithArgs("admin", "tenant-a", "cluster-a", "k-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	dao := &OperationsKnowledgeDAO{}
	if err := dao.Submit(context.Background(), KnowledgeScope{TenantID: "tenant-a", ClusterID: "cluster-a"}, "k-1", "admin"); err != nil {
		t.Fatalf("submit must affect the draft row within scope: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestKnowledgeSubmitRejectsWhenScopeDoesNotMatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	SetDB(db)
	defer SetDB(nil)

	// 0 行受影响必须显式失败，不得被解释为成功。
	mock.ExpectExec(regexp.QuoteMeta("UPDATE operations_knowledge SET status='pending_review', updated_by=?, updated_at=NOW(3) WHERE tenant_id=? AND (scope_type='platform_common' OR (scope_type='cluster' AND cluster_id=?)) AND knowledge_id=? AND status='draft'")).
		WithArgs("admin", "tenant-a", "cluster-a", "k-missing").
		WillReturnResult(sqlmock.NewResult(0, 0))

	dao := &OperationsKnowledgeDAO{}
	err = dao.Submit(context.Background(), KnowledgeScope{TenantID: "tenant-a", ClusterID: "cluster-a"}, "k-missing", "admin")
	if err == nil || !strings.Contains(err.Error(), "not an editable draft") {
		t.Fatalf("zero-row submit must fail loudly, got %v", err)
	}
}

func TestKnowledgeDisableUpdatesAndFailsLoudlyOnZeroRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	SetDB(db)
	defer SetDB(nil)

	// 占位符顺序：updated_by=?, tenant_id=?, cluster_id=?, knowledge_id=?
	mock.ExpectExec(regexp.QuoteMeta("UPDATE operations_knowledge SET status='disabled', disabled_at=NOW(3), updated_by=?, updated_at=NOW(3) WHERE tenant_id=? AND (scope_type='platform_common' OR (scope_type='cluster' AND cluster_id=?)) AND knowledge_id=? AND status<>'disabled'")).
		WithArgs("admin", "tenant-a", "cluster-a", "k-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	dao := &OperationsKnowledgeDAO{}
	if err := dao.Disable(context.Background(), KnowledgeScope{TenantID: "tenant-a", ClusterID: "cluster-a"}, "k-1", "admin"); err != nil {
		t.Fatalf("disable must affect the row within scope: %v", err)
	}

	// 0 行受影响必须报错：静默成功会让"删除验证"建立在假成功上。
	mock.ExpectExec(regexp.QuoteMeta("UPDATE operations_knowledge SET status='disabled', disabled_at=NOW(3), updated_by=?, updated_at=NOW(3) WHERE tenant_id=? AND (scope_type='platform_common' OR (scope_type='cluster' AND cluster_id=?)) AND knowledge_id=? AND status<>'disabled'")).
		WithArgs("admin", "tenant-a", "cluster-a", "k-missing").
		WillReturnResult(sqlmock.NewResult(0, 0))
	if err := dao.Disable(context.Background(), KnowledgeScope{TenantID: "tenant-a", ClusterID: "cluster-a"}, "k-missing", "admin"); err == nil {
		t.Fatal("zero-row disable must fail loudly")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
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
