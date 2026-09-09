import type { ResourceCatalogItem, ResourceDetailResponse } from '../../api/resources'

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

function display(value: unknown): string {
  if (value === undefined || value === null || value === '') return '未提供'
  if (typeof value === 'boolean') return value ? '是' : '否'
  if (Array.isArray(value)) return value.length ? value.map(display).join('、') : '无'
  if (typeof value === 'object') return JSON.stringify(value)
  return String(value)
}

function field(key: string, label: string, value: unknown): DetailField {
  return { key, label, value: display(value) }
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

