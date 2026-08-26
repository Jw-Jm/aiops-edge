package api

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

var defaultLogShipperNamespaces = []string{"observability", "default", "deepflow"}

// configuredLogShipperNamespaces parses the optional explicit namespace
// allowlist. An empty value means the shipper should discover namespaces from
// the Kubernetes API instead of silently limiting collection to platform
// namespaces.
func configuredLogShipperNamespaces(raw string) []string {
	seen := make(map[string]struct{})
	for _, item := range strings.Split(raw, ",") {
		name := strings.TrimSpace(item)
		if name != "" {
			seen[name] = struct{}{}
		}
	}
	namespaces := make([]string, 0, len(seen))
	for name := range seen {
		namespaces = append(namespaces, name)
	}
	sort.Strings(namespaces)
	return namespaces
}

func parseLogShipperNamespaces(body []byte) ([]string, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(list.Items))
	for _, item := range list.Items {
		if name := strings.TrimSpace(item.Metadata.Name); name != "" {
			seen[name] = struct{}{}
		}
	}
	namespaces := make([]string, 0, len(seen))
	for name := range seen {
		namespaces = append(namespaces, name)
	}
	sort.Strings(namespaces)
	return namespaces, nil
}

func discoverLogShipperNamespaces(client *http.Client, k8sAPI string, token []byte) ([]string, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(k8sAPI, "/")+"/api/v1/namespaces", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+string(token))
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("namespace discovery returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseLogShipperNamespaces(body)
}

func buildLogShipperPayload(timestamp, message, namespace, pod, phase, tenantID, clusterID string) map[string]string {
	serviceName := namespace + "/" + pod
	return map[string]string{
		"_time":        timestamp,
		"_msg":         message,
		"service":      serviceName,
		"service_name": serviceName,
		"namespace":    namespace,
		"pod":          pod,
		"phase":        phase,
		"tenant_id":    tenantID,
		"cluster_id":   clusterID,
	}
}

func (h *Handler) StartLogShipper() {
	go func() {
		time.Sleep(10 * time.Second)
		log.Println("[log-shipper] production shipper started")

		token, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
		if err != nil {
			log.Printf("[log-shipper] FATAL: cannot read K8s token: %v", err)
			return
		}
		tenantID := strings.TrimSpace(os.Getenv("AIOPS_SYSTEM_TENANT_ID"))
		clusterID := strings.TrimSpace(os.Getenv("AIOPS_SYSTEM_CLUSTER_ID"))
		if tenantID == "" || clusterID == "" {
			log.Printf("[log-shipper] FATAL: canonical tenant/cluster scope is not configured")
			return
		}
		k8sAPI := os.Getenv("K8S_API_URL")
		if k8sAPI == "" {
			k8sAPI = "https://kubernetes.default.svc"
		}
		vlURL := os.Getenv("VICTORIA_LOGS_URL")
		if vlURL == "" {
			vlURL = "http://victoria-logs.observability.svc.cluster.local:9428/insert/jsonline"
		}
		// K8s TLS 校验：默认开启（C-05 / F-12：生产 Query API 必须验证目标集群 API Server
		// 证书/CA）；insecureSkipVerify=true 只能用于明确标记的本地验证 profile。
		insecure := strings.EqualFold(os.Getenv("K8S_INSECURE_SKIP_VERIFY"), "true")
		if insecure {
			log.Printf("WARN[C-05/F-12]: K8S_INSECURE_SKIP_VERIFY=true — disabling K8s API TLS certificate verification; ONLY for explicit local verification profile; production MUST verify cluster API server cert/CA")
		}
		tlsConf := &tls.Config{InsecureSkipVerify: insecure, MinVersion: tls.VersionTLS12}
		if !insecure {
			// 生产必须验证 API Server 证书：显式加载 in-cluster CA（K8s 注入的
			// /var/run/secrets/kubernetes.io/serviceaccount/ca.crt），失败则 fail-closed
			// （不静默退化为 InsecureSkipVerify）。本地验证 profile 才允许 insecure=true。
			if ca, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"); err == nil && len(ca) > 0 {
				pool := x509.NewCertPool()
				if pool.AppendCertsFromPEM(ca) {
					tlsConf.RootCAs = pool
				} else {
					log.Printf("WARN[C-05]: in-cluster CA present but not parseable; using system roots for K8s API TLS verification")
				}
			}
		}
		httpClient := &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: tlsConf,
				MaxIdleConns:    20,
				IdleConnTimeout: 30 * time.Second,
			},
		}
		configuredNamespaces := configuredLogShipperNamespaces(os.Getenv("LOG_SHIPPER_NAMESPACES"))

		for {
			roundShipped := 0
			roundSince := time.Now().Add(-61 * time.Second).Format(time.RFC3339)
			namespaces := configuredNamespaces
			if len(namespaces) == 0 {
				discovered, discoverErr := discoverLogShipperNamespaces(httpClient, k8sAPI, token)
				if discoverErr != nil || len(discovered) == 0 {
					if discoverErr != nil {
						log.Printf("[log-shipper] namespace discovery unavailable, using fallback: %v", discoverErr)
					}
					namespaces = defaultLogShipperNamespaces
				} else {
					namespaces = discovered
				}
			}

			for _, ns := range namespaces {
				// List pods
				req, _ := http.NewRequest("GET", k8sAPI+"/api/v1/namespaces/"+ns+"/pods", nil)
				req.Header.Set("Authorization", "Bearer "+string(token))
				resp, err := httpClient.Do(req)
				if err != nil {
					continue
				}
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 200 {
					continue
				}

				var podList struct {
					Items []struct {
						Metadata struct {
							Name string `json:"name"`
						} `json:"metadata"`
						Status struct {
							Phase string `json:"phase"`
						} `json:"status"`
					} `json:"items"`
				}
				json.Unmarshal(body, &podList)

				for _, pod := range podList.Items {
					pname := pod.Metadata.Name
					key := ns + "/" + pname

					// Use per-pod cursor for incremental fetch; fallback to 60s window
					logCursors.Lock()
					since := logCursors.cursors[key]
					if since == "" {
						since = roundSince
					}
					logCursors.Unlock()

					// Fetch logs with sinceTime for incremental collection
					u := fmt.Sprintf("%s/api/v1/namespaces/%s/pods/%s/log?sinceTime=%s&timestamps=true&tailLines=50",
						k8sAPI, ns, pname, since)
					req, _ := http.NewRequest("GET", u, nil)
					req.Header.Set("Authorization", "Bearer "+string(token))
					logResp, err := httpClient.Do(req)
					if err != nil {
						continue
					}
					logBody, _ := io.ReadAll(logResp.Body)
					logResp.Body.Close()

					text := string(logBody)
					if len(text) == 0 {
						continue
					}

					var latestTS string
					lines := strings.Split(text, "\n")
					batch := make([]string, 0, len(lines))
					for _, line := range lines {
						line = strings.TrimSpace(line)
						if line == "" {
							continue
						}
						// 健壮时间戳解析：仅当行首为 RFC3339 时间戳时用作 _time；否则用当前时间
						msg := line
						ts := time.Now().UTC().Format(time.RFC3339Nano)
						if idx := strings.Index(line, " "); idx > 0 {
							if _, err := time.Parse(time.RFC3339Nano, line[:idx]); err == nil {
								ts = line[:idx]
								msg = line[idx+1:]
								latestTS = ts
							}
						}
						if len(msg) > 2000 {
							msg = msg[:2000]
						}
						payload := buildLogShipperPayload(ts, msg, ns, pname, pod.Status.Phase, tenantID, clusterID)
						data, _ := json.Marshal(payload)
						batch = append(batch, string(data))
					}
					// 批量推送（JSON Lines），避免逐条 HTTP 请求
					if len(batch) > 0 {
						body := strings.Join(batch, "\n")
						vlReq, _ := http.NewRequest("POST", vlURL, strings.NewReader(body))
						vlReq.Header.Set("Content-Type", "application/json")
						if vlResp, err := httpClient.Do(vlReq); err == nil {
							vlResp.Body.Close()
							roundShipped += len(batch)
						}
					}
					// Update cursor for next round
					if latestTS != "" {
						logCursors.Lock()
						logCursors.cursors[key] = latestTS
						logCursors.Unlock()
					}
				}
			}
			if roundShipped > 0 {
				log.Printf("[log-shipper] shipped %d logs, cursors: %d pods", roundShipped, len(logCursors.cursors))
			}
			time.Sleep(30 * time.Second)
		}
	}()
}
