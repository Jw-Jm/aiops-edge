package api

import (
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
