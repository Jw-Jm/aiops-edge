import { api } from './client'

/**
 * 资源定位：把 (type, namespace, name) 解析为 canonical ResourceRef（设计规范 §4.3）。
 *
 * 对象链接必须使用 canonical UID，显示名变化不破坏链接。该端点是服务端
 * 唯一权威的定位边界：客户端提交的 tenant/cluster 只是请求参数，
 * 服务端会重建并校验授权范围。
 */

export interface ResolvedResource {
  clusterId: string
  resourceType: string
  namespace: string
  name: string
  uid: string
  domain?: string
  health?: string
}

export interface ResolveRequest {
  clusterId: string
  resourceType: string
  namespace: string
  name: string
}

const TYPE_TO_PATH_SEGMENT: Record<string, string> = {
  pod: 'pods',
  deployment: 'deployments',
  statefulset: 'statefulsets',
  daemonset: 'daemonsets',
  job: 'jobs',
  cronjob: 'cronjobs',
  node: 'nodes',
  k8s_service: 'services',
  service: 'services',
  ingress: 'ingresses',
  pvc: 'persistentvolumeclaims',
  pv: 'persistentvolumes',
}

export function canonicalUidOf(clusterId: string, type: string, namespace: string, name: string): string | null {
  const segment = TYPE_TO_PATH_SEGMENT[type.toLowerCase()]
  if (!segment || !name) return null
  return namespace
    ? `/${clusterId}/${namespace}/${segment}/${name}`
    : `/${clusterId}/${segment}/${name}`
}

/** 服务端定位失败时返回结构化原因，前端不得用本地拼接冒充权威 UID。 */
export async function resolveResource(req: ResolveRequest, signal?: AbortSignal): Promise<ResolvedResource> {
  const res = await api.get<Record<string, unknown>>('/resources/resolve', {
    params: { cluster_id: req.clusterId, type: req.resourceType, namespace: req.namespace, name: req.name },
    signal,
  })
  const d = res.data ?? {}
  return {
    clusterId: String(d.cluster_id ?? req.clusterId),
    resourceType: String(d.resource_type ?? req.resourceType),
    namespace: String(d.namespace ?? req.namespace),
    name: String(d.name ?? req.name),
    uid: String(d.uid ?? d.resource_uid ?? ''),
    ...(d.domain ? { domain: String(d.domain) } : {}),
    ...(d.health ? { health: String(d.health) } : {}),
  }
}
