import type { AppIconName } from '../components/AppIcons'

export interface NavItem {
  path: string
  label: string
  icon: AppIconName
  badge?: string
  adminOnly?: boolean
}

export const PRIMARY_NAV: NavItem[] = [
  { path: '/overview', label: '工作台', icon: 'overview' },
  { path: '/investigation', label: '调查', icon: 'chat' },
  { path: '/resources', label: '资源', icon: 'assets' },
  { path: '/observe', label: '观测', icon: 'monitor', badge: 'dynamic' },
  { path: '/actions', label: '处置', icon: 'approvals' },
  { path: '/reports', label: '报告', icon: 'reports' },
  { path: '/admin', label: '系统管理', icon: 'settings', adminOnly: true },
]

export const LEGACY_REDIRECTS = new Map<string, string>([
  ['/observability/service', '/resources?kind=service'],
  ['/observability/relationships', '/resources?view=relationships'],
  ['/observability/vms', '/resources?kind=vm'],
  ['/infra/k8s', '/resources?kind=kubernetes'],
  ['/hardware', '/resources?kind=hardware'],
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

export function legacyTarget(pathname: string): string | null {
  return LEGACY_REDIRECTS.get(pathname) ?? null
}
