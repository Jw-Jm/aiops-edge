package k8sboundary

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ── Test doubles ──────────────────────────────────────────────────────────────

type fakeClusterStore struct {
	clusters map[string]*store.Cluster
	errByID  map[string]error
}

func (f *fakeClusterStore) GetByClusterID(clusterID string) (*store.Cluster, error) {
	if e, ok := f.errByID[clusterID]; ok {
		return nil, e
	}
	c, ok := f.clusters[clusterID]
	if !ok {
		return nil, store.ErrClusterNotFound
	}
	return c, nil
}

type fakeKubeconfigReader struct {
	kcByRef  map[string]string
	errByRef map[string]error
}

func (f *fakeKubeconfigReader) ResolveKubeconfig(credentialRef string) (string, error) {
	if e, ok := f.errByRef[credentialRef]; ok {
		return "", e
	}
	kc, ok := f.kcByRef[credentialRef]
	if !ok {
		return "", errSecretNotFound
	}
	return kc, nil
}

type fakeIdentityReader struct {
	uidByKubeconfig map[string]string
}

func (f *fakeIdentityReader) ReadKubeSystemUID(kubeconfig string) (string, error) {
	uid, ok := f.uidByKubeconfig[kubeconfig]
	if !ok {
		return "", errors.New("identity probe failed")
	}
	return uid, nil
}

// ── Fixtures ─────────────────────────────────────────────────────────────────

const (
	uuidA = "11111111-1111-4111-8111-111111111111"
	uuidB = "22222222-2222-4222-8222-222222222222"

	secretA = "k8s-secret://aiops/cluster-a"
	secretB = "k8s-secret://aiops/cluster-b"

	kcA = "kubeconfig-A"
	kcB = "kubeconfig-B"

	uidA = "uid-kube-system-orbstack"
	uidB = "uid-kube-system-kind02"
)

// newBoundary wires the three production dependencies plus a fake store so the
// boundary is exercised entirely in-process (no kubectl / Kubernetes API).
func newBoundary(t *testing.T, store *fakeClusterStore) (*ClusterClientManager, *fakeKubeconfigReader, *fakeIdentityReader) {
	t.Helper()
	kcreader := &fakeKubeconfigReader{kcByRef: map[string]string{
		secretA: kcA,
		secretB: kcB,
	}}
	ireader := &fakeIdentityReader{uidByKubeconfig: map[string]string{
		kcA: uidA,
		kcB: uidB,
	}}
	return NewClusterClientManager(kcreader, ireader, store), kcreader, ireader
}

func baseStore() *fakeClusterStore {
	return &fakeClusterStore{clusters: map[string]*store.Cluster{
		uuidA: {ClusterID: uuidA, Slug: "orbstack", CredentialRef: secretA, KubernetesIdentityUID: uidA, Status: "ready"},
		uuidB: {ClusterID: uuidB, Slug: "aiops-kind-02", CredentialRef: secretB, KubernetesIdentityUID: uidB, Status: "ready"},
	}}
}

func TestKubeGraphResourceSetIncludesKubeVirtDependencySources(t *testing.T) {
	resources := kubeGraphResourceSet()
	want := map[string]bool{"data_volumes": false, "nads": false, "pvcs": false, "pvs": false, "virtual_machine_instances": false}
	for _, resource := range resources {
		if _, ok := want[resource.field]; ok {
			want[resource.field] = true
		}
	}
	for field, found := range want {
		if !found {
			t.Fatalf("graph resource set missing %q", field)
		}
	}
}

// ── Tests ────────────────────────────────────────────────────────────────────

// 1. UUID_A + Secret_A → identity A → GetClient PASS
// 2. UUID_B + Secret_B → identity B → GetClient PASS
func TestGetClientPassesWhenResolvedIdentityMatchesBinding(t *testing.T) {
	mgr, _, _ := newBoundary(t, baseStore())
	for _, tc := range []struct {
		id  string
		uid string
	}{
		{uuidA, uidA},
		{uuidB, uidB},
	} {
		t.Run(tc.id, func(t *testing.T) {
			client, err := mgr.GetClient(tc.id)
			if err != nil {
				t.Fatalf("GetClient(%s) error = %v, want nil", tc.id, err)
			}
			if client == nil {
				t.Fatalf("GetClient(%s) client = nil, want non-nil", tc.id)
			}
			if client.IdentityUID() != tc.uid {
				t.Fatalf("GetClient(%s) identity = %q, want %q", tc.id, client.IdentityUID(), tc.uid)
			}
		})
	}
}

// 3. UUID_A + missing Secret → fail closed
func TestGetClientFailsClosedOnMissingSecret(t *testing.T) {
	s := baseStore()
	s.clusters[uuidA] = &store.Cluster{ClusterID: uuidA, Slug: "orbstack", CredentialRef: "k8s-secret://aiops/missing", KubernetesIdentityUID: uidA, Status: "ready"}
	mgr, _, _ := newBoundary(t, s)
	client, err := mgr.GetClient(uuidA)
	if client != nil {
		t.Fatalf("GetClient(missing secret) client = %v, want nil", client)
	}
	if err == nil {
		t.Fatal("GetClient(missing secret) error = nil, want fail-closed error")
	}
	if !errors.Is(err, errSecretNotFound) {
		t.Fatalf("GetClient(missing secret) error = %v, want errSecretNotFound", err)
	}
}

// 4/5. UUID_A + Secret_B → CLUSTER_IDENTITY_MISMATCH, and no client is returned
func TestGetClientFailsClosedOnIdentityMismatch(t *testing.T) {
	// Cluster A's credential_ref is misconfigured to point at cluster B's Secret.
	s := baseStore()
	s.clusters[uuidA] = &store.Cluster{ClusterID: uuidA, Slug: "orbstack", CredentialRef: secretB, KubernetesIdentityUID: uidA, Status: "ready"}
	mgr, _, _ := newBoundary(t, s)

	client, err := mgr.GetClient(uuidA)
	if client != nil {
		t.Fatalf("GetClient(mismatch) client = %v, want nil", client)
	}
	if !errors.Is(err, store.ErrClusterIdentityMismatch) {
		t.Fatalf("GetClient(mismatch) error = %v, want ErrClusterIdentityMismatch", err)
	}
	// The error must never be a plain "connection reached a node" style success.
	if err == nil || strings.Contains(err.Error(), "kubeconfig-B") {
		t.Fatalf("GetClient(mismatch) must not leak the wrong credential: %v", err)
	}
}

// 6. mismatch is not cached: a second call re-validates and still fails closed
func TestGetClientMismatchIsNotCached(t *testing.T) {
	s := baseStore()
	s.clusters[uuidA] = &store.Cluster{ClusterID: uuidA, Slug: "orbstack", CredentialRef: secretB, KubernetesIdentityUID: uidA, Status: "ready"}
	mgr, _, _ := newBoundary(t, s)

	for i := 0; i < 2; i++ {
		client, err := mgr.GetClient(uuidA)
		if client != nil {
			t.Fatalf("attempt %d: mismatch client = %v, want nil", i, client)
		}
		if !errors.Is(err, store.ErrClusterIdentityMismatch) {
			t.Fatalf("attempt %d: error = %v, want ErrClusterIdentityMismatch", i, err)
		}
	}
}

// 7. cached valid client for A is invalidated when credential_ref changes to B
func TestGetClientInvalidatesCacheOnCredentialChange(t *testing.T) {
	s := baseStore()
	mgr, _, _ := newBoundary(t, s)

	client, err := mgr.GetClient(uuidA)
	if err != nil || client == nil {
		t.Fatalf("initial GetClient error = %v, client=%v, want valid", err, client)
	}

	// Misconfigure the registry: A now points at B's Secret.
	s.clusters[uuidA] = &store.Cluster{ClusterID: uuidA, Slug: "orbstack", CredentialRef: secretB, KubernetesIdentityUID: uidA, Status: "ready"}

	client2, err := mgr.GetClient(uuidA)
	if client2 != nil {
		t.Fatalf("after credential change, client = %v, want nil (stale cache must not be reused)", client2)
	}
	if !errors.Is(err, store.ErrClusterIdentityMismatch) {
		t.Fatalf("after credential change, error = %v, want ErrClusterIdentityMismatch", err)
	}
}

// 8. after credential is fixed back to A, validation recovers
func TestGetClientRecoversAfterCredentialFixed(t *testing.T) {
	s := baseStore()
	mgr, _, _ := newBoundary(t, s)

	// Introduce a transient misconfiguration, then fix it.
	s.clusters[uuidA] = &store.Cluster{ClusterID: uuidA, Slug: "orbstack", CredentialRef: secretB, KubernetesIdentityUID: uidA, Status: "ready"}
	if c, err := mgr.GetClient(uuidA); c != nil || !errors.Is(err, store.ErrClusterIdentityMismatch) {
		t.Fatalf("expected mismatch while misconfigured, got client=%v err=%v", c, err)
	}

	s.clusters[uuidA] = &store.Cluster{ClusterID: uuidA, Slug: "orbstack", CredentialRef: secretA, KubernetesIdentityUID: uidA, Status: "ready"}
	client, err := mgr.GetClient(uuidA)
	if err != nil || client == nil {
		t.Fatalf("after fix, error = %v, client=%v, want valid", err, client)
	}
	if client.IdentityUID() != uidA {
		t.Fatalf("recovered client identity = %q, want %q", client.IdentityUID(), uidA)
	}
}

// 9. registry record without kubernetes_identity_uid → fail closed (never skip check)
func TestGetClientFailsClosedWhenBindingMissing(t *testing.T) {
	s := baseStore()
	s.clusters[uuidA] = &store.Cluster{ClusterID: uuidA, Slug: "orbstack", CredentialRef: secretA, KubernetesIdentityUID: "", Status: "ready"}
	mgr, _, _ := newBoundary(t, s)

	client, err := mgr.GetClient(uuidA)
	if client != nil {
		t.Fatalf("GetClient(missing binding) client = %v, want nil", client)
	}
	if !errors.Is(err, store.ErrClusterIdentityMissing) {
		t.Fatalf("GetClient(missing binding) error = %v, want ErrClusterIdentityMissing", err)
	}
}

// 10. only canonical UUID is accepted; no default/current-context/all fallback
func TestGetClientRejectsNonCanonicalClusterRef(t *testing.T) {
	mgr, _, _ := newBoundary(t, baseStore())
	for _, ref := range []string{"all", "default", "1", "", "orbstack"} {
		client, err := mgr.GetClient(ref)
		if client != nil {
			t.Fatalf("GetClient(%q) client = %v, want nil", ref, client)
		}
		if !errors.Is(err, store.ErrInvalidClusterRef) {
			t.Fatalf("GetClient(%q) error = %v, want ErrInvalidClusterRef", ref, err)
		}
	}
}

// fakeKubectl 在临时 PATH 中放置一个假 kubectl，记录调用参数与收到的 kubeconfig。
// 它证明 KubeEvents 复用的是客户端已校验的 kubeconfig，而不是进程默认/current context。
func fakeKubectl(t *testing.T, stdout string) (argsLog, kubeconfigSeen string) {
	t.Helper()
	dir := t.TempDir()
	argsLog = filepath.Join(dir, "args.log")
	kubeconfigSeen = filepath.Join(dir, "kubeconfig.seen")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$KUBECTL_TEST_ARGS"
prev=""
for a in "$@"; do
  if [ "$prev" = "--kubeconfig" ]; then cat "$a" > "$KUBECTL_TEST_KC"; fi
  prev="$a"
done
printf '%s' "$KUBECTL_TEST_STDOUT"
`
	bin := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}
	t.Setenv("KUBECTL_TEST_ARGS", argsLog)
	t.Setenv("KUBECTL_TEST_KC", kubeconfigSeen)
	t.Setenv("KUBECTL_TEST_STDOUT", stdout)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argsLog, kubeconfigSeen
}

// 11. KubeEvents 只使用该客户端已校验的 kubeconfig，并返回解析后的 Warning 事件。
func TestClientKubeEventsUsesBoundKubeconfig(t *testing.T) {
	const eventsJSON = `{"items":[{"type":"Warning","reason":"FailedScheduling","message":"no nodes available","count":2,"involvedObject":{"kind":"VirtualMachineInstance","name":"shared-vm"}},{"type":"Normal","reason":"Started","message":"ok","count":1,"involvedObject":{"kind":"VirtualMachineInstance","name":"shared-vm"}}]}`
	argsLog, kubeconfigSeen := fakeKubectl(t, eventsJSON)

	mgr, _, _ := newBoundary(t, baseStore())
	client, err := mgr.GetClient(uuidA)
	if err != nil || client == nil {
		t.Fatalf("GetClient(%s) = %v, %v", uuidA, client, err)
	}
	events, err := client.KubeEvents()
	if err != nil {
		t.Fatalf("KubeEvents: %v", err)
	}
	if len(events) != 1 || events[0]["reason"] != "FailedScheduling" {
		t.Fatalf("events = %#v, want only the Warning event", events)
	}
	seen, err := os.ReadFile(kubeconfigSeen)
	if err != nil {
		t.Fatalf("fake kubectl did not receive a kubeconfig: %v", err)
	}
	if string(seen) != kcA {
		t.Fatalf("KubeEvents used kubeconfig %q, want the client-bound %q", string(seen), kcA)
	}
	args, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatalf("fake kubectl did not log args: %v", err)
	}
	if !strings.Contains(string(args), "get events -A -o json") {
		t.Fatalf("unexpected kubectl args: %s", string(args))
	}
}
