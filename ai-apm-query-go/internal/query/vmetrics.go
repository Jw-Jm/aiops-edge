package query

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
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
	StartTime *time.Time
	EndTime   *time.Time
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
		ResultType string          `json:"resultType"`
		Result     []vmQuerySeries `json:"result"`
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

// ServiceRED 查询某服务最近 N 分钟的 RED 时序列（调用数/错误数/平均耗时）。
// RED writer 写入的是累计 counter；这里按固定的一分钟采样桶取 increase，
// 将 counter 的单位还原为每桶计数，避免把小于 1 的每秒 rate 四舍五入为 0。
func (r *VictoriaMetricsReader) ServiceRED(ctx context.Context, q VMQuery) ([]REDPoint, error) {
	if q.Minutes <= 0 {
		q.Minutes = 60
	}
	now := time.Now()
	start := now.Add(-time.Duration(q.Minutes) * time.Minute).Unix()
	end := now.Unix()
	if q.StartTime != nil && q.EndTime != nil {
		start = q.StartTime.Unix()
		end = q.EndTime.Unix()
	}
	step := int64(60)
	sel := vmLabelSelectors(q)

	// Return all RED components in one bounded query.  Each component is
	// labelled before the set union so the response remains attributable after
	// PromQL aggregation; absent error/duration series remain zero rather than
	// being inferred from latency or a missing field.
	const bucketWindow = "1m"
	expr := strings.Join([]string{
		fmt.Sprintf(`label_replace(sum(increase(call_total%s[%s])), "red_kind", "calls", "", "")`, sel, bucketWindow),
		fmt.Sprintf(`label_replace(sum(increase(error_total%s[%s])), "red_kind", "errors", "", "")`, sel, bucketWindow),
		fmt.Sprintf(`label_replace(sum(increase(duration_seconds_sum%s[%s])), "red_kind", "duration_sum", "", "")`, sel, bucketWindow),
		fmt.Sprintf(`label_replace(sum(increase(duration_seconds_count%s[%s])), "red_kind", "duration_count", "", "")`, sel, bucketWindow),
	}, " or ")
	series, err := r.queryRangeSeries(ctx, expr, start, end, step)
	if err != nil {
		return nil, err
	}
	if len(series) == 0 {
		return nil, NoData()
	}
	type aggregate struct {
		calls, errors, durationCount float64
		durationSum                  float64
	}
	aggregates := make(map[int64]*aggregate)
	for _, item := range series {
		kind := item.Metric["red_kind"]
		if kind == "" {
			// Compatibility with pre-RED envelopes that returned only call_total.
			kind = "calls"
		}
		for _, value := range item.Values {
			if len(value) != 2 {
				continue
			}
			ts := int64(toFloat64(value[0]))
			if ts == 0 {
				continue
			}
			agg := aggregates[ts]
			if agg == nil {
				agg = &aggregate{}
				aggregates[ts] = agg
			}
			n := toFloat64(value[1])
			switch kind {
			case "calls":
				agg.calls += n
			case "errors":
				agg.errors += n
			case "duration_sum":
				agg.durationSum += n
			case "duration_count":
				agg.durationCount += n
			}
		}
	}
	timestamps := make([]int64, 0, len(aggregates))
	for ts := range aggregates {
		timestamps = append(timestamps, ts)
	}
	sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })
	points := make([]REDPoint, 0, len(timestamps))
	for _, ts := range timestamps {
		agg := aggregates[ts]
		avgMS := 0.0
		if agg.calls > 0 && agg.durationSum > 0 {
			avgMS = agg.durationSum / agg.calls * 1000
		}
		points = append(points, REDPoint{T: time.Unix(ts, 0).UTC(), CallCount: int(math.Round(agg.calls)), ErrorCount: int(math.Round(agg.errors)), AvgMS: avgMS})
	}
	if len(points) == 0 {
		return nil, NoData()
	}
	return points, nil
}

// queryRange 调 VM /api/v1/query_range，返回时间序列采样点。
func (r *VictoriaMetricsReader) queryRange(ctx context.Context, expr string, start, end, step int64) ([]REDPoint, error) {
	series, err := r.queryRangeSeries(ctx, expr, start, end, step)
	if err != nil {
		return nil, err
	}
	var out []REDPoint
	for _, item := range series {
		for _, v := range item.Values {
			if len(v) != 2 {
				continue
			}
			out = append(out, REDPoint{T: time.Unix(int64(toFloat64(v[0])), 0).UTC(), CallCount: int(toFloat64(v[1]))})
		}
	}
	return out, nil
}

type vmQuerySeries struct {
	Metric map[string]string `json:"metric"`
	Values [][2]interface{}  `json:"values"`
}

func (r *VictoriaMetricsReader) queryRangeSeries(ctx context.Context, expr string, start, end, step int64) ([]vmQuerySeries, error) {
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
	return vr.Data.Result, nil
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
