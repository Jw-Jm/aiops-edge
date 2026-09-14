import type { PlatformClusterHealth } from '../../api/platform'

/**
 * 平台与集群详情共用的健康呈现映射。
 *
 * 同一集群在平台矩阵、平台列表和集群详情中必须得到完全相同的状态标签、
 * 色调与原因文本，否则会出现"平台显示健康、详情显示降级"的现场事实矛盾。
 */
export const CLUSTER_HEALTH_LABELS: Record<PlatformClusterHealth, string> = {
  healthy: '健康',
  degraded: '降级',
  critical: '严重',
  unknown: '未知',
}

export const CLUSTER_HEALTH_TONES: Record<PlatformClusterHealth, 'ok' | 'warn' | 'crit' | 'muted'> = {
  healthy: 'ok',
  degraded: 'warn',
  critical: 'crit',
  unknown: 'muted',
}

/** 健康排序：严重 → 降级 → 未知 → 健康。 */
export const CLUSTER_HEALTH_SORT_ORDER: Record<PlatformClusterHealth, number> = {
  critical: 0,
  degraded: 1,
  unknown: 2,
  healthy: 3,
}

/**
 * 接入/注册状态的中文投影。注册状态只说明生命周期（active/ready/running/ok），
 * 不产生健康结论，因此单独呈现，不与健康标签混用。
 */
export function registrationLabel(status: string | undefined): string {
  switch ((status ?? '').toLowerCase().trim()) {
    case 'ready':
    case 'active':
      return '接入：已就绪'
    case 'pending':
    case 'provisioning':
      return '接入：处理中'
    case 'retired':
    case 'disabled':
      return '接入：已停用'
    case '':
      return '接入：未知'
    default:
      return `接入：${status}`
  }
}

/** 能力摘要为空时不得伪造分子分母。 */
export function capabilitySummaryText(healthy: number, total: number): string {
  return total > 0 ? `${healthy}/${total}` : '未获得组件状态'
}
