import type { ResourceCatalogItem, ResourceDetailResponse } from '../../api/resources'
import { formatStructuredValue } from '../../lib/structuredDisplay'

export interface DetailField {
  key: string
  label: string
  value: string
}

export interface DetailSection {
  key: string
  title: string
  fields: DetailField[]
}

export interface ResourceRef {
  uid?: string
  type?: string
  name: string
  namespace?: string
}

export interface VmDiskDependency {
  deviceName: string
  bus?: string
  volumeName: string
  dataVolume?: ResourceRef
  pvc?: ResourceRef
  pv?: ResourceRef
  storageClass?: ResourceRef
}

export interface VmNetworkDependency {
  interfaceName: string
  binding: string
  defaultPodNetwork: boolean
  nad?: ResourceRef
  cni?: ResourceRef
  network?: ResourceRef
}

export interface VmDependencies {
  disks: VmDiskDependency[]
  networks: VmNetworkDependency[]
}

function display(value: unknown): string {
  if (value === undefined || value === null || value === '') return '未提供'
  if (typeof value === 'boolean') return value ? '是' : '否'
  if (Array.isArray(value)) return value.length ? value.map(display).join('、') : '无'
  if (typeof value === 'object') return formatStructuredValue(value)
  return String(value)
}

function field(key: string, label: string, value: unknown): DetailField {
  return { key, label, value: display(value) }
}

function ref(value: unknown): ResourceRef | undefined {
  if (!value || typeof value !== 'object') return undefined
  const item = value as Record<string, unknown>
  const name = typeof item.name === 'string' ? item.name : ''
  return name ? { name, ...(typeof item.uid === 'string' ? { uid: item.uid } : {}), ...(typeof item.type === 'string' ? { type: item.type } : {}), ...(typeof item.namespace === 'string' ? { namespace: item.namespace } : {}) } : undefined
}

function stringValue(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value.trim() : undefined
}

/** Converts the typed backend dependency DTO without inventing missing facts. */
export function projectVmDependencies(attributes?: Record<string, unknown>): VmDependencies | undefined {
  const raw = attributes?.vm_dependencies
  if (!raw || typeof raw !== 'object') return undefined
  const value = raw as Record<string, unknown>
  const disks = Array.isArray(value.disks) ? value.disks.flatMap((item): VmDiskDependency[] => {
    if (!item || typeof item !== 'object') return []
    const disk = item as Record<string, unknown>
    const deviceName = stringValue(disk.device_name ?? disk.deviceName)
    const volumeName = stringValue(disk.volume_name ?? disk.volumeName)
    if (!deviceName || !volumeName) return []
    return [{ deviceName, volumeName, ...(stringValue(disk.bus) ? { bus: stringValue(disk.bus) } : {}), ...(ref(disk.data_volume ?? disk.dataVolume) ? { dataVolume: ref(disk.data_volume ?? disk.dataVolume) } : {}), ...(ref(disk.pvc) ? { pvc: ref(disk.pvc) } : {}), ...(ref(disk.pv) ? { pv: ref(disk.pv) } : {}), ...(ref(disk.storage_class ?? disk.storageClass) ? { storageClass: ref(disk.storage_class ?? disk.storageClass) } : {}) }]
  }) : []
  const networks = Array.isArray(value.networks) ? value.networks.flatMap((item): VmNetworkDependency[] => {
    if (!item || typeof item !== 'object') return []
    const network = item as Record<string, unknown>
    const interfaceName = stringValue(network.interface_name ?? network.interfaceName)
    if (!interfaceName) return []
    const defaultPodNetwork = network.default_pod_network === true || network.defaultPodNetwork === true
    return [{ interfaceName, binding: stringValue(network.binding) ?? 'unknown', defaultPodNetwork, ...(ref(network.nad) ? { nad: ref(network.nad) } : {}), ...(ref(network.cni) ? { cni: ref(network.cni) } : {}), ...(ref(network.network) ? { network: ref(network.network) } : {}) }]
  }) : []
  return disks.length || networks.length ? { disks, networks } : undefined
}

function typeFields(item: ResourceCatalogItem): DetailField[] {
  const attrs = item.attributes ?? {}
  if (item.type === 'physical_server') return [
    field('vendor', '厂商', attrs.vendor), field('model', '型号', attrs.model ?? attrs.product_name), field('serial_number', '序列号', attrs.serial_number),
    field('bmc', 'BMC', attrs.bmc_identifier), field('component_health', '组件健康', attrs.component_health),
  ]
  if (item.type === 'k8s_node') return [
    field('role', '角色', attrs.role), field('version', '版本', attrs.version), field('ready', 'Ready', attrs.ready), field('taints', '污点', attrs.taints),
    field('capacity', '容量', attrs.capacity), field('host', '宿主', attrs.host),
  ]
  if (item.type === 'vm' || item.type === 'vmi') return [
    field('namespace', 'Namespace', attrs.namespace ?? item.namespace), field('node', '节点', attrs.node), field('cpu', 'CPU', attrs.cpu),
    field('memory', '内存', attrs.memory), field('disk', '磁盘', attrs.disk), field('network', '网络', attrs.network), field('migration', '迁移', attrs.migration),
  ]
  return [field('location', '定位路径', item.location), field('capabilities', '能力', item.capabilities)]
}

export function projectResourceDetail(detail: ResourceDetailResponse): DetailSection[] {
  const item = detail.data
  return [
    { key: 'identity', title: '身份', fields: [field('type', '类型', item.type), field('name', '名称', item.name), field('location', '定位路径', item.location), field('cluster', '集群', item.clusterId)] },
    { key: 'health', title: '健康与来源', fields: [field('health', '健康', item.health), field('source', '来源', item.source), field('last_seen_at', '最近观测', item.lastSeenAt), field('resolution', '数据质量', item.resolution)] },
    { key: 'type-specific', title: '类型专属信息', fields: typeFields(item) },
    { key: 'operations', title: '运维上下文', fields: [field('events', '事件', undefined), field('changes', '变更', undefined), field('dependencies', '依赖', undefined), field('investigation', '调查', undefined), field('actions', '动作', item.capabilities?.length ? item.capabilities : undefined)] },
    { key: 'data-quality', title: '数据质量', fields: [field('partial', '部分数据', detail.meta.partial ? '是' : '否'), field('stale', '可能过期', detail.meta.stale ? '是' : '否'), field('warnings', '告警代码', detail.meta.warningCodes)] },
  ]
}
