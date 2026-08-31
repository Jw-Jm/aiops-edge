// ai-credential-broker exchanges a pre-registered credential_ref for a
// short-lived Kubernetes service-account token.  It is deliberately a small
// boundary service: callers cannot submit arbitrary service-account names,
// namespaces, resources or operations; every request is matched against an
// operator-managed profile before a TokenRequest is sent to the API server.
package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

type profile struct {
	Ref         string
	Namespace   string
	ServiceAcct string
	Operations  map[string]bool
}

type tokenRequest struct {
	CredentialRef string `json:"credential_ref"`
	ClusterID     string `json:"cluster_id"`
	Namespace     string `json:"namespace"`
	Resource      string `json:"resource"`
	Operation     string `json:"operation"`
	Audience      string `json:"audience"`
	TTLSeconds    int64  `json:"ttl_seconds"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresAt   string `json:"expires_at"`
	Profile     string `json:"profile"`
}

type server struct {
	token      string
	profiles   map[string]profile
	apiURL     string
	apiToken   string
	httpClient *http.Client
	requestMu  sync.Mutex
}

var canonicalClusterID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func main() {
	profiles, err := parseProfiles(os.Getenv("CREDENTIAL_PROFILES"))
	if err != nil {
		log.Fatalf("invalid CREDENTIAL_PROFILES: %v", err)
	}
	apiToken := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICEACCOUNT_TOKEN"))
	if apiToken == "" {
		if raw, readErr := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token"); readErr == nil {
			apiToken = strings.TrimSpace(string(raw))
		}
	}
	s := &server{
		token:      strings.TrimSpace(os.Getenv("BROKER_TOKEN")),
		profiles:   profiles,
		apiURL:     strings.TrimRight(strings.TrimSpace(os.Getenv("KUBERNETES_API_URL")), "/"),
		apiToken:   apiToken,
		httpClient: newKubernetesClient(),
	}
	if s.apiURL == "" {
		host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
		if host != "" {
			s.apiURL = "https://" + host + ":" + firstNonEmpty(port, "443")
		}
	}
	if strings.EqualFold(os.Getenv("AIOPS_ENV"), "production") {
		if s.token == "" || s.apiToken == "" || s.apiURL == "" || len(s.profiles) == 0 {
			log.Fatal("production broker requires BROKER_TOKEN, Kubernetes API credentials and at least one profile")
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/readyz", s.ready)
	mux.HandleFunc("/v1/credentials/token", s.issue)
	port := firstNonEmpty(os.Getenv("BROKER_PORT"), "8080")
	server := &http.Server{Addr: ":" + port, Handler: requireMTLS(mux)}
	if err := configureMTLSServer(server); err != nil {
		log.Fatalf("mTLS configuration: %v", err)
	}
	log.Printf("ai-credential-broker listening on :%s profiles=%d", port, len(s.profiles))
	if err := listenHTTP(server); err != nil {
		log.Fatal(err)
	}
}

func parseProfiles(raw string) (map[string]profile, error) {
	out := map[string]profile{}
	for _, item := range strings.Split(raw, ";") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts := strings.Split(item, "|")
		if len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
			return nil, fmt.Errorf("profile must be ref|namespace|serviceaccount|operations: %q", item)
		}
		ops := map[string]bool{}
		for _, op := range strings.Split(parts[3], ",") {
			op = strings.ToLower(strings.TrimSpace(op))
			if op != "" {
				ops[op] = true
			}
		}
		if len(ops) == 0 {
			return nil, fmt.Errorf("profile %q has no operations", parts[0])
		}
		out[parts[0]] = profile{Ref: parts[0], Namespace: parts[1], ServiceAcct: parts[2], Operations: ops}
	}
	return out, nil
}

func (s *server) authorized(r *http.Request) bool {
	if s.token == "" {
		return false
	}
	got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	return got != "" && got == s.token
}

func (s *server) ready(w http.ResponseWriter, r *http.Request) {
	if strings.EqualFold(os.Getenv("AIOPS_ENV"), "production") && (s.apiURL == "" || s.apiToken == "") {
		http.Error(w, "credential broker is not configured", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *server) issue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	var req tokenRequest
	if json.Unmarshal(body, &req) != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	p, ok := s.profiles[req.CredentialRef]
	if !ok || !canonicalClusterID.MatchString(strings.ToLower(req.ClusterID)) || req.Namespace != p.Namespace || req.Resource == "" || !p.Operations[strings.ToLower(req.Operation)] {
		http.Error(w, "credential profile denied", http.StatusForbidden)
		return
	}
	if req.TTLSeconds <= 0 || req.TTLSeconds > 300 {
		req.TTLSeconds = 300
	}
	if req.Audience == "" {
		req.Audience = "https://kubernetes.default.svc"
	}
	if s.apiURL == "" || s.apiToken == "" {
		http.Error(w, "credential broker unavailable", http.StatusServiceUnavailable)
		return
	}

	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	requestBody, _ := json.Marshal(map[string]any{
		"apiVersion": "authentication.k8s.io/v1",
		"kind":       "TokenRequest",
		"spec": map[string]any{
			"audiences":         []string{req.Audience},
			"expirationSeconds": req.TTLSeconds,
		},
	})
	target := s.apiURL + "/api/v1/namespaces/" + url.PathEscape(p.Namespace) + "/serviceaccounts/" + url.PathEscape(p.ServiceAcct) + "/token"
	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target, bytes.NewReader(requestBody))
	if err != nil {
		http.Error(w, "credential request failed", http.StatusBadGateway)
		return
	}
	httpReq.Header.Set("Authorization", "Bearer "+s.apiToken)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		http.Error(w, "credential broker unavailable", http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		http.Error(w, "credential request denied", http.StatusForbidden)
		return
	}
	var token struct {
		Status struct {
			Token               string `json:"token"`
			ExpirationTimestamp string `json:"expirationTimestamp"`
		} `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil || token.Status.Token == "" {
		http.Error(w, "credential response invalid", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: token.Status.Token, ExpiresAt: token.Status.ExpirationTimestamp, Profile: p.Ref})
}

func newKubernetesClient() *http.Client {
	tlsConf := &tls.Config{MinVersion: tls.VersionTLS12}
	if ca, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"); err == nil {
		pool := x509.NewCertPool()
		if pool.AppendCertsFromPEM(ca) {
			tlsConf.RootCAs = pool
		}
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsConf}}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
