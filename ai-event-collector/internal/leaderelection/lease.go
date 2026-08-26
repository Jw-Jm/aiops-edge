// Package leaderelection 提供访问 coordination.k8s.io/v1 Lease 的最小实现，
// 用于 Event Collector 的集群级 K8s watch single-leader 语义（V9.2 §71）。
//
// 仅访问 coordination.k8s.io/v1 Lease（get/create/update），不引入大型 Kubernetes
// framework（client-go）。RBAC 仅需 coordination.k8s.io/leases get/create/update。
package leaderelection

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// leaseTimeLayout 是 K8s Lease 时间字段要求的格式：微秒精度。
// RFC3339（2006-01-02T15:04:05Z）会被 API 拒绝（400 "cannot parse Z as .000000"）。
const leaseTimeLayout = "2006-01-02T15:04:05.000000Z"

// nowMicros 返回当前 UTC 时间的 K8s Lease 兼容格式（微秒精度）。
func nowMicros() string {
	return time.Now().UTC().Format(leaseTimeLayout)
}

// kubeLease 是 coordination.k8s.io/v1 Lease 的最小 JSON 表示（仅承载 identity）。
type kubeLease struct {
	Metadata struct {
		Name            string `json:"name"`
		ResourceVersion string `json:"resourceVersion"`
	} `json:"metadata"`
	Spec struct {
		HolderIdentity       string `json:"holderIdentity"`
		LeaseDurationSeconds int32  `json:"leaseDurationSeconds"`
		AcquireTime          string `json:"acquireTime"`
		RenewTime            string `json:"renewTime"`
		LeaseTransitions     int32  `json:"leaseTransitions"`
	} `json:"spec"`
}

// LeaseInfo 一次 Get 的结果：当前 holderIdentity 与最近续租时间。
type LeaseInfo struct {
	Holder          string
	AcquireTime     string
	RenewTime       time.Time // 零值表示未知/Lease 不存在
	ResourceVersion string
}

// LeaseClient 是对 coordination.k8s.io/v1 Lease 的窄读写接口。
type LeaseClient interface {
	// Get 返回当前 Lease 信息；Lease 不存在返回 (LeaseInfo{}, nil)。
	Get(ctx context.Context, namespace, name string) (LeaseInfo, error)
	// Create 创建 Lease（假设不存在）；若已存在由调用方改走 Update。
	Create(ctx context.Context, namespace, name, holderIdentity string) error
	// Update 更新 holderIdentity 与 renewTime（续租）。
	Update(ctx context.Context, namespace, name, holderIdentity string) error
	// Release 主动释放 lease（把 holder 清空并置 renewTime 为过去），使 follower 可立即接管。
	// 用于 leader 正常退出（ctx 取消）时的 graceful handoff，避免等租约自然过期。
	Release(ctx context.Context, namespace, name string) error
}

type leaseClient struct {
	baseURL string
	http    *http.Client
	token   string
}

// NewLeaseClient 构造 Lease client（用于测试/无鉴权环境）。kubeAPIBase 形如
// "https://<apiserver>" 或测试 server URL。
func NewLeaseClient(kubeAPIBase string) LeaseClient {
	return &leaseClient{
		baseURL: strings.TrimRight(kubeAPIBase, "/"),
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// NewLeaseClientWithToken 构造带 Bearer token 的 Lease client（in-cluster），并接受
// 复用外部配置好的 http.Client（含 service account CA 的 TLS 配置）。
func NewLeaseClientWithToken(kubeAPIBase, token string, hc *http.Client) LeaseClient {
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &leaseClient{
		baseURL: strings.TrimRight(kubeAPIBase, "/"),
		http:    hc,
		token:   strings.TrimSpace(token),
	}
}

func (c *leaseClient) leaseURL(namespace, name string) string {
	return fmt.Sprintf("%s/apis/coordination.k8s.io/v1/namespaces/%s/leases/%s", c.baseURL, namespace, name)
}

// leaseCollectionURL 返回 Lease 的集合 URL（不含资源名）。K8s API 语义：创建资源必须
// POST 到集合 URL（/apis/.../leases），POST 到单个资源 URL（.../leases/{name}）会返回
// 405 MethodNotAllowed。此修复解决真实 K8s API 下的 leader 选举 create 失败。
func (c *leaseClient) leaseCollectionURL(namespace string) string {
	return fmt.Sprintf("%s/apis/coordination.k8s.io/v1/namespaces/%s/leases", c.baseURL, namespace)
}

// authorize 为 in-cluster 请求附加 Bearer token。
func (c *leaseClient) authorize(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

func (c *leaseClient) Get(ctx context.Context, namespace, name string) (LeaseInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.leaseURL(namespace, name), nil)
	if err != nil {
		return LeaseInfo{}, err
	}
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return LeaseInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return LeaseInfo{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return LeaseInfo{}, fmt.Errorf("get lease %s/%s: status %d: %s", namespace, name, resp.StatusCode, string(b))
	}
	var l kubeLease
	if err := json.NewDecoder(resp.Body).Decode(&l); err != nil {
		return LeaseInfo{}, err
	}
	info := LeaseInfo{
		Holder:          l.Spec.HolderIdentity,
		AcquireTime:     l.Spec.AcquireTime,
		ResourceVersion: l.Metadata.ResourceVersion,
	}
	if l.Spec.RenewTime != "" {
		if rt, err := time.Parse(time.RFC3339, l.Spec.RenewTime); err == nil {
			info.RenewTime = rt
		}
	}
	return info, nil
}

func (c *leaseClient) Create(ctx context.Context, namespace, name, holderIdentity string) error {
	l := kubeLease{}
	l.Metadata.Name = name
	l.Spec.HolderIdentity = holderIdentity
	l.Spec.LeaseDurationSeconds = 15
	l.Spec.AcquireTime = nowMicros()
	l.Spec.RenewTime = nowMicros()
	body, _ := json.Marshal(l)
	// 创建必须 POST 到集合 URL（不带 {name}），否则 K8s API 返回 405 MethodNotAllowed。
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.leaseCollectionURL(namespace), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("create lease %s/%s: status %d: %s", namespace, name, resp.StatusCode, string(b))
	}
	return nil
}

func (c *leaseClient) Update(ctx context.Context, namespace, name, holderIdentity string) error {
	info, err := c.Get(ctx, namespace, name)
	if err != nil {
		return err
	}
	if info.ResourceVersion == "" {
		return fmt.Errorf("update lease %s/%s: resourceVersion missing", namespace, name)
	}
	return c.put(ctx, namespace, name, holderIdentity, info.ResourceVersion, info.AcquireTime, time.Now())
}

// Release 清空 holder 并把 renewTime 置为过去，使 follower 可立即接管。
func (c *leaseClient) Release(ctx context.Context, namespace, name string) error {
	info, err := c.Get(ctx, namespace, name)
	if err != nil {
		return err
	}
	if info.ResourceVersion == "" {
		return nil
	}
	return c.put(ctx, namespace, name, "", info.ResourceVersion, info.AcquireTime, time.Now().Add(-time.Hour))
}

func (c *leaseClient) put(ctx context.Context, namespace, name, holderIdentity, resourceVersion, acquireTime string, renewTime time.Time) error {
	l := kubeLease{}
	l.Metadata.Name = name
	l.Metadata.ResourceVersion = resourceVersion
	l.Spec.HolderIdentity = holderIdentity
	l.Spec.LeaseDurationSeconds = 15
	l.Spec.AcquireTime = acquireTime
	// K8s Lease 的 renewTime/acquireTime 要求微秒精度（2006-01-02T15:04:05.000000Z），
	// 用 RFC3339（无微秒）会导致 API 400 "cannot parse Z as .000000"。
	l.Spec.RenewTime = renewTime.UTC().Format(leaseTimeLayout)
	body, _ := json.Marshal(l)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.leaseURL(namespace, name), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("update lease %s/%s: status %d: %s", namespace, name, resp.StatusCode, string(b))
	}
	return nil
}
