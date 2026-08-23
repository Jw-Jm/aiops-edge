package query

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// VMQuery 是 VictoriaMetrics（Raw Metrics 新 SoT）的规范化查询请求。
// tenant/cluster/resource 由可信 scope 注入，PromQL 内强制这些 label 过滤。
type VMQuery struct {
	TenantID  string
	ClusterID string
	Service   string
	Resource  string
	Minutes   int
}

// VictoriaMetricsReader 是 Raw Metrics 新 SoT（VictoriaMetrics）的 reader（P6.3）。
// 通过 VM /api/v1/query_range 查询 RED 指标；tenant/cluster/resource 标签由可信 scope 注入。
// 失败语义：unavailable / timeout / no_data；**禁止 fallback ClickHouse**。
type VictoriaMetricsReader struct {
	endpoint string
	client   *http.Client
}

// NewVictoriaMetricsReader 构造 VictoriaMetrics reader。
func NewVictoriaMetricsReader(endpoint string, client *http.Client) *VictoriaMetricsReader {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &VictoriaMetricsReader{endpoint: strings.TrimSuffix(endpoint, "/"), client: client}
}

// vmQueryRangeResp 是 VM /api/v1/query_range 响应。
type vmQueryRangeResp struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values [][2]interface{} `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

// vmLabelSelectors 构造 tenant/cluster/resource/service 的 PromQL label 过滤。
// tenant 必须注入（可信 scope）；cluster/resource/service 非空时追加。
func vmLabelSelectors(q VMQuery) string {
	var parts []string
	parts = append(parts, fmt.Sprintf(`tenant_id="%s"`, q.TenantID))
	if q.ClusterID != "" {
		parts = append(parts, fmt.Sprintf(`cluster_id="%s"`, q.ClusterID))
	}
	if q.Resource != "" {
		parts = append(parts, fmt.Sprintf(`resource_id="%s"`, q.Resource))
	}
	if q.Service != "" {
		parts = append(parts, fmt.Sprintf(`service_name="%s"`, q.Service))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// ServiceRED 查询某服务最近 N 分钟的 RED 时序列（call_rate/error_rate/duration）。
// 通过 VM query_range，取 call_total / error_total / duration_sum 的 rate。
func (r *VictoriaMetricsReader) ServiceRED(ctx context.Context, q VMQuery) ([]REDPoint, error) {
	if q.Minutes <= 0 {
		q.Minutes = 60
	}
	now := time.Now()
	start := now.Add(-time.Duration(q.Minutes) * time.Minute).Unix()
	end := now.Unix()
	step := int64(60)
	sel := vmLabelSelectors(q)

	// 一次取调用量与错误量（误差联合），VM 支持多表达式仅需 query_range 一次 per series。
	// 用 call_total 的 rate 作为主序列；错误量/耗时从同 series 派生需要多个查询。
	// 此处实现调用量序列（call_rate）；错误率/耗时可经同 reader 扩展（P6.3 scope 最小）。
	expr := fmt.Sprintf(`sum(rate(call_total%s[5m]))`, sel)
	pts, err := r.queryRange(ctx, expr, start, end, step)
	if err != nil {
		return nil, err
	}
	if len(pts) == 0 {
		return nil, NoData()
	}
	return pts, nil
}

// queryRange 调 VM /api/v1/query_range，返回时间序列采样点。
func (r *VictoriaMetricsReader) queryRange(ctx context.Context, expr string, start, end, step int64) ([]REDPoint, error) {
	u := r.endpoint + "/api/v1/query_range"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	q.Set("query", expr)
	q.Set("start", strconv.FormatInt(start, 10))
	q.Set("end", strconv.FormatInt(end, 10))
	q.Set("step", strconv.FormatInt(step, 10))
	req.URL.RawQuery = q.Encode()

	resp, err := r.client.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, Timeout("victoriametrics: " + err.Error())
		}
		return nil, Unavailable("victoriametrics: " + err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, Unavailable(fmt.Sprintf("victoriametrics: status %d", resp.StatusCode))
	}
	var vr vmQueryRangeResp
	if err := json.NewDecoder(resp.Body).Decode(&vr); err != nil {
		return nil, Unavailable("victoriametrics: decode: " + err.Error())
	}
	if vr.Status != "success" {
		return nil, Unavailable("victoriametrics: status " + vr.Status)
	}
	var out []REDPoint
	for _, series := range vr.Data.Result {
		for _, v := range series.Values {
			ts := toInt64(v[0])
			val := toFloat64(v[1])
			out = append(out, REDPoint{
				T:         time.Unix(ts, 0).UTC(),
				CallCount: int(val),
			})
		}
	}
	return out, nil
}

func toInt64(v interface{}) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	case json.Number:
		n, _ := t.Int64()
		return n
	}
	return 0
}

func toFloat64(v interface{}) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	case json.Number:
		f, _ := t.Float64()
		return f
	}
	return 0
}


