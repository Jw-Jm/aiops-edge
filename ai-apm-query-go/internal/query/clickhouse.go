package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// ClickHouseRepo 是 ClickHouse 事实查询的统一 repository。所有 handler 的
// ClickHouse 查询应经此执行，以获得统一错误语义（no_data / unavailable / timeout），
// 消除"每处各写各的 respondError(500)"。
type ClickHouseRepo struct {
	baseURL  string
	client   *http.Client
	user     string
	password string
}

// NewClickHouseRepo 构造 ClickHouse repository。hc 为 nil 时使用默认 20s 超时 client。
func NewClickHouseRepo(baseURL string, hc *http.Client) *ClickHouseRepo {
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	return &ClickHouseRepo{baseURL: strings.TrimRight(baseURL, "/"), client: hc}
}

// WithCHAuth 设置 ClickHouse Basic Auth（用户/密码，经 Secret 注入）。
// 未设置时保持无凭据（本地/dev 向后兼容）；设置后 Query/QueryJSON 附带 Basic Auth。
func (r *ClickHouseRepo) WithCHAuth(user, password string) *ClickHouseRepo {
	r.user = user
	r.password = password
	return r
}

// applyAuth 为 ClickHouse 请求附加 Basic Auth（若配置了用户/密码）。
func (r *ClickHouseRepo) applyAuth(req *http.Request) {
	if r.user != "" && r.password != "" {
		req.SetBasicAuth(r.user, r.password)
	}
}

// Query 执行 ClickHouse 查询并返回 TabSeparated 原始 body。
//
// 错误语义（统一，非 generic 500）：
//   - HTTP 200 但空 body → NoData
//   - 非 200 / 连接失败 / 传输错误 → Unavailable（503，可重试）
//   - ctx 超时 / deadline → Timeout（504，可重试）
func (r *ClickHouseRepo) Query(ctx context.Context, sql string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/", strings.NewReader(sql))
	if err != nil {
		return nil, Unavailable("clickhouse: build request: " + err.Error())
	}
	req.Header.Set("Content-Type", "text/plain")
	r.applyAuth(req) // ClickHouse Basic Auth（若配置）

	resp, err := r.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, Timeout("clickhouse query: " + ctx.Err().Error())
		}
		return nil, Unavailable("clickhouse: " + err.Error())
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, Unavailable(fmt.Sprintf("clickhouse: status %d: %s", resp.StatusCode, strings.TrimSpace(string(b))))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return nil, Timeout("clickhouse read: " + ctx.Err().Error())
		}
		return nil, Unavailable("clickhouse read: " + err.Error())
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, NoData()
	}
	return body, nil
}

// Exec submits a mutating ClickHouse statement. Unlike Query, an empty 200
// response is success because ALTER TABLE ... DELETE normally acknowledges the
// mutation without a response body. queryID is attached as the ClickHouse
// query id so callers can correlate the submitted mutation in system tables.
func (r *ClickHouseRepo) Exec(ctx context.Context, sql, queryID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/", strings.NewReader(sql))
	if err != nil {
		return Unavailable("clickhouse: build request: " + err.Error())
	}
	req.Header.Set("Content-Type", "text/plain")
	if queryID != "" {
		req.Header.Set("X-ClickHouse-Query-Id", queryID)
	}
	r.applyAuth(req)

	resp, err := r.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Timeout("clickhouse exec: " + ctx.Err().Error())
		}
		return Unavailable("clickhouse: " + err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return Unavailable(fmt.Sprintf("clickhouse: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		if ctx.Err() != nil {
			return Timeout("clickhouse exec: " + ctx.Err().Error())
		}
		return Unavailable("clickhouse exec read: " + err.Error())
	}
	return nil
}

// QueryJSON 执行 ClickHouse 查询并以 JSONEachRow 格式返回解析后的行数组。
// 复用 Query 的统一错误语义（no_data/unavailable/timeout）。SQL ownership 在调用方 repository。
func (r *ClickHouseRepo) QueryJSON(ctx context.Context, sql string) ([]map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/", nil)
	if err != nil {
		return nil, Unavailable("clickhouse: build request: " + err.Error())
	}
	q := req.URL.Query()
	q.Set("query", sql)
	q.Set("default_format", "JSONEachRow")
	req.URL.RawQuery = q.Encode()
	r.applyAuth(req) // ClickHouse Basic Auth（若配置）

	resp, err := r.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, Timeout("clickhouse query: " + ctx.Err().Error())
		}
		return nil, Unavailable("clickhouse: " + err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, Unavailable(fmt.Sprintf("clickhouse: status %d: %s", resp.StatusCode, strings.TrimSpace(string(b))))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, Unavailable("clickhouse read: " + err.Error())
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, NoData()
	}
	var rows []map[string]interface{}
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			return nil, Unavailable("clickhouse JSONEachRow parse: " + err.Error())
		}
		rows = append(rows, obj)
	}
	return rows, nil
}

// chLike 构造 LIKE 模式的安全字符串（含 % 通配符转义），返回已加引号的字面量。
// 语义对齐 handler 原 chLike：pattern 中的 % 和 _ 转义为普通字符（精确包含匹配），并包在 %...% 中。
func chLike(pattern string) string {
	escaped := strings.ReplaceAll(pattern, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `'`, `''`)
	escaped = strings.ReplaceAll(escaped, `%`, `\%`)
	escaped = strings.ReplaceAll(escaped, `_`, `\_`)
	return "'%" + escaped + "%'"
}

// scalarVal 取 JSONEachRow 首行的唯一列值并转为 float64（SLO 规则单值查询用）。
func scalarVal(row map[string]interface{}) float64 {
	for _, v := range row {
		switch x := v.(type) {
		case float64:
			return x
		case string:
			var f float64
			fmt.Sscanf(x, "%f", &f)
			return f
		case int:
			return float64(x)
		case int64:
			return float64(x)
		}
	}
	return 0
}

// isNetTimeout 判断错误是否为网络层超时（供调用方兜底分类）。
func isNetTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
