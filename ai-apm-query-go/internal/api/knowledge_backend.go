package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/query"
)

// knowledgeBackendCfg 从 environment 读取 knowledge 检索后端配置。
type knowledgeBackendCfg struct {
	chromaURL  string // Chroma 向量索引地址
	collection string // Chroma collection（knowledge 向量索引）
	client     *http.Client
}

// newKnowledgeBackendFromEnv 构造 knowledge 检索后端。
// Chroma + MinIO 未配置 → 返回 nil（repository 将返回 unavailable，fail-closed，绝不回退 ProxyAI）。
func newKnowledgeBackendFromEnv() query.KnowledgeBackend {
	cfg := knowledgeBackendCfg{
		chromaURL:  os.Getenv("CHROMA_URL"),
		collection: os.Getenv("CHROMA_COLLECTION"),
		client:     &http.Client{Timeout: 15 * 60_000_000_000},
	}
	if cfg.chromaURL == "" {
		return nil // fail-closed：知识检索不可用时不伪造结果
	}
	if cfg.collection == "" {
		cfg.collection = "aiops-knowledge"
	}
	return chromaKnowledgeBackend{cfg: cfg}
}

// chromaKnowledgeBackend 通过 Chroma REST API 查询向量索引（Knowledge Vector Index）。
// Chroma query 返回最相近 document chunks；minioRef/metadata 来自 collection metadata。
type chromaKnowledgeBackend struct {
	cfg knowledgeBackendCfg
}

// chromaQueryResponse 是 Chroma collection query 的响应结构。
type chromaQueryResponse struct {
	Documents [][]string                 `json:"documents"`
	IDs       [][]string                 `json:"ids"`
	Distances [][]float64                `json:"distances"`
	Metadatas [][]map[string]interface{} `json:"metadatas"`
}

// Search 检索 Chroma collection，将命中的 metadata 映射为 query.KnowledgeHit。
func (b chromaKnowledgeBackend) Search(ctx context.Context, text string, topK int) ([]query.KnowledgeHit, error) {
	payload := map[string]interface{}{
		"query_texts": []string{text},
		"n_results":   topK,
		"include":     []string{"documents", "metadatas", "distances"},
	}
	body, _ := json.Marshal(payload)

	u := strings.TrimSuffix(b.cfg.chromaURL, "/") +
		"/api/v1/collections/" + url.PathEscape(b.cfg.collection) + "/query"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.cfg.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errChromaUnavailable
	}

	var cr chromaQueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, err
	}
	return mapChromaHits(&cr), nil
}

var errChromaUnavailable = &query.QueryError{Code: query.UnavailableCode, Message: "knowledge: chroma unavailable", Retryable: true}

// mapChromaHits 将 Chroma 命中映射为结构化 KnowledgeHit（document_id/source/version/similarity/applicability）。
func mapChromaHits(cr *chromaQueryResponse) []query.KnowledgeHit {
	if cr == nil || len(cr.IDs) == 0 {
		return nil
	}
	var out []query.KnowledgeHit
	ids := cr.IDs[0]
	distances := firstFloats(cr.Distances)
	metas := firstMetadatas(cr.Metadatas)
	for i, id := range ids {
		if id == "" {
			continue
		}
		meta := map[string]interface{}{}
		if i < len(metas) && metas[i] != nil {
			meta = metas[i]
		}
		similarity := 0.0
		if i < len(distances) {
			// Chroma 返回 L2 distance；换算为 0-1 similarity（越小越相似）。
			similarity = 1.0 / (1.0 + distances[i])
		}
		out = append(out, query.KnowledgeHit{
			DocumentID:    id,
			Source:        metaString(meta, "source"),
			Version:       metaString(meta, "version"),
			Similarity:    similarity,
			Applicability: metaString(meta, "applicability"),
		})
	}
	return out
}

func firstFloats(rows [][]float64) []float64 {
	if len(rows) == 0 {
		return nil
	}
	return rows[0]
}

func firstMetadatas(rows [][]map[string]interface{}) []map[string]interface{} {
	if len(rows) == 0 {
		return nil
	}
	return rows[0]
}

func metaString(meta map[string]interface{}, key string) string {
	if meta == nil {
		return ""
	}
	switch v := meta[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}
