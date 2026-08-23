package k8sboundary

import (
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ClusterRegistration is the request a caller submits to register a Kubernetes
// cluster. It deliberately has NO KubernetesIdentityUID field: the authoritative
// identity is discovered by the boundary from the real target cluster, so a
// caller cannot forge the binding.
type ClusterRegistration struct {
	TenantID       string
	Slug           string
	Name           string
	Environment    string
	Region         string
	CredentialRef  string
	Type           string
	Capabilities   string
	Labels         string
}

// ClusterRegistrationStore is the subset of the Cluster Registry the registrar
// needs. Production uses store.ClusterDAO; tests provide fakes.
type ClusterRegistrationStore interface {
	RegisterCluster(c *store.Cluster) error
	FindActiveByKubeSystemUID(uid string) (*store.Cluster, error)
}

// ClusterRegistrar is the production path for registering a Kubernetes cluster
// into the canonical Cluster Registry. It encapsulates the P3.10c-final identity
// binding contract:
//
//	credential_ref → SecretResolver → kubeconfig
//	→ target Kubernetes API → kube-system Namespace metadata.uid
//	→ persist kubernetes_identity_uid (never caller-supplied)
//
// Registration fails closed: the credential is validated and the identity
// observed BEFORE any DB write, and duplicate ACTIVE registration of the same
// physical cluster is rejected.
type ClusterRegistrar struct {
	kubeconfigs KubeconfigReader
	identities  IdentityReader
	dao         ClusterRegistrationStore
}

// NewClusterRegistrar wires the registrar. Production callers pass a
// SecretResolver, a kubectlIdentityReader and a store.ClusterDAO.
func NewClusterRegistrar(kubeconfigs KubeconfigReader, identities IdentityReader, dao ClusterRegistrationStore) *ClusterRegistrar {
	return &ClusterRegistrar{kubeconfigs: kubeconfigs, identities: identities, dao: dao}
}

// Register resolves the credential, discovers the authoritative kube-system
// identity from the real target cluster, generates a fresh canonical UUID and
// persists the cluster with the service-discovered identity. The caller cannot
// influence kubernetes_identity_uid.
func (r *ClusterRegistrar) Register(reg ClusterRegistration) (*store.Cluster, error) {
	// 1. Resolve and validate the credential up-front (fail before any write).
	kubeconfig, err := r.kubeconfigs.ResolveKubeconfig(reg.CredentialRef)
	if err != nil {
		return nil, fmt.Errorf("register: resolve credential: %w", err)
	}

	// 2. Discover the authoritative identity from the real target cluster.
	observedUID, err := r.identities.ReadKubeSystemUID(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("register: discover kube-system identity: %w", err)
	}
	if observedUID == "" {
		return nil, errors.New("register: discovered kube-system identity is empty")
	}

	// 3. Generate a fresh canonical UUID (never derived from the UID).
	clusterID, err := newCanonicalUUID()
	if err != nil {
		return nil, fmt.Errorf("register: generate canonical uuid: %w", err)
	}

	// 4. Persist with the service-discovered identity. RegisterCluster also
	// rejects duplicate ACTIVE registration of the same physical cluster.
	cluster := &store.Cluster{
		ClusterID:             clusterID,
		TenantID:              reg.TenantID,
		Slug:                  reg.Slug,
		Name:                  reg.Name,
		Environment:           reg.Environment,
		Region:                reg.Region,
		CredentialRef:         reg.CredentialRef,
		Status:                "ready",
		Type:                  reg.Type,
		Capabilities:          reg.Capabilities,
		Labels:                reg.Labels,
		KubernetesIdentityUID: observedUID,
	}
	if err := r.dao.RegisterCluster(cluster); err != nil {
		return nil, fmt.Errorf("register: persist: %w", err)
	}
	return cluster, nil
}

// newCanonicalUUID returns a lowercase random UUID v4 that satisfies the V9.2
// canonical cluster_id pattern (version 4, RFC 4122 variant 8/9/a/b).
func newCanonicalUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10xx
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
