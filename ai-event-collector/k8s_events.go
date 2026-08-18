package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// service account 挂载路径（in-cluster 自动注入）
	saTokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	saCAFile    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"

	// watch 服务端超时（秒），到点服务端主动断流，触发重新 LIST 以恢复一致视图
	maxWatchTimeout = 300

	// maxRecentEventUIDs 内存去重集合上限：保留最近 N 个事件 UID，
	// 防止 LIST→WATCH 循环/重连时同一事件被重复写入（H1）。
	maxRecentEventUIDs = 1000

	// resumeTolerance 断点续采容忍窗口：重启后接受 ts >= checkpoint - 5min 的事件，
	// 容忍晚到/乱序事件（checkpoint 为 max(ts)，直接按 max(ts) 过滤会丢乱序事件）。
	resumeTolerance = 5 * time.Minute
)

// k8sEvent 覆盖 core/v1 与 events.k8s.io/v1 两种 Event 字段（字段冗余，按需取用）。
type k8sEvent struct {
	Metadata struct {
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
		UID       string `json:"uid"`
	} `json:"metadata"`

	// core/v1
	InvolvedObject struct {
		Kind      string `json:"kind"`
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
	} `json:"involvedObject"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Type    string `json:"type"`
	Source  struct {
		Component string `json:"component"`
	} `json:"source"`
	LastTimestamp string `json:"lastTimestamp"`

	// events.k8s.io/v1
	Regarding struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"regarding"`
	Note                    string `json:"note"`
	ReportingController     string `json:"reportingController"`
	EventTime               string `json:"eventTime"`
	DeprecatedLastTimestamp string `json:"deprecatedLastTimestamp"`
}

type eventList struct {
	Metadata struct {
		ResourceVersion string `json:"resourceVersion"`
		Continue        string `json:"continue"`
	} `json:"metadata"`
	Items []k8sEvent `json:"items"`
}

// watchEvent K8s watch 流中的单条消息。
type watchEvent struct {
	Type   string          `json:"type"` // ADDED | MODIFIED | DELETED | BOOKMARK | ERROR
	Object json.RawMessage `json:"object"`
}

// k8sWatcher 通过 K8s REST API 采集事件（纯标准库实现，无 client-go 依赖）。
// 流程：断点续采（CH 查最新 ts，容忍 5min 乱序）→ LIST 全量（过滤 Warning/Error）→ watch=true 增量，
// 断连自动重连（重新 LIST）。
// H1 去重：内存维护最近 maxRecentEventUIDs 个事件 UID，LIST/WATCH 阶段对同一 UID 只写一次；
// 重启后 checkpoint 回退 5min 容忍乱序，残余重复由 ReplacingMergeTree（含 name/message 的完整键）兜底。
type k8sWatcher struct {
	cfg        *Config
	writer     *EventWriter
	client     *http.Client
	baseURL    string
	token      string
	eventsPath string // 探测成功后缓存，如 /api/v1/events
	collected  atomic.Int64

	dedupMu   sync.Mutex
	dedup     map[string]struct{} // 最近事件 UID 集合
	dedupRing []string            // FIFO 顺序，用于淘汰最旧 UID
}

// NewK8sWatcher 构造 in-cluster K8s REST 客户端。
// KUBERNETES_SERVICE_HOST/PORT 缺失（非 in-cluster）时返回错误，调用方可选择禁用该采集器。
func NewK8sWatcher(cfg *Config, writer *EventWriter) (*k8sWatcher, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, fmt.Errorf("KUBERNETES_SERVICE_HOST/PORT not set (not running in-cluster)")
	}
	tokenBytes, err := os.ReadFile(saTokenFile)
	if err != nil {
		return nil, fmt.Errorf("read service account token: %w", err)
	}

	tr := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 0, // watch 长连接不设响应头超时
	}
	if ca, err := os.ReadFile(saCAFile); err == nil {
		pool := x509.NewCertPool()
		if pool.AppendCertsFromPEM(ca) {
			tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
		}
	} else {
		log.Printf("K8S watcher: ca.crt not found (%v), using system roots", err)
	}

	return &k8sWatcher{
		cfg:       cfg,
		writer:    writer,
		client:    &http.Client{Transport: tr},
		baseURL:   "https://" + net.JoinHostPort(host, port),
		token:     strings.TrimSpace(string(tokenBytes)),
		dedup:     make(map[string]struct{}),
		dedupRing: make([]string, 0, maxRecentEventUIDs),
	}, nil
}

// seen 记录事件 UID 并返回是否已见过（重复则跳过写入）。UID 为空时不做去重（放行）。
// 集合上限 maxRecentEventUIDs，FIFO 淘汰最旧，避免内存无界增长。
func (k *k8sWatcher) seen(uid string) bool {
	if uid == "" {
		return false
	}
	k.dedupMu.Lock()
	defer k.dedupMu.Unlock()
	if _, ok := k.dedup[uid]; ok {
		return true
	}
	k.dedup[uid] = struct{}{}
	k.dedupRing = append(k.dedupRing, uid)
	if len(k.dedupRing) > maxRecentEventUIDs {
		old := k.dedupRing[0]
		k.dedupRing = k.dedupRing[1:]
		delete(k.dedup, old)
	}
	return false
}

// Run 主循环：断点续采 + LIST→WATCH，断连自动重连（指数退避，不崩溃）。
func (k *k8sWatcher) Run(ctx context.Context) {
	resumeTs := k.checkpoint()
	// H1：checkpoint 为 max(ts)，回退 5min 容忍晚到/乱序事件，避免重启后丢乱序数据。
	if !resumeTs.IsZero() {
		resumeTs = resumeTs.Add(-resumeTolerance)
	}
	log.Printf("K8S watcher: started (cluster=%s, resumeTs=%s)", k.cfg.ClusterID, fmtTime(resumeTs))
	backoff := 5 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		err := k.syncOnce(ctx, resumeTs)
		if err == nil {
			backoff = 5 * time.Second
			continue
		}
		log.Printf("K8S watcher: sync error: %v (reconnect in %s)", err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > 5*time.Minute {
			backoff = 5 * time.Minute
		}
	}
}

// checkpoint 从 CH 查询本 source 最新事件时间戳（断点续采）。
func (k *k8sWatcher) checkpoint() time.Time {
	t, err := k.writer.QueryLatestTS("k8s")
	if err != nil {
		log.Printf("K8S watcher: checkpoint query failed (start from watch beginning): %v", err)
		return time.Time{}
	}
	return t
}

// syncOnce 执行一次完整同步：LIST 全量 → 过滤入库 → watch 增量。
func (k *k8sWatcher) syncOnce(ctx context.Context, resumeTs time.Time) error {
	rv, events, err := k.listEvents(ctx)
	if err != nil {
		return err
	}
	kept := 0
	skippedDup := 0
	for i := range events {
		e := &events[i]
		if !keepEvent(e, resumeTs) {
			continue
		}
		// H1：同一 UID 的事件只写一次（LIST→WATCH 循环/重连时避免重复写）
		if k.seen(e.Metadata.UID) {
			skippedDup++
			continue
		}
		k.writer.Add(k.toEvent(e))
		kept++
	}
	k.collected.Add(int64(kept))
	log.Printf("K8S watcher: listed %d events, kept %d (Warning/Error), dedup-skipped %d, watch from rv=%s",
		len(events), kept, skippedDup, rv)
	return k.watchLoop(ctx, rv)
}

// listEvents 执行全量 LIST（自动分页），返回 resourceVersion 与事件列表。
// 优先 core/v1（/api/v1/events），不存在则回退 events.k8s.io/v1。
func (k *k8sWatcher) listEvents(ctx context.Context) (string, []k8sEvent, error) {
	var paths []string
	if k.eventsPath != "" {
		paths = []string{k.eventsPath}
	} else {
		paths = []string{"/api/v1/events", "/apis/events.k8s.io/v1/events"}
	}
	var lastErr error
	for _, p := range paths {
		rv, items, err := k.listFromPath(ctx, p)
		if err != nil {
			lastErr = err
			log.Printf("K8S watcher: events endpoint %s failed: %v", p, err)
			continue
		}
		k.eventsPath = p
		return rv, items, nil
	}
	return "", nil, lastErr
}

func (k *k8sWatcher) listFromPath(ctx context.Context, path string) (string, []k8sEvent, error) {
	var all []k8sEvent
	rv := ""
	continueTok := ""
	for {
		u := k.baseURL + path
		if continueTok != "" {
			u += "?continue=" + url.QueryEscape(continueTok)
		}
		reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u, nil)
		if err != nil {
			cancel()
			return "", nil, err
		}
		req.Header.Set("Authorization", "Bearer "+k.token)
		req.Header.Set("Accept", "application/json")
		resp, err := k.client.Do(req)
		cancel()
		if err != nil {
			return "", nil, err
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return "", nil, readErr
		}
		switch resp.StatusCode {
		case http.StatusOK:
		case http.StatusNotFound:
			return "", nil, fmt.Errorf("events API %s not found (status 404)", path)
		case http.StatusForbidden:
			return "", nil, fmt.Errorf("events API %s forbidden (RBAC): %s", path, strings.TrimSpace(string(body)))
		default:
			return "", nil, fmt.Errorf("list events %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var el eventList
		if err := json.Unmarshal(body, &el); err != nil {
			return "", nil, fmt.Errorf("decode EventList: %w", err)
		}
		if rv == "" {
			rv = el.Metadata.ResourceVersion
		}
		all = append(all, el.Items...)
		continueTok = el.Metadata.Continue
		if continueTok == "" {
			break
		}
	}
	return rv, all, nil
}

// watchLoop 从 rv 开始 watch 增量事件，流式解码。服务端超时断流（EOF）视为正常，
// 返回 nil 触发重新 LIST；410 Gone 等错误也返回错误触发重连。
func (k *k8sWatcher) watchLoop(ctx context.Context, rv string) error {
	u := fmt.Sprintf("%s%s?watch=true&allowWatchBookmarks=true&timeoutSeconds=%d&resourceVersion=%s",
		k.baseURL, k.eventsPath, maxWatchTimeout, url.QueryEscape(rv))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+k.token)
	req.Header.Set("Accept", "application/json")

	resp, err := k.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("watch request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusGone { // 410: resourceVersion 已过期
		return fmt.Errorf("watch: resourceVersion expired (410 Gone), need relist")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("watch: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	dec := json.NewDecoder(resp.Body)
	for {
		var we watchEvent
		if err := dec.Decode(&we); err != nil {
			if err == io.EOF {
				return nil // 服务端超时断流 → 触发重连（重新 LIST）
			}
			return fmt.Errorf("watch stream decode: %w", err)
		}
		switch we.Type {
		case "ADDED", "MODIFIED":
			var e k8sEvent
			if err := json.Unmarshal(we.Object, &e); err != nil {
				log.Printf("K8S watcher: skip unparseable event: %v", err)
				continue
			}
			if !keepEvent(&e, time.Time{}) { // 增量阶段仅按类型过滤
				continue
			}
			// H1：同一 UID 的事件只写一次（LIST 已入库的事件在 watch 增量中不再重复写）
			if k.seen(e.Metadata.UID) {
				continue
			}
			k.writer.Add(k.toEvent(&e))
			k.collected.Add(1)
		case "BOOKMARK":
			var b struct {
				Metadata struct {
					ResourceVersion string `json:"resourceVersion"`
				} `json:"metadata"`
			}
			_ = json.Unmarshal(we.Object, &b)
			if b.Metadata.ResourceVersion != "" {
				rv = b.Metadata.ResourceVersion
			}
		case "ERROR":
			var st struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			}
			_ = json.Unmarshal(we.Object, &st)
			if st.Code == http.StatusGone {
				return fmt.Errorf("watch: resourceVersion expired during stream")
			}
			log.Printf("K8S watcher: watch error event: %d %s", st.Code, st.Message)
			return fmt.Errorf("watch error event")
		case "DELETED":
			// 事件删除无需入库
		}
	}
}

// keepEvent 过滤：只保留 type=Warning 或 Error 的事件（Normal 丢弃，避免日志爆炸），
// 并按断点续采时间过滤已入库的旧事件（resumeTs 为零值时跳过时间过滤）。
func keepEvent(e *k8sEvent, resumeTs time.Time) bool {
	if e.Type != "Warning" && e.Type != "Error" {
		return false
	}
	if !resumeTs.IsZero() {
		ts := eventTime(e)
		if !ts.IsZero() && ts.Before(resumeTs) {
			return false
		}
	}
	return true
}

// eventTime 依次尝试 lastTimestamp / eventTime / deprecatedLastTimestamp 解析事件时间。
func eventTime(e *k8sEvent) time.Time {
	for _, s := range []string{e.LastTimestamp, e.EventTime, e.DeprecatedLastTimestamp} {
		if s == "" {
			continue
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UTC()
			}
		}
	}
	return time.Time{}
}

// toEvent 将 K8s 事件映射为统一 Event 结构（source=k8s）。
func (k *k8sWatcher) toEvent(e *k8sEvent) *Event {
	ts := eventTime(e)
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	ioKind := firstNonEmpty(e.InvolvedObject.Kind, e.Regarding.Kind)
	ioName := firstNonEmpty(e.InvolvedObject.Name, e.Regarding.Name)
	ns := firstNonEmpty(e.Metadata.Namespace, e.InvolvedObject.Namespace)
	msg := firstNonEmpty(e.Message, e.Note)
	component := firstNonEmpty(e.Source.Component, e.ReportingController)

	involved := ioName
	if ioKind != "" {
		if ioName != "" {
			involved = ioKind + "/" + ioName
		} else {
			involved = ioKind
		}
	}
	return &Event{
		Ts:              ts,
		Namespace:       ns,
		Kind:            "Event",
		Name:            e.Metadata.Name,
		Reason:          e.Reason,
		Type:            e.Type,
		Message:         msg,
		InvolvedObject:  involved,
		SourceComponent: component,
		Source:          "k8s",
		Node:            "",
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "none"
	}
	return t.Format(time.RFC3339Nano)
}
