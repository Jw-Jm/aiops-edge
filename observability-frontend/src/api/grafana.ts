import api from './client'

// ===== Grafana 集成（工作流 F3，query-api 代理：/api/v1/grafana/*）=====

// 上游 grafana /api/health 字段（透传）
export interface GrafanaHealth {
  database?: string
  version?: string
  commit?: string
  [k: string]: unknown
}

// 上游 grafana /api/search 返回的 dashboard 条目
export interface GrafanaDashboardItem {
  id?: number
  uid?: string
  title?: string
  tags?: string[]
  type?: string
  folderTitle?: string
  [k: string]: unknown
}

// 健康检查：透传上游 /api/health（200 即服务可达）
export const getGrafanaHealth = () => api.get<GrafanaHealth>('/grafana/health')

// 搜索：透传上游 /api/search?query=
export const searchGrafanaDashboards = (query: string) =>
  api.get<GrafanaDashboardItem[]>('/grafana/search', { params: { query } })

// 备用：dashboard JSON（Phase 2 原生渲染用，本页 Phase 1 不依赖）
export const getGrafanaDashboard = (uid: string) =>
  api.get<Record<string, unknown>>(`/grafana/dashboards/${encodeURIComponent(uid)}`)
