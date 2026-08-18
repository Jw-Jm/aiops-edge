/**
 * 告警严重度归一化工具：把中文/英文/数字等多套取值映射到统一枚举。
 * 解决筛选值（英文）与展示值（中文）不一致导致的筛选失效问题（见 A4）。
 */

/** 统一严重度枚举 */
export type Severity = 'critical' | 'warning' | 'info'

/** 统一严重度 → 中文展示标签 */
export const SEVERITY_LABELS: Record<Severity, string> = {
  critical: '严重',
  warning: '警告',
  info: '信息',
}

/**
 * 归一化严重度取值到统一枚举。
 *
 * 支持输入：
 * - 中文：严重/警告/信息（含 紧急/致命/高 等近义）；
 * - 英文：critical/warning/info（含 error/fatal/alert → critical，warn → warning）；
 * - 数字：3/2/1 档位，或 0-100 分档（>=80 critical，>=50 warning，其余 info）；
 * - 缺失/未知：默认 'warning'（与现有页面兜底一致）。
 */
export function normalizeSeverity(v: unknown): Severity {
  if (v === null || v === undefined || v === '') return 'warning'
  if (typeof v === 'number') {
    if (v >= 80 || v === 3) return 'critical'
    if (v >= 50 || v === 2) return 'warning'
    return 'info'
  }
  const s = String(v).trim().toLowerCase()
  // 数字字符串（如 "3"、"90"）走数字分档
  if (/^\d+(\.\d+)?$/.test(s)) {
    const n = parseFloat(s)
    if (n >= 80 || n === 3) return 'critical'
    if (n >= 50 || n === 2) return 'warning'
    return 'info'
  }
  if (['critical', 'crit', 'error', 'fatal', 'alert', 'emergency', 'severe', '严重', '紧急', '致命', '高'].includes(s)) return 'critical'
  if (['warning', 'warn', '警告', '中'].includes(s)) return 'warning'
  if (['info', 'information', 'informational', 'notice', 'debug', '信息', '低', '提示'].includes(s)) return 'info'
  return 'warning'
}