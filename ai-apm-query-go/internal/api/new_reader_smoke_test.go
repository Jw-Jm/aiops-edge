package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	trustedauth "github.com/observability-platform/ai-apm-query-go/internal/auth"
	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/query"
)

// ─────────────────────────────────────────────────────────────────────────────
// P6.3.4 Isolated New-Reader Mode Smoke
//
// 不切生产 writer：以 QUERY_READER_MODE=new 启动隔离 query layer，对真实后端形态
// （VictoriaMetrics / VictoriaLogs / ClickHouse / Chroma / Kubernetes）做 8 域 query smoke，
// 证明 new reader 已 ready，只差进入原子窗口。
// ─────────────────────────────────────────────────────────────────────────────

// newReaderSmokeHandler 构造 QUERY_READER_MODE=new 的隔离 handler（全域 mock 后端）。
func newReaderSmokeHandler(t *testing.T) (*Handler, ed25519.PrivateKey) {
	t.Helper()

	// strict internal verifier
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	pub := priv.Public().(ed25519.PublicKey)
	restore := configureInternalRequestVerifier(trustedauth.VerifyConfig{
		Issuer:       "ai-orchestrator",
		Audience:     "ai-apm-query-go",
		PublicKeys:   map[string]ed25519.PublicKey{trustedauth.KeyID(pub): pub},
		ServiceToken: "test-service-token",
		ReplayCache:  trustedauth.NewReplayCache(100),
		ClockSkew:    30 * time.Second,
	})
	t.Cleanup(restore)

	// ClickHouse mock（topology/traces/alerts/changes）
	chSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("query")
		switch {
		case strings.Contains(q, "change_records"):
			_, _ = w.Write([]byte(`{"change_id":"ch-1","service_name":"checkout","change_type":"deploy","start_time":"2026-08-20 10:00:00","actor":"alice","summary":"v1","revision":"a"}` + "\n"))
		case strings.Contains(q, "service_topology"):
			_, _ = w.Write([]byte(`{"src":"a","dst":"b","src_ns":"x","dst_ns":"y","calls":5}` + "\n"))
		default:
			_, _ = w.Write([]byte(""))
		}
	}))
	t.Cleanup(chSrv.Close)

	// VictoriaMetrics mock（new mode Raw Metrics）
	vmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/query_range" {
			_, _ = w.Write([]byte(`{"status":"success","data":{"result":[{"metric":{"service_name":"checkout"},"values":[["1710000000","10"]]}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[]}}`))
	}))
	t.Cleanup(vmSrv.Close)

	// VictoriaLogs mock（new mode Raw Logs）
	vlogSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"_time":"2026-08-20T10:00:00Z","service_name":"checkout","level":"info","_msg":"hello"}` + "\n"))
	}))
	t.Cleanup(vlogSrv.Close)

	host, port := splitHostPort(chSrv.URL)
	h := &Handler{client: &http.Client{}}
	h.repo = *query.NewClickHouseRepo(fmt.Sprintf("http://%s:%d", host, port), &http.Client{Timeout: 5 * time.Second})

	// new mode wiring（与 NewHandler 一致）
	h.metricsRepo = query.NewMetricsRepository(&h.repo)
	h.metricsRepo.WithVMRouter(query.NewVictoriaMetricsReader(vmSrv.URL, &http.Client{Timeout: 5 * time.Second}), query.ModeNew)
	h.logRepo = query.NewLogRepository(&h.repo, query.NewVLogsReader(vlogSrv.URL, &http.Client{Timeout: 5 * time.Second}), query.NewSourceRouter(query.ModeNew))
	h.traceRepo = query.NewTraceRepository(&h.repo)
	h.alertRepo = query.NewAlertRepository(&h.repo)
	h.topoRepo = query.NewTopologyRepository(&h.repo)
	h.resourceRepo = query.NewResourceRepository(&h.repo)
	h.changeRepo = query.NewChangeRepository(&h.repo)
	h.knowledgeRepo = query.NewKnowledgeRepository(&fakeKnowledgeBackend{hits: []query.KnowledgeHit{
		{DocumentID: "doc-1", Source: "runbook", Version: "v3", Similarity: 0.9, Applicability: "checkout"},
	}})
	h.kubeRepo = query.NewKubernetesRepository(&smokeKubeAccessor{nodes: []string{"node-a"}})
	return h, priv
}

// smokeKubeAccessor 实现 query.KubernetesAccessor（api 包内局部 fake）。
type smokeKubeAccessor struct {
	nodes []string
}

func (s *smokeKubeAccessor) Client(ctx context.Context, clusterID string) (query.KubeClient, error) {
	return &smokeKubeClient{nodes: s.nodes}, nil
}

type smokeKubeClient struct {
	nodes []string
}

func (c *smokeKubeClient) ClusterID() string             { return "" }
func (c *smokeKubeClient) ListNodeNames() ([]string, error) { return c.nodes, nil }
func (c *smokeKubeClient) ListNodeDetails() ([]map[string]interface{}, error) { return nil, nil }
func (c *smokeKubeClient) ListPods(ns string) ([]query.KubePod, error) { return nil, nil }


// internalClusterRequest 构造指向指定内部 route 的签名请求（默认 metrics.read capability）。
func internalClusterRequest(t *testing.T, priv ed25519.PrivateKey, path, body string, capability string) *http.Request {
	t.Helper()
	now := time.Now().UTC()
	ctx := contract.NewTrustedRequestContext(
		"ai-orchestrator", "ai-apm-query-go", "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", "user", authzUserID, authzSessionID,
		authzTenantID, "ffffffff-ffff-4fff-8fff-ffffffffffff", "cluster", testClusterID,
		capability, "planner", now, now.Add(30*time.Second), "33333333-3333-4333-8333-333333333333",
	)
	token, err := trustedauth.SignTrustedRequestContextV2(ctx, priv)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", "test-service-token")
	req.Header.Set("X-Trusted-Request-Context", token)
	return req
}

// TestNewReaderSmokeAllDomains 以 QUERY_READER_MODE=new 对 8 域做 query smoke。
func TestNewReaderSmokeAllDomains(t *testing.T) {
	h, priv := newReaderSmokeHandler(t)

	cases := []struct {
		path       string
		body       string
		capability string
		handler    func(w http.ResponseWriter, r *http.Request)
	}{
		{"/internal/v1/query/metrics", `{"service":"checkout","minutes":60}`, "observability.metrics.read", h.InternalQueryMetrics},
		{"/internal/v1/query/logs", `{"service":"checkout","minutes":60}`, "observability.logs.read", h.InternalQueryLogs},
		{"/internal/v1/query/traces", `{"service":"checkout","hours":1}`, "observability.traces.read", h.InternalQueryTraces},
		{"/internal/v1/query/alerts", `{"service":"checkout"}`, "observability.alerts.read", h.InternalQueryAlerts},
		{"/internal/v1/query/topology", `{"minutes":60}`, "observability.topology.read", h.InternalQueryTopology},
		{"/internal/v1/query/kubernetes", `{}`, "kubernetes.resources.read", h.InternalQueryKubernetes},
		{"/internal/v1/query/changes", `{"service":"checkout"}`, "changes.read", h.InternalQueryChanges},
		{"/internal/v1/query/knowledge", `{"query":"checkout"}`, "knowledge.search", h.InternalQueryKnowledge},
	}
	for _, tc := range cases {
		req := internalClusterRequest(t, priv, tc.path, tc.body, tc.capability)
		rec := httptest.NewRecorder()
		tc.handler(rec, req)
		// new reader ready：所有域返回 200 或明确失败语义，不允许 500/panic。
		if rec.Code == http.StatusInternalServerError {
			t.Fatalf("%s: unexpected 500 (reader not ready): %s", tc.path, rec.Body.String())
		}
	}
}


