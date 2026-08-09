package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
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

// QueryRange 处理 GET /api/v1/metrics/query_range，代理 VictoriaMetrics /api/v1/query_range。
// 参数：query=PromQL 表达式, start/end/step（Prometheus 兼容时间格式）。
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
	body, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
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
