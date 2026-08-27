package query

import (
	"context"
	"regexp"
	"strings"
)

// KubernetesScope 是 kubernetes 资源域的作用域（租户）。
type KubernetesScope struct {
	TenantID  string
	ClusterID string
}

// KubePod 一条 K8s Pod 资源（typed 视图字段）。
type KubePod struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Status    string `json:"status"`
	Restarts  int64  `json:"restarts"`
}

// KubeObjectIdentity is the minimum immutable target identity needed for an
// Action preflight and later TOCTOU check. The raw object is intentionally not
// exposed through the public query response.
type KubeObjectIdentity struct {
	UID             string
	ResourceVersion string
	Namespace       string
	Name            string
	Observed        []byte
}

// KubeClient 是绑定身份校验的 K8s 只读访问器（窄接口，包装 k8sboundary.Client）。
// 底层为冻结的 K8s Access Boundary；此处不新建第二套 client。
type KubeClient interface {
	ClusterID() string
	ListNodeNames() ([]string, error)
	ListNodeDetails() ([]map[string]interface{}, error)
	ListPods(namespace string) ([]KubePod, error)
	GetDeploymentIdentity(namespace, name string) (KubeObjectIdentity, error)
}

// KubeTargetIdentityClient is the optional extension used by the canonical
// Action preflight path. Keeping it separate preserves existing read-only
// clients while requiring the production boundary adapter to expose only
// immutable target identity, never credentials or a full unbounded object.
type KubeTargetIdentityClient interface {
	GetObjectIdentity(resourceType, namespace, name string) (KubeObjectIdentity, error)
}

// KubernetesAccessor 解析 canonical cluster_id → 校验身份的 K8s 客户端。
// 生产实现包装 k8sboundary.ClusterClientManager；测试提供 fake。
type KubernetesAccessor interface {
	Client(ctx context.Context, clusterID string) (KubeClient, error)
}

// canonicalUUID 复刻 k8sboundary 的 canonical cluster_id 校验，保证只接受规范 UUID。
var canonicalUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// KubernetesRepository 是 kubernetes 资源域的 domain repository（V9.2 Phase 6 P6.2d）。
// 职责：以 canonical cluster_id 通过既有 K8s Access Boundary 查询目标集群，
// 将边界错误映射为统一 QueryError 语义；handler 不再直接承担 API URL/资源查询/错误映射。
type KubernetesRepository struct {
	accessor KubernetesAccessor
}

// NewKubernetesRepository 构造 kubernetes repository，注入访问边界 accessor。
func NewKubernetesRepository(accessor KubernetesAccessor) *KubernetesRepository {
	return &KubernetesRepository{accessor: accessor}
}

// clientFor 解析并校验 canonical cluster_id，返回身份绑定 client。
// 非法 cluster_id → permission_denied（与边界 fail-closed 一致）。
func (r *KubernetesRepository) clientFor(ctx context.Context, clusterID string) (KubeClient, error) {
	if !canonicalUUID.MatchString(clusterID) {
		return nil, PermissionDenied("kubernetes: invalid canonical cluster reference")
	}
	if r.accessor == nil {
		return nil, Unavailable("kubernetes: access boundary not configured")
	}
	client, err := r.accessor.Client(ctx, clusterID)
	if err != nil {
		return nil, mapKubeBoundaryError(err)
	}
	return client, nil
}

// mapKubeBoundaryError 将 K8s Access Boundary 错误映射为统一 QueryError 语义：
//   - 身份不匹配/不可达/非法引用 → permission_denied
//   - 边界/后端故障 → unavailable
//
// 边界已 fail-closed，不产生超时（kubectl 超时由边界控制），超时保留供未来扩展。
func mapKubeBoundaryError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "identity mismatch"),
		strings.Contains(msg, "invalid credential"),
		strings.Contains(msg, "invalid canonical"),
		strings.Contains(msg, "identity missing"),
		strings.Contains(msg, "invalid cluster ref"):
		return PermissionDenied("kubernetes: " + msg)
	default:
		return Unavailable("kubernetes: " + msg)
	}
}

// ListNodeNames 返回目标集群节点名列表。空 → no_data。
func (r *KubernetesRepository) ListNodeNames(ctx context.Context, scope KubernetesScope, clusterID string) ([]string, error) {
	client, err := r.clientFor(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	nodes, err := client.ListNodeNames()
	if err != nil {
		return nil, mapKubeBoundaryError(err)
	}
	if len(nodes) == 0 {
		return nil, NoData()
	}
	return nodes, nil
}

// GetDeploymentIdentity resolves only the target identity through the existing
// cluster-bound Access Boundary. It is used by Action preflight and never
// returns credentials or an unbounded object list.
func (r *KubernetesRepository) GetDeploymentIdentity(ctx context.Context, scope KubernetesScope, clusterID, namespace, name string) (KubeObjectIdentity, error) {
	if strings.TrimSpace(namespace) == "" || strings.TrimSpace(name) == "" {
		return KubeObjectIdentity{}, PermissionDenied("kubernetes: namespace and deployment name are required")
	}
	client, err := r.clientFor(ctx, clusterID)
	if err != nil {
		return KubeObjectIdentity{}, err
	}
	identity, err := client.GetDeploymentIdentity(namespace, name)
	if err != nil {
		return KubeObjectIdentity{}, mapKubeBoundaryError(err)
	}
	if identity.UID == "" || identity.ResourceVersion == "" {
		return KubeObjectIdentity{}, Unavailable("kubernetes: target identity incomplete")
	}
	return identity, nil
}

// GetObjectIdentity resolves a workload, Pod, or Node identity through the
// existing canonical cluster boundary. It is the only repository entry point
// used by the multi-kind Action preflight flow.
func (r *KubernetesRepository) GetObjectIdentity(ctx context.Context, scope KubernetesScope, clusterID, resourceType, namespace, name string) (KubeObjectIdentity, error) {
	resourceType = strings.ToLower(strings.TrimSpace(resourceType))
	if resourceType != "node" && strings.TrimSpace(namespace) == "" {
		return KubeObjectIdentity{}, PermissionDenied("kubernetes: namespace is required for namespaced target")
	}
	if strings.TrimSpace(name) == "" {
		return KubeObjectIdentity{}, PermissionDenied("kubernetes: target name is required")
	}
	if resourceType != "deployment" && resourceType != "statefulset" && resourceType != "daemonset" && resourceType != "pod" && resourceType != "node" {
		return KubeObjectIdentity{}, PermissionDenied("kubernetes: unsupported target resource type")
	}
	client, err := r.clientFor(ctx, clusterID)
	if err != nil {
		return KubeObjectIdentity{}, err
	}
	identityClient, ok := client.(KubeTargetIdentityClient)
	if !ok {
		return KubeObjectIdentity{}, Unavailable("kubernetes: target identity capability not configured")
	}
	identity, err := identityClient.GetObjectIdentity(resourceType, namespace, name)
	if err != nil {
		return KubeObjectIdentity{}, mapKubeBoundaryError(err)
	}
	if identity.UID == "" || identity.ResourceVersion == "" {
		return KubeObjectIdentity{}, Unavailable("kubernetes: target identity incomplete")
	}
	return identity, nil
}

// ListNodeDetails 返回节点 Ready 状态与 capacity（供健康评估）。
func (r *KubernetesRepository) ListNodeDetails(ctx context.Context, scope KubernetesScope, clusterID string) ([]map[string]interface{}, error) {
	client, err := r.clientFor(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	details, err := client.ListNodeDetails()
	if err != nil {
		return nil, mapKubeBoundaryError(err)
	}
	return details, nil
}

// ListPods 返回目标集群（可选命名空间过滤）的 Pod 列表。空 → no_data。
func (r *KubernetesRepository) ListPods(ctx context.Context, scope KubernetesScope, clusterID, namespace string) ([]KubePod, error) {
	client, err := r.clientFor(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	pods, err := client.ListPods(namespace)
	if err != nil {
		return nil, mapKubeBoundaryError(err)
	}
	if len(pods) == 0 {
		return nil, NoData()
	}
	return pods, nil
}
