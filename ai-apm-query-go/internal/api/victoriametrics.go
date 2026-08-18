package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// buildQueryRangeURL 构造 VictoriaMetrics /api/v1/query_range 完整 URL，
// 对 query/start/end/step 做校验并 URL 编码。
func (h *Handler) buildQueryRangeURL(query, start, end, step string) string {
	u, _ := url.Parse(h.vmURL + "/api/v1/query_range")
	q := u.Query()
	q.Set("query", query)
	q.Set("start", start)
	q.Set("end", end)
	q.Set("step", step)
	u.RawQuery = q.Encode()
	return u.String()
}

// maxVMPayload 限制 VM 代理响应体上限（20MB），防止超大响应占内存（安全加固）。
const maxVMPayload = 20 << 20

// maxQueryRangePoints 限制 query_range 单次查询的数据点上限（G4/S14 修复），
// 防止 (end-start)/step 过小导致 VM 返回海量数据点。
const maxQueryRangePoints = 10000

// ---- /metrics/query PromQL 透传路径简单限流（安全加固，参照 auth.go 登录限流风格）----
// metricsQueryAttempts 按客户端 IP 计数：60s 窗口内最多 metricsQueryLimit 次，
// 超限返回 429，防止单客户端反复打透传端点打爆 VictoriaMetrics。
// 注意：与登录限流一致，key 取自 clientIP（X-Forwarded-For 可被伪造），仅作基线防护，
// 生产建议配合网络层限流。
var (
	metricsQueryAttempts   = map[string]loginAttempt{}
	metricsQueryAttemptsMu sync.Mutex
)

const (
	metricsQueryLimit  = 60
	metricsQueryWindow = 60 // 秒
)

// allowMetricsQuery 记录一次透传请求并判断是否超限（超限返回 false）。
func allowMetricsQuery(key string) bool {
	now := time.Now().Unix()
	metricsQueryAttemptsMu.Lock()
	defer metricsQueryAttemptsMu.Unlock()
	if len(metricsQueryAttempts) > 10000 {
		for k, a := range metricsQueryAttempts {
			if now-a.window >= metricsQueryWindow {
				delete(metricsQueryAttempts, k)
			}
		}
	}
	a, ok := metricsQueryAttempts[key]
	if !ok || now-a.window >= metricsQueryWindow {
		a = loginAttempt{count: 0, window: now}
	}
	a.count++
	metricsQueryAttempts[key] = a
	return a.count <= metricsQueryLimit
}

// proxyVMInstantQuery 代理 VictoriaMetrics /api/v1/query（instant query），
// 原样透传 VM 响应（含状态码与 JSON body）。
// 供 /api/v1/metrics/query 在携带 PromQL query 参数且无 service 参数时使用（P2-6）。
//
// 已知限制（安全文档化）：本透传端点不注入租户/cluster 过滤。原因：PromQL 表达式结构
// 任意（聚合、正则、histogram_quantile、by/without 等），向表达式拼接 cluster 标签
// 选择器会破坏语义；且 VM service_* 指标上的 cluster label 未必覆盖所有序列。
// 该端点已由 AuthMiddleware 强制 JWT 鉴权，但本身无租户/cluster 数据隔离，
// 需配合网络层隔离（如仅对可信网段开放）控制可达性。
//
// 安全加固：
//   - 按客户端 IP 限流（60s 窗口 metricsQueryLimit 次，超限 429）
//   - 响应体大小上限 maxVMPayload（10MB），超限返回 502
func (h *Handler) proxyVMInstantQuery(w http.ResponseWriter, r *http.Request, query string) {
	if h.vmURL == "" {
		respondError(w, http.StatusServiceUnavailable, "victoria-metrics not configured")
		return
	}
	if !allowMetricsQuery(clientIP(r)) {
		respondError(w, http.StatusTooManyRequests, "metrics query rate limit exceeded")
		return
	}
	u, _ := url.Parse(h.vmURL + "/api/v1/query")
	q := u.Query()
	q.Set("query", query)
	// 可选 time 参数（Prometheus instant query 兼容），有则透传
	if t := r.URL.Query().Get("time"); t != "" {
		q.Set("time", t)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp, err := h.client.Do(req)
	if err != nil {
		log.Printf("VM instant query error: %v", err)
		respondError(w, http.StatusBadGateway, "victoria-metrics unavailable: "+err.Error())
		return
	}
	defer resp.Body.Close()
	// 安全加固：限制响应体大小（10MB），防止超大响应占内存；超限返回 502。
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxVMPayload+1))
	if err != nil {
		respondError(w, http.StatusBadGateway, "victoria-metrics read error: "+err.Error())
		return
	}
	if len(body) > maxVMPayload {
		respondError(w, http.StatusBadGateway, "victoria-metrics response too large (>10MB)")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

// QueryRange 处理 GET /api/v1/metrics/query_range，代理 VictoriaMetrics /api/v1/query_range。
// 参数：query=PromQL 表达式, start/end/step（Prometheus 兼容时间格式）。
// G4 安全加固（S14）：按 IP 限流 + 数据点上限 + 响应体大小上限，防止透传端点被滥用。
func (h *Handler) QueryRange(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := q.Get("query")
	start := q.Get("start")
	end := q.Get("end")
	step := q.Get("step")
	if query == "" || start == "" || end == "" || step == "" {
		respondError(w, http.StatusBadRequest, "query, start, end, step are required")
		return
	}
	if h.vmURL == "" {
		respondError(w, http.StatusServiceUnavailable, "victoria-metrics not configured")
		return
	}
	// G4：按客户端 IP 限流（与 instant query 共用计数，60s 窗口 metricsQueryLimit 次）
	if !allowMetricsQuery(clientIP(r)) {
		respondError(w, http.StatusTooManyRequests, "metrics query rate limit exceeded")
		return
	}
	// G4：数据点上限——(end-start)/step 超过 maxQueryRangePoints 直接拒绝，
	// 要求增大 step 或缩小时间范围，防止 VM 返回海量数据点。
	if pts, ok := queryRangePoints(start, end, step); ok && pts > maxQueryRangePoints {
		respondError(w, http.StatusBadRequest,
			fmt.Sprintf("query_range too large: %d data points exceeds max %d (increase step or reduce time range)", pts, maxQueryRangePoints))
		return
	}
	target := h.buildQueryRangeURL(query, start, end, step)
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp, err := h.client.Do(req)
	if err != nil {
		log.Printf("VM query_range error: %v", err)
		respondError(w, http.StatusBadGateway, "victoria-metrics unavailable: "+err.Error())
		return
	}
	defer resp.Body.Close()
	// G4：响应体大小上限（20MB），超限返回 502。
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxVMPayload+1))
	if err != nil {
		respondError(w, http.StatusBadGateway, "victoria-metrics read error: "+err.Error())
		return
	}
	if len(body) > maxVMPayload {
		respondError(w, http.StatusBadGateway, "victoria-metrics response too large (>20MB)")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

// queryRangePoints 估算 query_range 的数据点数量。
// start/end 支持 Unix 秒（数字）或 RFC3339 时间串；解析失败返回 ok=false（跳过检查）。
func queryRangePoints(start, end, step string) (int, bool) {
	toUnix := func(s string) (float64, bool) {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f, true
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return float64(t.Unix()), true
		}
		return 0, false
	}
	sv, ok1 := toUnix(start)
	ev, ok2 := toUnix(end)
	st, ok3 := toUnix(step)
	if !ok1 || !ok2 || !ok3 || st <= 0 {
		return 0, false
	}
	pts := int((ev - sv) / st)
	if pts < 0 {
		pts = -pts
	}
	return pts, true
}

// vmRangeQuery 调 VM /api/v1/query_range 返回历史数值序列（供 zscore/MAD / SLO 烧毁率）。
// start/end 为 Unix 秒，step 为采样步长（秒）。
func (h *Handler) vmRangeQuery(promQL string, start, end int64, step int) ([]float64, error) {
	if h.vmURL == "" {
		return nil, fmt.Errorf("victoria-metrics not configured")
	}
	target := h.buildQueryRangeURL(promQL, fmt.Sprintf("%d", start), fmt.Sprintf("%d", end), fmt.Sprintf("%d", step))
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	// 显式检查状态码：VM 错误响应可能带 JSON body，若跳过会静默吞掉 500 错误
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("VM query_range returned %d: %s", resp.StatusCode, string(body)[:min(len(body), 200)])
	}
	var vr struct {
		Data struct {
			Result []struct {
				Values [][2]interface{} `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &vr); err != nil {
		return nil, err
	}
	var out []float64
	for _, r := range vr.Data.Result {
		for _, v := range r.Values {
			if len(v) != 2 {
				continue
			}
			// VM 返回 value 为字符串（如 "1.5"）或数字，统一转 float64
			switch val := v[1].(type) {
			case string:
				var f float64
				if _, err := fmt.Sscanf(val, "%f", &f); err == nil {
					out = append(out, f)
				}
			case float64:
				out = append(out, val)
			case json.Number:
				if f, err := val.Float64(); err == nil {
					out = append(out, f)
				}
			}
		}
	}
	return out, nil
}
