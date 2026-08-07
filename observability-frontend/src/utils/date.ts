import dayjs from 'dayjs'
import utc from 'dayjs/plugin/utc'
import timezone from 'dayjs/plugin/timezone'

// 启用 dayjs 的 UTC 与时区插件
dayjs.extend(utc)
dayjs.extend(timezone)

// 系统展示默认时区（与服务器/用户本地一致；如需固定为北京时间可改为 'Asia/Shanghai'）
const LOCAL_TZ = dayjs.tz.guess() || 'Asia/Shanghai'

/**
 * 将后端返回的 UTC 时间字符串转换为本地时区并格式化。
 * 后端统一以 UTC 存储/返回，前端展示需转换为用户本地时间（如 CST）。
 *
 * 支持的输入：
 *  - "2026-08-06 05:53:00"           (UTC，无后缀)
 *  - "2026-08-06T05:53:00.123Z"      (ISO 带 Z)
 *  - "2026-08-06 05:53:00.000000000" (纳秒精度)
 *  - undefined/null / 空字符串       (返回 fallback)
 *
 * @param v        后端返回的时间值
 * @param fallback 空值时的回退文本，默认 '-'
 * @param fmt      输出格式，默认 'YYYY-MM-DD HH:mm:ss'
 */
export function fmtLocalTime(v?: string | number | null, fallback = '-', fmt = 'YYYY-MM-DD HH:mm:ss'): string {
  if (v === undefined || v === null || v === '') return fallback

  // 提取可解析的时间字符串（去掉可能的时区/纳秒噪声，统一视为 UTC）
  let s = String(v).trim()
  if (!s) return fallback

  // 形如 "2026-08-06 05:53:00" / "2026-08-06T05:53:00"：无 Z/偏移，视为 UTC
  // 形如 "2026-08-06 05:53:00.000000000"：去掉纳秒
  s = s.replace(/\.\d+$/, '').replace('T', ' ')

  // 解析为 UTC 时间，再转本地时区
  const d = dayjs.utc(s)
  if (!d.isValid()) return fallback
  return d.tz(LOCAL_TZ).format(fmt)
}

/**
 * 将 UTC 时间字符串转换为本地时区的时分（用于趋势图 x 轴）。
 */
export function fmtLocalHM(v?: string | null, fallback = ''): string {
  return fmtLocalTime(v, fallback, 'HH:mm')
}

/**
 * 将带纳秒的 UTC 时间字符串转换为本地时区并保留毫秒精度。
 * 输入如 "2026-08-06 05:55:28.899717137"（UTC），输出如 "13:55:28.899"（本地）。
 */
export function fmtLocalMs(v?: string | null, fallback = '-'): string {
  if (v === undefined || v === null || v === '') return fallback
  let s = String(v).trim().replace('T', ' ')
  const m = s.match(/^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\.(\d+)$/)
  if (!m) {
    // 无小数部分，直接当 UTC 秒级转换
    return fmtLocalTime(s, fallback, 'HH:mm:ss.SSS')
  }
  const ms = m[2].slice(0, 3).padEnd(3, '0')
  const d = dayjs.utc(m[1])
  if (!d.isValid()) return fallback
  return d.tz(LOCAL_TZ).format('HH:mm:ss') + '.' + ms
}
