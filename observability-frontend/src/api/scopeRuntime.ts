/**
 * P2-A2: 内存 scope runtime——替代请求拦截器里"每请求 JSON.parse(localStorage)"。
 *
 * - scopeStore 在 GET /me 初始化或 POST /me/scope 确认后调用 setScopeCluster；
 * - 模块加载时不从 localStorage 恢复 active scope；本地只允许保存偏好值；
 * - 请求拦截器只读内存 getScopeCluster()，不再逐请求 JSON.parse。
 *
 * cluster_id 是查询过滤参数，不是授权依据：服务端由 Query API 基于
 * HttpOnly session + active scope 强制注入/校验（见 /me/scope）。
 */

let activeClusterId = ''

export function setScopeCluster(id: string | null | undefined): void {
  activeClusterId = id || ''
}

export function getScopeCluster(): string {
  return activeClusterId
}
