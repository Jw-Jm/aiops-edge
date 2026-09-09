import type { GraphEntity, GraphEntityType } from '../../api/graphContracts'
import type { PlatformResourceRef, ResourceDomain } from './types'

export const SELECTABLE_RESOURCE_TYPES = {
  compute: ['physical_server', 'k8s_node', 'vm', 'vmi'],
  network: ['switch', 'switch_port', 'nic', 'network', 'nad'],
  storage: ['disk', 'storage_class', 'pv', 'pvc'],
  kubernetes: ['namespace', 'deployment', 'replicaset', 'statefulset', 'daemonset', 'pod', 'container', 'k8s_service', 'endpoint_slice'],
  application: ['business', 'application', 'service', 'middleware'],
} as const satisfies Record<ResourceDomain, readonly GraphEntityType[]>

const TYPE_LABELS: Record<string, string> = {
  physical_server: '物理服务器',
  k8s_node: 'Kubernetes 节点',
  vm: '虚拟机',
  vmi: '虚拟机实例',
  switch: '交换机',
  switch_port: '交换机端口',
  nic: '网卡',
  network: '网络',
  nad: '网络附加定义',
  disk: '磁盘',
  storage_class: '存储类',
  pv: '持久卷',
  pvc: '持久卷声明',
  namespace: 'Namespace',
  deployment: 'Deployment',
  replicaset: 'ReplicaSet',
  statefulset: 'StatefulSet',
  daemonset: 'DaemonSet',
  pod: 'Pod',
  container: '容器',
  k8s_service: 'Kubernetes Service',
  endpoint_slice: 'EndpointSlice',
  business: '业务',
  application: '应用',
  service: '应用服务',
  middleware: '中间件',
}

const DOMAIN_BY_TYPE = new Map<string, ResourceDomain>(
  Object.entries(SELECTABLE_RESOURCE_TYPES).flatMap(([domain, types]) => types.map((type) => [type, domain as ResourceDomain])),
)

export function resourceDomainOf(type: GraphEntityType): ResourceDomain | null {
  return DOMAIN_BY_TYPE.get(type) ?? null
}

export function isSelectableResourceType(type: GraphEntityType): boolean {
  return resourceDomainOf(type) !== null
}

export function resourceTypeLabel(type: GraphEntityType): string {
  return TYPE_LABELS[type] ?? '未识别资源'
}

export function resourceLocation(resource: PlatformResourceRef): string {
  return resource.namespace ? `${resource.namespace} / ${resource.name}` : resource.name
}

export function toPlatformResourceRef(entity: GraphEntity, activeClusterId: string): PlatformResourceRef {
  if (entity.cluster_id !== activeClusterId) {
    throw new Error('跨集群资源不能进入当前范围')
  }
  const domain = resourceDomainOf(entity.entity_type)
  if (!domain) {
    throw new Error(`不可选择的资源类型: ${entity.entity_type}`)
  }
  const resource: PlatformResourceRef = {
    clusterId: entity.cluster_id,
    uid: entity.entity_uid,
    type: entity.entity_type,
    domain,
    name: entity.name,
  }
  if (domain === 'kubernetes' && entity.namespace) resource.namespace = entity.namespace
  return resource
}
