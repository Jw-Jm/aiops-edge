/**
 * P2-A2: 内存 scope runtime——替代请求拦截器里"每请求 JSON.parse(localStorage)"。
 *
 * - uiStore(zustand persist) 在 hydrate/切换集群时调用 setScopeCluster 同步；
 * - 模块加载时一次性从 localStorage 恢复初始值（仅一次，非每请求）；
 * - 请求拦截器只读内存 getScopeCluster()，不再逐请求 JSON.parse。
 *
 * cluster_id 是查询过滤参数，不是授权依据：服务端由 Query API 基于
 * HttpOnly session + active scope 强制注入/校验（见 /me/scope）。
 */

function readInitialCluster(): string {
  try {
    const raw = localStorage.getItem('aiops-ui-v3')
    if (!raw) return ''
    const parsed = JSON.parse(raw) as { state?: { currentClusterId?: string } }
    return parsed?.state?.currentClusterId ?? ''
  } catch {
    return ''
  }
}

let currentClusterId = readInitialCluster()

export function setScopeCluster(id: string | null | undefined): void {
  currentClusterId = id || ''
}

export function getScopeCluster(): string {
  return currentClusterId
}
