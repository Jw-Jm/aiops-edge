/**
 * 时间/容量/CPU 格式化工具：统一全站展示口径。
 * 消除各处 `slice(5,16)`、`Number(v)*1000` 等对时间格式的假设。
 */

/**
 * 将时间戳格式化为 "YYYY-MM-DD HH:mm:ss"。
 *
 * 输入约定：
 * - number：> 1e12 视为毫秒，否则视为秒（自动判别，无需调用方换算）；
 * - string：ISO 字符串（如 "2026-08-18T10:30:00Z"），无法解析时原样返回；
 * - null / undefined / ''：返回 '-'。
 */
export function fmtTime(ts: number | string | null | undefined): string {
  if (ts === null || ts === undefined || ts === '') return '-'
  let d: Date
  if (typeof ts === 'number') {
    const ms = ts > 1e12 ? ts : ts * 1000
    d = new Date(ms)
  } else {
    const parsed = Date.parse(ts)
    if (isNaN(parsed)) return String(ts)
    d = new Date(parsed)
  }
  if (isNaN(d.getTime())) return String(ts)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

/**
 * 字节数人类可读格式化（B/KB/MB/GB/TB/PB，1024 进制）。
 * 无效输入（null/undefined/NaN）返回 '-'。
 */
export function fmtBytes(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined || isNaN(bytes)) return '-'
  if (bytes === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  const i = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)))
  const v = bytes / Math.pow(1024, i)
  const s = v >= 100 ? v.toFixed(0) : v.toFixed(1)
  return `${s} ${units[i]}`
}

/**
 * CPU 核数格式化：整数直接输出，小数保留至多 2 位并去除尾零。
 * 无效输入（null/undefined/NaN）返回 '-'。
 */
export function fmtCpu(cores: number | null | undefined): string {
  if (cores === null || cores === undefined || isNaN(cores)) return '-'
  if (cores === 0) return '0'
  const s = cores >= 100 ? cores.toFixed(0) : cores.toFixed(2).replace(/\.?0+$/, '')
  return s
}