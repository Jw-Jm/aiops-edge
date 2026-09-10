import type { AppIconName } from '../components/AppIcons'

export interface NavItem {
  path: string
  label: string
  icon: AppIconName
  workspace?: ClusterWorkspace
  badge?: string
  adminOnly?: boolean
}

export type ClusterWorkspace = 'overview' | 'resources' | 'observe' | 'graph' | 'assistant' | 'investigations' | 'actions' | 'knowledge' | 'reports'

export const PRIMARY_NAV: NavItem[] = [
  { path: '/overview', label: '平台', icon: 'overview' },
  { path: '/clusters', label: '集群', icon: 'cluster', workspace: 'overview' },
  { path: '/clusters', label: '助手', icon: 'chat', workspace: 'assistant' },
  { path: '/clusters', label: '调查', icon: 'tasks', workspace: 'investigations' },
  { path: '/clusters', label: '处置', icon: 'approvals', workspace: 'actions' },
  { path: '/clusters', label: '知识', icon: 'knowledge', workspace: 'knowledge' },
  { path: '/clusters', label: '报告', icon: 'reports', workspace: 'reports' },
  { path: '/admin', label: '系统管理', icon: 'settings', adminOnly: true },
]

export const LEGACY_REDIRECTS = new Map<string, string>([
  ['/observability/service', '/resources?domain=application'],
  ['/observability/relationships', '/resources?view=graph'],
  ['/observability/vms', '/resources?domain=compute&type=vm'],
  ['/infra/k8s', '/resources?domain=kubernetes'],
  ['/hardware', '/resources?domain=compute&type=physical_server'],
  ['/capacity', '/resources?view=capacity'],
  ['/alerts/events', '/observe?view=alerts'],
  ['/alerts/rules', '/observe?view=rules'],
  ['/observability/trace', '/observe?view=traces'],
  ['/observability/log', '/observe?view=telemetry'],
  ['/changes', '/observe?view=changes'],
  ['/observability/grafana', '/observe?view=grafana'],
  ['/admin/approvals', '/actions'],
  ['/report', '/reports'],
])

export function visiblePrimaryNav(role: string): NavItem[] {
  return PRIMARY_NAV.filter((item) => !item.adminOnly || role === 'admin')
}

export function clusterPath(clusterId: string, workspace: ClusterWorkspace): string {
  const encodedClusterId = encodeURIComponent(clusterId)
  return workspace === 'overview'
    ? `/clusters/${encodedClusterId}`
    : `/clusters/${encodedClusterId}/${workspace}`
}

export function legacyTarget(pathname: string): string | null {
  return LEGACY_REDIRECTS.get(pathname) ?? null
}
