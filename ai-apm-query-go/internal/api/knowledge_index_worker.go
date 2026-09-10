package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/query"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

type KnowledgeIndexMetadata struct {
	TenantID        string `json:"tenant_id"`
	ScopeType       string `json:"scope_type"`
	ClusterID       string `json:"cluster_id,omitempty"`
	KnowledgeID     string `json:"knowledge_id"`
	VersionID       string `json:"version_id"`
	Type            string `json:"type"`
	Status          string `json:"status"`
	SourceRevision  string `json:"source_revision,omitempty"`
	ContentChecksum string `json:"content_checksum"`
	IsCurrent       bool   `json:"is_current"`
}

type KnowledgeIndexer interface {
	Upsert(ctx context.Context, metadata KnowledgeIndexMetadata, content string) error
}

type KnowledgeIndexWorker struct {
	DAO     *store.OperationsKnowledgeDAO
	Indexer KnowledgeIndexer
}

func BuildKnowledgeIndexMetadata(item store.OperationsKnowledge, version store.OperationsKnowledgeVersion) KnowledgeIndexMetadata {
	return KnowledgeIndexMetadata{TenantID: item.TenantID, ScopeType: string(item.ScopeType), ClusterID: item.ClusterID, KnowledgeID: item.KnowledgeID, VersionID: version.VersionID, Type: item.KnowledgeType, Status: string(item.Status), SourceRevision: version.SourceRevision, ContentChecksum: version.ContentSHA256, IsCurrent: item.Status == store.KnowledgePublished && item.CurrentVersionID == version.VersionID}
}

func (w *KnowledgeIndexWorker) Process(ctx context.Context, outbox store.KnowledgeIndexOutbox, item store.OperationsKnowledge, version store.OperationsKnowledgeVersion) error {
	if w == nil || w.DAO == nil || w.Indexer == nil {
		return errors.New("knowledge index worker is not configured")
	}
	metadata := BuildKnowledgeIndexMetadata(item, version)
	if metadata.Status != string(store.KnowledgePublished) || !metadata.IsCurrent {
		return w.DAO.MarkIndexSucceeded(ctx, outbox.OutboxID, outbox.KnowledgeID, outbox.VersionID)
	}
	if err := w.Indexer.Upsert(ctx, metadata, version.Content); err != nil {
		safe := sanitizeKnowledgeIndexError(err)
		return w.DAO.MarkIndexFailed(ctx, outbox.OutboxID, outbox.KnowledgeID, outbox.VersionID, safe, time.Now().UTC().Add(time.Minute))
	}
	return w.DAO.MarkIndexSucceeded(ctx, outbox.OutboxID, outbox.KnowledgeID, outbox.VersionID)
}

func sanitizeKnowledgeIndexError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	for _, token := range []string{"http://", "https://", "token=", "password=", "secret="} {
		if index := strings.Index(strings.ToLower(message), token); index >= 0 {
			message = strings.TrimSpace(message[:index])
		}
	}
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}

type mysqlKnowledgeAuthority struct{ dao *store.OperationsKnowledgeDAO }

func (a mysqlKnowledgeAuthority) IsCurrentPublished(ctx context.Context, scope query.KnowledgeScope, knowledgeID, versionID string) (bool, error) {
	if a.dao == nil {
		a.dao = &store.OperationsKnowledgeDAO{}
	}
	return a.dao.IsCurrentPublished(ctx, store.KnowledgeScope{TenantID: scope.TenantID, ClusterID: scope.ClusterID}, knowledgeID, versionID)
}
