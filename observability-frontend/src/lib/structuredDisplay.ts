/**
 * Render structured diagnostic values as readable key/value lines.
 * Raw JSON remains available only through RawDataPanel, where it is folded
 * and bounded for safe inspection.
 */
export function formatStructuredValue(value: unknown): string {
  const seen = new WeakSet<object>()
  const visit = (current: unknown, depth: number): string => {
    if (current === null || current === undefined || current === '') return '未提供'
    if (typeof current === 'boolean') return current ? '是' : '否'
    if (typeof current !== 'object') return String(current)
    if (seen.has(current)) return '[循环引用]'
    seen.add(current)
    if (Array.isArray(current)) return current.length ? current.map((item, index) => `${index + 1}. ${visit(item, depth + 1)}`).join('\n') : '无'
    const entries = Object.entries(current)
    if (!entries.length) return '无'
    const indent = '  '.repeat(depth)
    return entries.map(([key, item]) => `${indent}${key}: ${visit(item, depth + 1)}`).join('\n')
  }
  try { return visit(value, 0) } catch { return String(value) }
}

