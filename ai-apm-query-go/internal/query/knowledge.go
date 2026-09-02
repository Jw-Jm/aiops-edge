package query

import (
	"context"
	"strings"
)

// KnowledgeScope 是 knowledge 资源域的作用域（租户/集群）。
type KnowledgeScope struct {
	TenantID  string
	ClusterID string
}

// KnowledgeHit 一条知识检索命中（knowledge.search 事实来源）。
// 保留 document_id/source/version/similarity/applicability（契约 42.6）。
type KnowledgeHit struct {
	DocumentID    string
	Source        string // runbook / sop / incident / rca / architecture / product
	Version       string
	Similarity    float64 // 0-1
	Applicability string  // 适用对象（服务/集群/资源）
}

// KnowledgeBackend 是知识检索后端（Chroma vector index + MinIO Knowledge Object）。
// SQL/向量检索 ownership 在 backend 实现；repository 负责作用域与错误语义。
type KnowledgeBackend interface {
	Search(ctx context.Context, scope KnowledgeScope, query string, topK int) ([]KnowledgeHit, error)
}

// KnowledgeRepository 是 knowledge 资源域的 domain repository（V9.2 Phase 6 mandatory gap）。
// 事实来源 = Chroma vector index + MinIO Knowledge Object；Knowledge empty = no_data。
// 禁止 ProxyAI 成为知识检索 fallback。
type KnowledgeRepository struct {
	backend KnowledgeBackend
}

// NewKnowledgeRepository 构造 knowledge repository，注入检索 backend。
func NewKnowledgeRepository(backend KnowledgeBackend) *KnowledgeRepository {
	return &KnowledgeRepository{backend: backend}
}

// Search 检索知识库，返回最相关的 topK 条。空结果语义 = no_data。
func (r *KnowledgeRepository) Search(ctx context.Context, scope KnowledgeScope, query string, topK int) ([]KnowledgeHit, error) {
	if strings.TrimSpace(scope.TenantID) == "" || strings.TrimSpace(scope.ClusterID) == "" {
		return nil, PermissionDenied("knowledge search requires tenant and cluster scope")
	}
	if topK <= 0 {
		topK = 5 // 契约 42.6 默认 top_k=5
	}
	if topK > 50 {
		topK = 50
	}
	if r.backend == nil {
		return nil, Unavailable("knowledge: backend not configured")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, ValidationFailed("knowledge query required")
	}
	hits, err := r.backend.Search(ctx, scope, query, topK)
	if err != nil {
		return nil, Unavailable("knowledge: " + err.Error())
	}
	if len(hits) == 0 {
		return nil, NoData()
	}
	return hits, nil
}
