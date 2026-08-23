// Package k8sboundary implements the V9.2 Kubernetes Access Boundary.
//
// It is the only place that resolves a canonical cluster's credential_ref into a
// usable kubeconfig (via a Kubernetes Secret), binds it to the authoritative
// Kubernetes identity (kube-system Namespace metadata.uid) recorded at
// registration time, and constructs validated clients. Agents, Planner and Tools
// must never create Kubernetes clients or load kubeconfigs directly; they go
// through this boundary keyed by canonical cluster_id only.
//
// P3.10c-final: a resolved credential is only ever used if the observed
// kube-system identity matches the identity bound to the canonical cluster
// registration. On mismatch the boundary fails closed and never returns a client
// for the wrong physical cluster.
package k8sboundary

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

var secretRefPattern = regexp.MustCompile(`^k8s-secret://([a-z0-9][-a-z0-9]*)/([a-z0-9][-a-z0-9.]*)$`)

// canonicalUUID matches the V9.2 canonical cluster_id form. The boundary rejects
// any non-canonical reference itself rather than relying on the store.
var canonicalUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

var errInvalidCredentialRef = errors.New("invalid credential_ref")
var errSecretNotFound = errors.New("credential secret not found")

// KubeconfigReader resolves a cluster's credential_ref into a kubeconfig.
// The only production implementation is SecretResolver; tests provide fakes.
type KubeconfigReader interface {
	ResolveKubeconfig(credentialRef string) (string, error)
}

// IdentityReader observes the Kubernetes identity (kube-system Namespace
// metadata.uid) for a kubeconfig. Production uses kubectl; tests provide fakes.
type IdentityReader interface {
	ReadKubeSystemUID(kubeconfig string) (string, error)
}

// ClusterStore is the subset of the canonical Cluster Registry the boundary
// needs. Production uses store.ClusterDAO; tests provide fakes.
type ClusterStore interface {
	GetByClusterID(clusterID string) (*store.Cluster, error)
}

// SecretResolver resolves a credential_ref (`k8s-secret://<namespace>/<name>`)
// into the target cluster's kubeconfig by reading the Secret's `kubeconfig` key
// through the admin (management-plane) kubeconfig. It is a low-level package
// capability; callers should normally use ClusterClientManager.GetClient.
type SecretResolver struct {
	adminKubeconfig string // management-plane kubeconfig used to read Secrets
	kubectl         string
}

func NewSecretResolver(adminKubeconfig string) *SecretResolver {
	return &SecretResolver{adminKubeconfig: adminKubeconfig, kubectl: "kubectl"}
}

// ResolveKubeconfig parses credential_ref, reads the Secret's `kubeconfig` key
// via kubectl, and returns the decoded kubeconfig. Fails closed on any error.
func (s *SecretResolver) ResolveKubeconfig(credentialRef string) (string, error) {
	m := secretRefPattern.FindStringSubmatch(strings.TrimSpace(credentialRef))
	if m == nil {
		return "", fmt.Errorf("%w: %q", errInvalidCredentialRef, credentialRef)
	}
	namespace, name := m[1], m[2]

	args := []string{"get", "secret", name, "-n", namespace,
		"-o", "jsonpath={.data.kubeconfig}"}
	out, err := s.runKubectl(args)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errSecretNotFound, err)
	}
	encoded := strings.TrimSpace(out)
	if encoded == "" {
		return "", fmt.Errorf("%w: secret %s/%s has no kubeconfig key", errSecretNotFound, namespace, name)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode kubeconfig from secret: %w", err)
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("%w: empty kubeconfig in %s/%s", errSecretNotFound, namespace, name)
	}
	return string(raw), nil
}

func (s *SecretResolver) runKubectl(args []string) (string, error) {
	base := args
	if s.adminKubeconfig != "" {
		tmp, err := os.CreateTemp("", "admin-kc-*.yaml")
		if err != nil {
			return "", err
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.WriteString(s.adminKubeconfig); err != nil {
			return "", err
		}
		tmp.Close()
		base = append([]string{"--kubeconfig", tmp.Name()}, args...)
	}
	out, err := exec.Command(s.kubectl, base...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", errors.New(string(ee.Stderr))
		}
		return "", err
	}
	return string(out), nil
}

// kubectlIdentityReader reads the kube-system Namespace metadata.uid for a
// kubeconfig through kubectl. This UID is the V1 authoritative Kubernetes
// cluster identity: it is independent of node names, kube-context, API server
// address, and slug.
type kubectlIdentityReader struct {
	kubectl string
}

// NewKubectlIdentityReader returns a production IdentityReader that reads the
// kube-system Namespace metadata.uid via kubectl. Exported so query-api can wire
// the ClusterClientManager without reaching into unexported types.
func NewKubectlIdentityReader() IdentityReader {
	return &kubectlIdentityReader{kubectl: "kubectl"}
}

func (r *kubectlIdentityReader) ReadKubeSystemUID(kubeconfig string) (string, error) {
	tmp, err := os.CreateTemp("", "kc-*.yaml")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(kubeconfig); err != nil {
		return "", err
	}
	tmp.Close()
	out, err := exec.Command(r.kubectl, "--kubeconfig", tmp.Name(),
		"get", "namespace", "kube-system", "-o", "jsonpath={.metadata.uid}").Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", errors.New(string(ee.Stderr))
		}
		return "", err
	}
	uid := strings.TrimSpace(string(out))
	if uid == "" {
		return "", errors.New("kube-system namespace UID is empty")
	}
	return uid, nil
}

// Client is a validated, identity-bound Kubernetes client. The raw kubeconfig is
// held privately; callers use the boundary methods and must never extract it.
type Client struct {
	clusterID   string
	identityUID string
	kubeconfig  string
}

// ClusterID returns the canonical cluster UUID this client is bound to.
func (c *Client) ClusterID() string { return c.clusterID }

// IdentityUID returns the kube-system identity this client was verified against.
func (c *Client) IdentityUID() string { return c.identityUID }

// KubeNodes returns the node names for this validated client.
func (c *Client) KubeNodes() ([]string, error) {
	return kubeNodes(c.kubeconfig)
}

// KubeNodeDetails returns node status/capacity for this validated client.
func (c *Client) KubeNodeDetails() ([]map[string]interface{}, error) {
	return kubeNodeDetails(c.kubeconfig)
}

// KubePods returns pod name/namespace/status/restarts for this validated client.
func (c *Client) KubePods(namespace string) ([]map[string]interface{}, error) {
	return kubePods(c.kubeconfig, namespace)
}

// clientEntry is a validated cache entry. It records the credential_ref it was
// resolved from so a credential change forces re-validation.
type clientEntry struct {
	clusterID    string
	credentialRef string
	identityUID  string
	kubeconfig   string
}

// ClusterClientManager resolves, identity-validates and caches a client per
// canonical cluster_id. The cache key is the immutable canonical UUID; there is
// no cross-cluster reuse. A client is cached only after identity validation
// passes.
type ClusterClientManager struct {
	kubeconfigs KubeconfigReader
	identities  IdentityReader
	clusters    ClusterStore

	mu    sync.Mutex
	cache map[string]*clientEntry
}

// NewClusterClientManager wires the boundary dependencies. Production callers
// pass a SecretResolver, a kubectlIdentityReader and a store.ClusterDAO.
func NewClusterClientManager(kubeconfigs KubeconfigReader, identities IdentityReader, clusters ClusterStore) *ClusterClientManager {
	return &ClusterClientManager{
		kubeconfigs: kubeconfigs,
		identities:  identities,
		clusters:    clusters,
		cache:       make(map[string]*clientEntry),
	}
}

// GetClient returns a validated, identity-bound client for a canonical cluster
// UUID. It resolves the credential_ref from the live Cluster Registry, probes
// the observed kube-system identity and compares it with the identity bound at
// registration. On mismatch or any validation failure it FAILS CLOSED: no client
// is returned and the stale cache entry (if any) is discarded.
func (m *ClusterClientManager) GetClient(clusterID string) (*Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// The boundary only accepts a canonical cluster UUID; there is no slug,
	// name, default, current-context or "all" fallback at this layer.
	if !canonicalUUID.MatchString(clusterID) {
		return nil, store.ErrInvalidClusterRef
	}

	cluster, err := m.clusters.GetByClusterID(clusterID)
	if err != nil {
		return nil, err
	}
	expectedUID := strings.TrimSpace(cluster.KubernetesIdentityUID)
	if expectedUID == "" {
		return nil, fmt.Errorf("%w: cluster_id=%s", store.ErrClusterIdentityMissing, clusterID)
	}

	// Reuse a cached, identity-validated client only if the registry's
	// credential_ref is unchanged. A credential change must invalidate.
	if entry, ok := m.cache[clusterID]; ok && entry.credentialRef == cluster.CredentialRef {
		return &Client{clusterID: clusterID, identityUID: entry.identityUID, kubeconfig: entry.kubeconfig}, nil
	}
	delete(m.cache, clusterID)

	kubeconfig, err := m.kubeconfigs.ResolveKubeconfig(cluster.CredentialRef)
	if err != nil {
		return nil, err
	}

	observedUID, err := m.identities.ReadKubeSystemUID(kubeconfig)
	if err != nil {
		return nil, err
	}

	if observedUID != expectedUID {
		// FAIL CLOSED: the credential reached a physical cluster different from
		// the one bound to this canonical registration. Never cache or return it.
		return nil, fmt.Errorf("%w: expected=%q observed=%q cluster_id=%s",
			store.ErrClusterIdentityMismatch, expectedUID, observedUID, clusterID)
	}

	m.cache[clusterID] = &clientEntry{
		clusterID:    clusterID,
		credentialRef: cluster.CredentialRef,
		identityUID:  observedUID,
		kubeconfig:   kubeconfig,
	}
	return &Client{clusterID: clusterID, identityUID: observedUID, kubeconfig: kubeconfig}, nil
}

// Invalidate drops a cluster's cached client (on credential rotation etc). The
// next GetClient re-resolves and re-validates identity.
func (m *ClusterClientManager) Invalidate(clusterID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cache, clusterID)
}

// getValidatedKubeconfig is the private, package-level way to obtain a
// validated kubeconfig for an execution path. It is intentionally not exported:
// external code must use GetClient instead of handling raw credential material.
func (m *ClusterClientManager) getValidatedKubeconfig(clusterID string) (string, error) {
	client, err := m.GetClient(clusterID)
	if err != nil {
		return "", err
	}
	return client.kubeconfig, nil
}

// kubeNodes returns the node names for a kubeconfig (used to prove canonical
// UUID routing without relying on the current kube-context). It is package
// private: raw kubeconfig must never cross the boundary as a public API.
func kubeNodes(kubeconfig string) ([]string, error) {
	tmp, err := os.CreateTemp("", "kc-*.yaml")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(kubeconfig); err != nil {
		return nil, err
	}
	tmp.Close()
	out, err := exec.Command("kubectl", "--kubeconfig", tmp.Name(), "get", "nodes", "-o", "jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, errors.New(string(ee.Stderr))
		}
		return nil, err
	}
	var names []string
	for _, n := range strings.Fields(string(out)) {
		if n != "" {
			names = append(names, n)
		}
	}
	return names, nil
}

// kubectlJSON 用临时 kubeconfig 执行 kubectl 并以 JSON 返回 stdout。
func kubectlJSON(kubeconfig string, args ...string) ([]byte, error) {
	tmp, err := os.CreateTemp("", "kc-*.yaml")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(kubeconfig); err != nil {
		return nil, err
	}
	tmp.Close()
	base := []string{"--kubeconfig", tmp.Name()}
	base = append(base, args...)
	out, err := exec.Command("kubectl", base...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, errors.New(string(ee.Stderr))
		}
		return nil, err
	}
	return out, nil
}

// kubeNodeDetails 返回节点 Ready 状态与 capacity（cpu/memory/kubelet 版本）。
func kubeNodeDetails(kubeconfig string) ([]map[string]interface{}, error) {
	data, err := kubectlJSON(kubeconfig, "get", "nodes", "-o", "json")
	if err != nil {
		return nil, err
	}
	var r struct {
		Items []struct {
			Metadata struct{ Name string } `json:"metadata"`
			Status   struct {
				Conditions  []struct{ Type, Status string } `json:"conditions"`
				NodeInfo    struct{ KubeletVersion string } `json:"nodeInfo"`
				Capacity    map[string]string               `json:"capacity"`
				Allocatable map[string]string               `json:"allocatable"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	nodes := []map[string]interface{}{}
	for _, it := range r.Items {
		ready := "NotReady"
		for _, c := range it.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" {
				ready = "Ready"
			}
		}
		cpu := it.Status.Capacity["cpu"]
		if cpu == "" {
			cpu = it.Status.Allocatable["cpu"]
		}
		mem := it.Status.Capacity["memory"]
		if mem == "" {
			mem = it.Status.Allocatable["memory"]
		}
		nodes = append(nodes, map[string]interface{}{
			"name": it.Metadata.Name, "status": ready, "cpu": cpu, "memory": mem,
			"version": it.Status.NodeInfo.KubeletVersion,
		})
	}
	return nodes, nil
}

// kubePods 返回 Pod 的 name/namespace/status/restarts（namespace="all" 表示全部命名空间）。
func kubePods(kubeconfig, namespace string) ([]map[string]interface{}, error) {
	args := []string{"get", "pods", "-A", "-o", "json"}
	if namespace != "" && namespace != "all" && namespace != "-A" {
		args = []string{"get", "pods", "-n", namespace, "-o", "json"}
	}
	data, err := kubectlJSON(kubeconfig, args...)
	if err != nil {
		return nil, err
	}
	var r struct {
		Items []struct {
			Metadata struct{ Name, Namespace string } `json:"metadata"`
			Spec     struct{ NodeName string }        `json:"spec"`
			Status   struct {
				Phase             string `json:"phase"`
				ContainerStatuses []struct{ RestartCount int } `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	pods := []map[string]interface{}{}
	for _, it := range r.Items {
		rc := 0
		if len(it.Status.ContainerStatuses) > 0 {
			rc = it.Status.ContainerStatuses[0].RestartCount
		}
		pods = append(pods, map[string]interface{}{
			"name": it.Metadata.Name, "namespace": it.Metadata.Namespace,
			"status": it.Status.Phase, "restarts": rc, "node_name": it.Spec.NodeName,
		})
	}
	return pods, nil
}
