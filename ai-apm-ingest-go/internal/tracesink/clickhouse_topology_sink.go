package tracesink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/observability-platform/ai-apm-ingest-go/internal/model"
)

const topologyInsertTemplate = `INSERT INTO observability.service_topology FORMAT JSONEachRow`

// ClickHouseTopologyEdgeSink persists the derived service dependency projection
// owned by unified ingest. The raw Trace SoT remains trace_spans; this sink only
// writes bounded minute buckets derived from accepted spans.
type ClickHouseTopologyEdgeSink struct {
	httpURL  string
	user     string
	password string
	client   *http.Client

	mu      sync.Mutex
	health  bool
	lastErr error
}

func NewClickHouseTopologyEdgeSink(httpURL string, timeout time.Duration) *ClickHouseTopologyEdgeSink {
	return NewClickHouseTopologyEdgeSinkAuth(httpURL, "", "", timeout)
}

func NewClickHouseTopologyEdgeSinkAuth(httpURL, user, password string, timeout time.Duration) *ClickHouseTopologyEdgeSink {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &ClickHouseTopologyEdgeSink{
		httpURL: httpURL, user: user, password: password,
		client: &http.Client{Timeout: timeout},
	}
}

// Probe marks the derived projection sink ready before the first trace arrives.
func (s *ClickHouseTopologyEdgeSink) Probe() error {
	req, err := http.NewRequest(http.MethodGet, s.httpURL+"/?query="+urlQueryEncode("SELECT 1"), nil)
	if err != nil {
		return s.recordError(err)
	}
	s.setAuth(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return s.recordError(fmt.Errorf("topology sink: ch probe: %w", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return s.recordError(fmt.Errorf("topology sink: ch probe status %d", resp.StatusCode))
	}
	s.mu.Lock()
	s.health, s.lastErr = true, nil
	s.mu.Unlock()
	return nil
}

// AddEdge implements pipeline.EdgeSink for compatibility with lightweight callers.
func (s *ClickHouseTopologyEdgeSink) AddEdge(edge *model.TopologyEdge) {
	_ = s.AddEdges([]*model.TopologyEdge{edge})
}

// AddEdges writes one flush as a single JSONEachRow request. A batch is
// acknowledged only after ClickHouse accepts the request; failures are retained
// for the Ingest readiness endpoint and returned to the caller.
func (s *ClickHouseTopologyEdgeSink) AddEdges(edges []*model.TopologyEdge) error {
	if len(edges) == 0 {
		return nil
	}
	var body bytes.Buffer
	for _, edge := range edges {
		if edge == nil {
			return s.recordError(fmt.Errorf("topology sink: nil edge"))
		}
		row := topologyEdgeRow{}.from(edge)
		encoded, err := json.Marshal(row)
		if err != nil {
			return s.recordError(err)
		}
		body.Write(encoded)
		body.WriteByte('\n')
	}
	if err := s.postRaw(body.Bytes()); err != nil {
		return s.recordError(err)
	}
	s.mu.Lock()
	s.health, s.lastErr = true, nil
	s.mu.Unlock()
	return nil
}

func (s *ClickHouseTopologyEdgeSink) Healthy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.health
}

func (s *ClickHouseTopologyEdgeSink) LastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

func (s *ClickHouseTopologyEdgeSink) recordError(err error) error {
	s.mu.Lock()
	s.health, s.lastErr = false, err
	s.mu.Unlock()
	return err
}

func (s *ClickHouseTopologyEdgeSink) setAuth(req *http.Request) {
	if s.user != "" {
		req.SetBasicAuth(s.user, s.password)
	}
}

func (s *ClickHouseTopologyEdgeSink) postRaw(body []byte) error {
	target := s.httpURL + "/?query=" + urlQueryEncode(topologyInsertTemplate)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain")
	s.setAuth(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("topology sink: ch post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("topology sink: ch insert status %d", resp.StatusCode)
	}
	return nil
}

type topologyEdgeRow struct {
	TenantID      string `json:"tenant_id"`
	ClusterID     string `json:"cluster_id"`
	SourceService string `json:"source_service"`
	TargetService string `json:"target_service"`
	TimeBucket    string `json:"time_bucket"`
	CallCount     uint64 `json:"call_count"`
	ErrorCount    uint64 `json:"error_count"`
	AvgDurationNs uint64 `json:"avg_duration_ns"`
	Date          string `json:"date"`
}

func (topologyEdgeRow) from(edge *model.TopologyEdge) topologyEdgeRow {
	return topologyEdgeRow{
		TenantID: edge.TenantID, ClusterID: edge.ClusterID,
		SourceService: edge.SourceService, TargetService: edge.TargetService,
		TimeBucket: edge.TimeBucket.UTC().Format("2006-01-02 15:04:05"),
		CallCount:  edge.CallCount, ErrorCount: edge.ErrorCount, AvgDurationNs: edge.AvgDurationNs,
		Date: edge.Date,
	}
}
