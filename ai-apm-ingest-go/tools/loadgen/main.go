// loadgen 是开发调试用的 OTLP trace 演示生成器。
// 周期性向 ai-apm-ingest 的 /v1/traces POST OTLP JSON trace，产生可控的服务 RED 指标
// （含成功/错误混合），用于验证 anomaly 检测、SLO 烧毁率等告警规则。
//
// 默认关闭（不随 ingest 部署）。仅调试环境手动启动：
//
//	LOADGEN_INGEST=http://ingest:8080 LOADGEN_API_KEY=xxx \
//	LOADGEN_SERVICES=payments,orders LOADGEN_ERROR_RATE=0.05 \
//	go run ./tools/loadgen
//
// 流量模式（LOADGEN_MODE）：
//   - steady   : 平稳流量（服务 RED 平稳基线，供 zscore/MAD 正常检测）
//   - spike    : 周期性突刺（流量短时激增，供 anomaly 突刺检测触发）
//   - error    : 持续高错误率（供 SLO 烧毁率触发）
package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// otlpSpan 构造 OTLP JSON trace 的一个 span。
type otlpSpan struct {
	TraceID           string                   `json:"traceId"`
	SpanID            string                   `json:"spanId"`
	ParentSpanID      string                   `json:"parentSpanId"`
	Name              string                   `json:"name"`
	Kind              int                      `json:"kind"`
	StartTimeUnixNano string                   `json:"startTimeUnixNano"`
	EndTimeUnixNano   string                   `json:"endTimeUnixNano"`
	Status            map[string]interface{}   `json:"status"`
	Attributes        []map[string]interface{} `json:"attributes"`
}

type otlpTrace struct {
	ResourceSpans []resourceSpans `json:"resourceSpans"`
}

type resourceSpans struct {
	Resource   resource     `json:"resource"`
	ScopeSpans []scopeSpans `json:"scopeSpans"`
}

type resource struct {
	Attributes []map[string]interface{} `json:"attributes"`
}

type scopeSpans struct {
	Spans []otlpSpan `json:"spans"`
}

func main() {
	var (
		ingest     = flag.String("ingest", os.Getenv("LOADGEN_INGEST"), "ingest /v1/traces 地址，如 http://ingest:8080")
		apiKey     = flag.String("api-key", loadgenAPIKey(), "ingest 鉴权 API key")
		services   = flag.String("services", os.Getenv("LOADGEN_SERVICES"), "逗号分隔的服务名，默认 payments")
		errorRate  = flag.Float64("error-rate", envFloat("LOADGEN_ERROR_RATE", 0.05), "错误 span 比例 0-1")
		mode       = flag.String("mode", os.Getenv("LOADGEN_MODE"), "steady|spike|error")
		interval   = flag.Duration("interval", 10*time.Second, "投喂间隔")
		qps        = flag.Int("qps", 20, "每轮 span 数量（QPS 基数）")
		logEnabled = flag.Bool("log-enabled", true, "是否同时生成日志（POST /v1/logs）")
		logQPS     = flag.Int("log-qps", 30, "每轮日志条数")
		logErrRate = flag.Float64("log-err-rate", 0.1, "错误日志(ERROR/FATAL)比例 0-1")
		once       = flag.Bool("once", false, "只投喂一轮后退出（调试用）")
		strict     = flag.Bool("strict", false, "一次性生成 payments -> orders 严格测试链路")
		marker     = flag.String("marker", os.Getenv("LOADGEN_MARKER"), "严格测试数据的唯一标记")
		rounds     = flag.Int("rounds", 1, "严格测试模式的轮数")
		tenantID   = flag.String("tenant-id", firstNonEmpty(os.Getenv("LOADGEN_TENANT_ID"), os.Getenv("AIOPS_SYSTEM_TENANT_ID")), "ingest 租户作用域")
		caFile     = flag.String("ca-file", firstNonEmpty(os.Getenv("LOADGEN_TLS_CA_FILE"), os.Getenv("AIOPS_TLS_CLIENT_CA_FILE")), "mTLS CA 文件")
		certFile   = flag.String("cert-file", firstNonEmpty(os.Getenv("LOADGEN_TLS_CERT_FILE"), os.Getenv("AIOPS_TLS_CERT_FILE")), "mTLS 客户端证书")
		keyFile    = flag.String("key-file", firstNonEmpty(os.Getenv("LOADGEN_TLS_KEY_FILE"), os.Getenv("AIOPS_TLS_KEY_FILE")), "mTLS 客户端私钥")
		serverName = flag.String("tls-server-name", os.Getenv("LOADGEN_TLS_SERVER_NAME"), "TLS server name")
	)
	flag.Parse()

	if *ingest == "" {
		*ingest = "http://ingest:8080"
	}
	if *services == "" {
		*services = "payments"
	}
	if *mode == "" {
		*mode = "steady"
	}
	svcList := strings.Split(*services, ",")
	for i := range svcList {
		svcList[i] = strings.TrimSpace(svcList[i])
	}

	if *strict {
		if *marker == "" {
			*marker = fmt.Sprintf("strict-%d", time.Now().UnixNano())
		}
		client, err := strictHTTPClient(*caFile, *certFile, *keyFile, *serverName)
		if err != nil {
			log.Fatalf("loadgen: mTLS client setup failed: %v", err)
		}
		for round := 1; round <= max(1, *rounds); round++ {
			batch := []otlpTrace{strictTrace(round, *marker, time.Now(), round%2 == 0)}
			if err := postTraces(client, *ingest, *apiKey, *tenantID, batch); err != nil {
				log.Fatalf("loadgen: strict trace post failed: %v", err)
			}
			logs := assembleLogs(round, []string{"payments", "orders"}, 4, 0.25, "steady", rngForStrict(round), *marker)
			if err := postLogs(client, *ingest, *apiKey, *tenantID, logs); err != nil {
				log.Fatalf("loadgen: strict log post failed: %v", err)
			}
			log.Printf("loadgen: strict round %d sent payments->orders trace and %d logs", round, len(logs))
		}
		return
	}

	log.Printf("loadgen: ingest=%s services=%v error_rate=%.2f mode=%s interval=%s",
		*ingest, svcList, *errorRate, *mode, *interval)

	client := &http.Client{Timeout: 10 * time.Second}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	round := 0

	for {
		round++
		n := *qps
		if *mode == "spike" {
			// 每 6 轮突刺一次（×5 流量）
			if round%6 == 0 {
				n *= 5
			}
		}
		// 每轮组装一批 span（多个服务、成功/错误混合）
		batch := assemble(round, svcList, n, *errorRate, *mode, rng)
		if err := postTraces(client, *ingest, *apiKey, *tenantID, batch); err != nil {
			log.Printf("loadgen: trace post error: %v", err)
		} else {
			log.Printf("loadgen: round %d sent %d spans (mode=%s)", round, n, *mode)
		}
		// 同时生成日志（POST /v1/logs），供 log 规则（log_error_rate/log_keyword）验证
		if *logEnabled {
			logs := assembleLogs(round, svcList, *logQPS, *logErrRate, *mode, rng, "")
			if err := postLogs(client, *ingest, *apiKey, *tenantID, logs); err != nil {
				log.Printf("loadgen: log post error: %v", err)
			} else {
				log.Printf("loadgen: round %d sent %d logs (err_rate=%.2f)", round, len(logs), *logErrRate)
			}
		}
		if *once {
			return
		}
		time.Sleep(*interval)
	}
}

func loadgenAPIKey() string {
	return firstNonEmpty(os.Getenv("LOADGEN_API_KEY"), os.Getenv("INGEST_API_KEY"))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func rngForStrict(round int) *rand.Rand {
	return rand.New(rand.NewSource(int64(round) * 1009))
}

func strictHTTPClient(caFile, certFile, keyFile, serverName string) (*http.Client, error) {
	if caFile == "" && certFile == "" && keyFile == "" {
		return &http.Client{Timeout: 15 * time.Second}, nil
	}
	return newMTLSClientWithServerName(caFile, certFile, keyFile, serverName)
}

func newMTLSClient(caFile, certFile, keyFile string) (*http.Client, error) {
	return newMTLSClientWithServerName(caFile, certFile, keyFile, "")
}

func newMTLSClientWithServerName(caFile, certFile, keyFile, serverName string) (*http.Client, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load client certificate: %w", err)
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read client CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("client CA contains no certificate")
	}
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{cert},
			RootCAs:      roots,
			ServerName:   serverName,
		}},
	}, nil
}

func strictTrace(round int, marker string, now time.Time, childError bool) otlpTrace {
	traceDigest := sha256.Sum256([]byte(fmt.Sprintf("%s/%d", marker, round)))
	traceID := fmt.Sprintf("%x", traceDigest)
	rootID := fmt.Sprintf("%016x", round*2+1)
	childID := fmt.Sprintf("%016x", round*2+2)
	start := now.Add(-150 * time.Millisecond).UnixNano()
	rootEnd := start + 80*1_000_000
	childEnd := start + 60*1_000_000
	root := otlpSpan{
		TraceID: traceID, SpanID: rootID, Name: "POST /payments", Kind: 1,
		StartTimeUnixNano: fmt.Sprintf("%d", start), EndTimeUnixNano: fmt.Sprintf("%d", rootEnd),
		Status: map[string]interface{}{"code": float64(0)}, Attributes: strictAttributes(marker, "payments"),
	}
	childStatus := float64(0)
	childName := "POST /orders"
	if childError {
		childStatus = 2
		childName = "POST /orders/error"
	}
	child := otlpSpan{
		TraceID: traceID, SpanID: childID, ParentSpanID: rootID, Name: childName, Kind: 1,
		StartTimeUnixNano: fmt.Sprintf("%d", start+10*1_000_000), EndTimeUnixNano: fmt.Sprintf("%d", childEnd),
		Status: map[string]interface{}{"code": childStatus}, Attributes: strictAttributes(marker, "orders"),
	}
	return otlpTrace{ResourceSpans: []resourceSpans{
		{Resource: resource{Attributes: resourceAttributes("payments")}, ScopeSpans: []scopeSpans{{Spans: []otlpSpan{root}}}},
		{Resource: resource{Attributes: resourceAttributes("orders")}, ScopeSpans: []scopeSpans{{Spans: []otlpSpan{child}}}},
	}}
}

func resourceAttributes(service string) []map[string]interface{} {
	return []map[string]interface{}{{"key": "service.name", "value": map[string]interface{}{"stringValue": service}}}
}

func strictAttributes(marker, service string) []map[string]interface{} {
	return append(resourceAttributes(service), map[string]interface{}{"key": "aiops.test.marker", "value": map[string]interface{}{"stringValue": marker}})
}

func resourceStringAttribute(attributes []map[string]interface{}, key string) string {
	for _, attribute := range attributes {
		if attribute["key"] != key {
			continue
		}
		value, ok := attribute["value"].(map[string]interface{})
		if !ok {
			continue
		}
		if text, ok := value["stringValue"].(string); ok {
			return text
		}
	}
	return ""
}

// assemble 生成一批 OTLP trace。
func assemble(round int, services []string, n int, errorRate float64, mode string, rng *rand.Rand) []otlpTrace {
	var traces []otlpTrace
	for i := 0; i < n; i++ {
		svc := services[rng.Intn(len(services))]
		traces = append(traces, singleTrace(round, svc, i, errorRate, mode, rng))
	}
	return traces
}

// singleTrace 生成一个含单个 SERVER span 的 trace。
func singleTrace(round int, svc string, i int, errorRate float64, mode string, rng *rand.Rand) otlpTrace {
	isErr := false
	switch mode {
	case "error":
		isErr = true // 持续高错误，供 SLO 烧毁率
	case "spike":
		isErr = rng.Float64() < errorRate
	default: // steady
		isErr = rng.Float64() < errorRate
	}
	// duration：正常 5-50ms，错误时 100-500ms
	durMs := int64(5 + rng.Intn(45))
	if isErr {
		durMs = int64(100 + rng.Intn(400))
	}
	start := time.Now().Add(-2 * time.Second).UnixNano()
	spanID := fmt.Sprintf("%016x", int64(round*100000+i+1))

	status := map[string]interface{}{"code": 0}
	if isErr {
		status = map[string]interface{}{"code": 2}
	}
	name := "/api/op"
	if isErr {
		name = "/api/err"
	}

	sp := otlpSpan{
		TraceID:           spanID + "0000000000000000",
		SpanID:            spanID,
		ParentSpanID:      "",
		Name:              name,
		Kind:              1, // SERVER（模型 KindMap: 0=INTERNAL,1=SERVER,2=CLIENT；此前误用 2 导致压测 span 被映射为 CLIENT）
		StartTimeUnixNano: fmt.Sprintf("%d", start),
		EndTimeUnixNano:   fmt.Sprintf("%d", start+durMs*1000000),
		Status:            status,
	}
	return otlpTrace{
		ResourceSpans: []resourceSpans{
			{
				Resource: resource{Attributes: []map[string]interface{}{
					{"key": "service.name", "value": map[string]interface{}{"stringValue": svc}},
				}},
				ScopeSpans: []scopeSpans{{Spans: []otlpSpan{sp}}},
			},
		},
	}
}

// postTraces 把一批 trace POST 到 ingest /v1/traces。
func postTraces(client *http.Client, ingest, apiKey, tenantID string, traces []otlpTrace) error {
	body, err := json.Marshal(map[string]interface{}{"resourceSpans": flattenResourceSpans(traces)})
	if err != nil {
		return err
	}
	req, err := newIngestRequest(http.MethodPost, strings.TrimRight(ingest, "/")+"/v1/traces", body, apiKey, tenantID)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ingest returned %d", resp.StatusCode)
	}
	return nil
}

// flattenResourceSpans 把多 trace 合并为一个 resourceSpans 数组（符合 OTLP JSON）。
func flattenResourceSpans(traces []otlpTrace) []resourceSpans {
	var out []resourceSpans
	for _, t := range traces {
		out = append(out, t.ResourceSpans...)
	}
	return out
}

// ── 日志生成（POST /v1/logs，写入 log_records，供 log 规则验证） ──────────────

// otlpLogRecord 构造 OTLP JSON 的一条日志。
type otlpLogRecord struct {
	TimeUnixNano string                   `json:"timeUnixNano"`
	SeverityText string                   `json:"severityText"`
	Body         map[string]interface{}   `json:"body"`
	Attributes   []map[string]interface{} `json:"attributes"`
}

type otlpLogRequest struct {
	ResourceLogs []logResourceSpans `json:"resourceLogs"`
}

type logResourceSpans struct {
	Resource  resource        `json:"resource"`
	ScopeLogs []logScopeSpans `json:"scopeLogs"`
}

type logScopeSpans struct {
	LogRecords []otlpLogRecord `json:"logRecords"`
}

// assembleLogs 生成一批日志（含 INFO/ERROR/FATAL 混合），供 log 规则验证。
func assembleLogs(round int, services []string, n int, errRate float64, mode string, rng *rand.Rand, marker string) []otlpLogRecord {
	var out []otlpLogRecord
	// error 模式：高错误日志比例
	effErr := errRate
	if mode == "error" {
		effErr = 0.9
	}
	for i := 0; i < n; i++ {
		svc := services[rng.Intn(len(services))]
		sev := "INFO"
		if rng.Float64() < effErr {
			if rng.Float64() < 0.2 {
				sev = "FATAL"
			} else {
				sev = "ERROR"
			}
		}
		body := "request processed"
		if sev == "ERROR" {
			body = "ERROR: connection timeout to upstream"
		} else if sev == "FATAL" {
			body = "FATAL: out of memory, process aborted"
		}
		if marker != "" {
			body = fmt.Sprintf("%s marker=%s", body, marker)
		}
		attributes := []map[string]interface{}{{"key": "service.name", "value": map[string]interface{}{"stringValue": svc}}}
		if marker != "" {
			attributes = append(attributes, map[string]interface{}{"key": "aiops.test.marker", "value": map[string]interface{}{"stringValue": marker}})
		}
		out = append(out, otlpLogRecord{
			TimeUnixNano: fmt.Sprintf("%d", time.Now().Add(-2*time.Second).UnixNano()),
			SeverityText: sev,
			Body:         map[string]interface{}{"stringValue": body},
			Attributes:   attributes,
		})
	}
	return out
}

// postLogs 把日志 POST 到 ingest /v1/logs。
func postLogs(client *http.Client, ingest, apiKey, tenantID string, logs []otlpLogRecord) error {
	// 按服务分组为 resourceLogs（每个 resource 一个 service.name）
	bySvc := map[string][]otlpLogRecord{}
	for _, l := range logs {
		svc := "unknown"
		for _, a := range l.Attributes {
			if a["key"] == "service.name" {
				if v, ok := a["value"].(map[string]interface{}); ok {
					if s, ok := v["stringValue"].(string); ok {
						svc = s
					}
				}
			}
		}
		bySvc[svc] = append(bySvc[svc], l)
	}
	var rls []logResourceSpans
	for svc, ls := range bySvc {
		rls = append(rls, logResourceSpans{
			Resource: resource{Attributes: []map[string]interface{}{
				{"key": "service.name", "value": map[string]interface{}{"stringValue": svc}},
			}},
			ScopeLogs: []logScopeSpans{{LogRecords: ls}},
		})
	}
	body, err := json.Marshal(otlpLogRequest{ResourceLogs: rls})
	if err != nil {
		return err
	}
	req, err := newIngestRequest(http.MethodPost, strings.TrimRight(ingest, "/")+"/v1/logs", body, apiKey, tenantID)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ingest returned %d", resp.StatusCode)
	}
	return nil
}

func newIngestRequest(method, url string, body []byte, apiKey, tenantID string) (*http.Request, error) {
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("X-Api-Key", apiKey)
	}
	if tenantID != "" {
		req.Header.Set("X-Tenant-ID", tenantID)
	}
	return req, nil
}

// envFloat 读取浮点环境变量，缺失或非法时返回默认值（P1-6 修复）。
func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}
