/**
 * 错误率归一化工具：兼容 0-1 与 0-100 双口径（见 A2 契约）。
 * 后端 `/services` 返回 0-1 小数，`/dashboard/stats` 曾返回 0-100 百分比，
 * 前端消费前统一归一为 0-1 小数。
 */

/**
 * 归一化错误率为 0-1 小数。
 * - v > 1：视为 0-100 百分比，除以 100；
 * - 否则视为 0-1 小数原样返回；
 * - 无效输入（null/undefined/NaN）返回 0。
 * 结果始终裁剪在 [0, 1] 区间。
 */
export function normalizeErrorRate(v: unknown): number {
  if (v === null || v === undefined || v === '') return 0
  const n = typeof v === 'number' ? v : parseFloat(String(v))
  if (isNaN(n)) return 0
  if (n > 1) return Math.max(0, Math.min(1, n / 100))
  return Math.max(0, Math.min(1, n))
}