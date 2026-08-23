//go:build integration

package k8sboundary

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// These tests run against two real local clusters (orbstack and
// kind-aiops-kind-02) using the real SecretResolver and kubectlIdentityReader.
// They are gated behind the "integration" build tag and require kubectl with both
// contexts. They prove the production boundary actively fails closed on
// credential → cluster identity mismatch.
//
// Run: go test -tags integration ./internal/k8sboundary/ -run Integration -v

const e2eNS = "aiops"

var (
	e2eUUIDA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	e2eUUIDB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

type e2eClusterStore struct {
	byID map[string]*store.Cluster
}

func (s *e2eClusterStore) GetByClusterID(clusterID string) (*store.Cluster, error) {
	c, ok := s.byID[clusterID]
	if !ok {
		return nil, store.ErrClusterNotFound
	}
	return c, nil
}

// TestIntegrationTwoClusterMismatchFailClosed is the real negative E2E:
//
//	UUID_A + Secret_A(orbstack)   → observed orbstack UID → PASS
//	UUID_B + Secret_B(kind-02)    → observed kind-02 UID  → PASS
//	UUID_A + Secret_B(kind-02)    → observed kind-02 UID ≠ orbstack binding
//	                              → CLUSTER_IDENTITY_MISMATCH → client == nil → cache unchanged
func TestIntegrationTwoClusterMismatchFailClosed(t *testing.T) {
	adminKC, ok := realKubeconfig(t, "kind-aiops-kind-02")
	if !ok {
		t.Skip("kind-aiops-kind-02 context unavailable")
	}
	orbKC, ok := realKubeconfig(t, "orbstack")
	if !ok {
		t.Skip("orbstack context unavailable")
	}
	kindKC := adminKC

	orbUID, ok := realKubeSystemUID(t, "orbstack")
	if !ok {
		t.Skip("cannot read orbstack kube-system UID")
	}
	kindUID, ok := realKubeSystemUID(t, "kind-aiops-kind-02")
	if !ok {
		t.Skip("cannot read kind-02 kube-system UID")
	}
	if orbUID == kindUID {
		t.Fatalf("test requires two distinct clusters, got identical UID %q", orbUID)
	}

	setupE2ESecrets(t, adminKC, orbKC, kindKC)
	defer teardownE2ESecrets(t, adminKC)

	resolver := NewSecretResolver(adminKC)
	reader := &kubectlIdentityReader{kubectl: "kubectl"}

	// 1/2. real resolution + real identity discovery
	orbResolved, err := resolver.ResolveKubeconfig("k8s-secret://aiops/cluster-a")
	if err != nil {
		t.Fatalf("ResolveKubeconfig(secretA=orbstack) error = %v", err)
	}
	if got, err := reader.ReadKubeSystemUID(orbResolved); err != nil || got != orbUID {
		t.Fatalf("observed orbstack UID = %q (err=%v), want %q", got, err, orbUID)
	}
	kindResolved, err := resolver.ResolveKubeconfig("k8s-secret://aiops/cluster-b")
	if err != nil {
		t.Fatalf("ResolveKubeconfig(secretB=kind02) error = %v", err)
	}
	if got, err := reader.ReadKubeSystemUID(kindResolved); err != nil || got != kindUID {
		t.Fatalf("observed kind-02 UID = %q (err=%v), want %q", got, err, kindUID)
	}

	// 2. boundary fail-closed with real readers
	s := &e2eClusterStore{byID: map[string]*store.Cluster{
		e2eUUIDA: {ClusterID: e2eUUIDA, Slug: "orbstack", CredentialRef: "k8s-secret://aiops/cluster-a", KubernetesIdentityUID: orbUID, Status: "ready"},
		e2eUUIDB: {ClusterID: e2eUUIDB, Slug: "aiops-kind-02", CredentialRef: "k8s-secret://aiops/cluster-b", KubernetesIdentityUID: kindUID, Status: "ready"},
	}}
	mgr := NewClusterClientManager(resolver, reader, s)

	if c, err := mgr.GetClient(e2eUUIDA); err != nil || c == nil || c.IdentityUID() != orbUID {
		t.Fatalf("GetClient(uuidA) matching binding: client=%v err=%v", c, err)
	}
	if c, err := mgr.GetClient(e2eUUIDB); err != nil || c == nil || c.IdentityUID() != kindUID {
		t.Fatalf("GetClient(uuidB) matching binding: client=%v err=%v", c, err)
	}

	// Misconfigure uuidA's credential to point at cluster B's Secret.
	s.byID[e2eUUIDA] = &store.Cluster{ClusterID: e2eUUIDA, Slug: "orbstack", CredentialRef: "k8s-secret://aiops/cluster-b", KubernetesIdentityUID: orbUID, Status: "ready"}
	client, err := mgr.GetClient(e2eUUIDA)
	if client != nil {
		t.Fatalf("GetClient(uuidA) with secretB returned a client, want nil (fail closed)")
	}
	if !errors.Is(err, store.ErrClusterIdentityMismatch) {
		t.Fatalf("GetClient(uuidA) with secretB error = %v, want ErrClusterIdentityMismatch", err)
	}
	// cache must be unchanged: re-validation still fails closed.
	if client, err := mgr.GetClient(e2eUUIDA); client != nil || !errors.Is(err, store.ErrClusterIdentityMismatch) {
		t.Fatalf("mismatch was cached: client=%v err=%v, want fail closed again", client, err)
	}
}

func realKubeconfig(t *testing.T, ctx string) (string, bool) {
	t.Helper()
	out, err := exec.Command("kubectl", "config", "view", "--context", ctx, "--minify", "--raw").Output()
	if err != nil || len(out) == 0 {
		return "", false
	}
	return string(out), true
}

func realKubeSystemUID(t *testing.T, ctx string) (string, bool) {
	t.Helper()
	out, err := exec.Command("kubectl", "--context", ctx, "get", "ns", "kube-system", "-o", "jsonpath={.metadata.uid}").Output()
	if err != nil {
		return "", false
	}
	uid := strings.TrimSpace(string(out))
	return uid, uid != ""
}

func setupE2ESecrets(t *testing.T, adminKC, orbKC, kindKC string) {
	t.Helper()
	apply := func(yaml string) {
		f, err := os.CreateTemp("", "k8s-e2e-*.yaml")
		if err != nil {
			t.Fatalf("create temp: %v", err)
		}
		defer os.Remove(f.Name())
		if _, err := f.WriteString(yaml); err != nil {
			t.Fatalf("write temp: %v", err)
		}
		f.Close()
		af, err := os.CreateTemp("", "admin-*.yaml")
		if err != nil {
			t.Fatalf("admin temp: %v", err)
		}
		defer os.Remove(af.Name())
		af.WriteString(adminKC)
		af.Close()
		if out, err := exec.Command("kubectl", "--kubeconfig", af.Name(), "apply", "-f", f.Name()).CombinedOutput(); err != nil {
			t.Fatalf("apply failed: %v\n%s", err, out)
		}
	}
	apply(fmt.Sprintf("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: %s\n", e2eNS))
	for name, kc := range map[string]string{"cluster-a": orbKC, "cluster-b": kindKC} {
		apply(fmt.Sprintf("apiVersion: v1\nkind: Secret\nmetadata:\n  name: %s\n  namespace: %s\ntype: Opaque\ndata:\n  kubeconfig: %s\n",
			name, e2eNS, base64.StdEncoding.EncodeToString([]byte(kc))))
	}
}

func teardownE2ESecrets(t *testing.T, adminKC string) {
	t.Helper()
	af, err := os.CreateTemp("", "admin-*.yaml")
	if err != nil {
		return
	}
	defer os.Remove(af.Name())
	af.WriteString(adminKC)
	af.Close()
	exec.Command("kubectl", "--kubeconfig", af.Name(), "delete", "namespace", e2eNS, "--ignore-not-found=true").Run()
}
