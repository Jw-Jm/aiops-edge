package api

import (
	"context"

	"github.com/observability-platform/ai-apm-query-go/internal/k8sboundary"
	"github.com/observability-platform/ai-apm-query-go/internal/query"
)

// boundaryAccessor 将 query.KubernetesAccessor 适配到既有 k8sboundary.ClusterClientManager。
// 不新建第二套 K8s client，不绕过 credential_ref → Secret → true Kubernetes identity 边界。
type boundaryAccessor struct {
	manager *k8sboundary.ClusterClientManager
}

// Client 解析 canonical cluster_id → 身份校验的 K8s 客户端。
func (a boundaryAccessor) Client(ctx context.Context, clusterID string) (query.KubeClient, error) {
	client, err := a.manager.GetClient(clusterID)
	if err != nil {
		return nil, err
	}
	return boundaryClient{client: client}, nil
}

// boundaryClient 将 k8sboundary.Client 适配为 query.KubeClient（窄只读接口）。
type boundaryClient struct {
	client *k8sboundary.Client
}

func (c boundaryClient) ClusterID() string { return c.client.ClusterID() }

func (c boundaryClient) ListNodeNames() ([]string, error) {
	return c.client.KubeNodes()
}

func (c boundaryClient) ListPods(namespace string) ([]query.KubePod, error) {
	pods, err := c.client.KubePods(namespace)
	if err != nil {
		return nil, err
	}
	result := make([]query.KubePod, 0, len(pods))
	for _, p := range pods {
		restarts, _ := toInt64(p["restarts"])
		result = append(result, query.KubePod{
			Name:      boundaryToString(p["name"]),
			Namespace: boundaryToString(p["namespace"]),
			Status:    boundaryToString(p["status"]),
			Restarts:  restarts,
		})
	}
	return result, nil
}

func (c boundaryClient) GetDeploymentIdentity(namespace, name string) (query.KubeObjectIdentity, error) {
	return c.GetObjectIdentity("deployment", namespace, name)
}

func (c boundaryClient) GetObjectIdentity(resourceType, namespace, name string) (query.KubeObjectIdentity, error) {
	raw, err := c.client.KubeObjectIdentity(resourceType, namespace, name)
	if err != nil {
		return query.KubeObjectIdentity{}, err
	}
	return query.KubeObjectIdentity{
		UID: boundaryToString(raw["uid"]), ResourceVersion: boundaryToString(raw["resource_version"]),
		Namespace: boundaryToString(raw["namespace"]), Name: boundaryToString(raw["name"]),
	}, nil
}

// ListNodeDetails 返回节点 Ready 状态与 capacity（边界只读能力）。
func (c boundaryClient) ListNodeDetails() ([]map[string]interface{}, error) {
	return c.client.KubeNodeDetails()
}

// boundaryToString 安全把边界 JSON 值转为 string。
func boundaryToString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
