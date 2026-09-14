import React, { useMemo, useState } from 'react'
import { Button } from 'antd'

const MAX_BYTES = 200 * 1024

function utf8Length(value: string): number {
  return typeof TextEncoder === 'undefined' ? value.length : new TextEncoder().encode(value).length
}

function truncateUtf8(value: string, maxBytes: number): string {
  if (utf8Length(value) <= maxBytes) return value
  let result = value.slice(0, maxBytes)
  while (utf8Length(result) > maxBytes) result = result.slice(0, -1)
  return `${result}\n… [已截断]`
}

export interface RawDataPanelProps {
  data: unknown
  title?: string
}

export function RawDataPanel({ data, title = '原始数据' }: RawDataPanelProps) {
  const [expanded, setExpanded] = useState(false)
  const [copied, setCopied] = useState(false)
  const formatted = useMemo(() => {
    if (typeof data === 'string') return data
    try { return JSON.stringify(data, null, 2) ?? 'null' } catch { return String(data) }
  }, [data])
  const truncated = utf8Length(formatted) > MAX_BYTES
  const display = truncated ? truncateUtf8(formatted, MAX_BYTES) : formatted

  async function copy(): Promise<void> {
    try {
      await navigator.clipboard?.writeText(formatted)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    } catch {
      setCopied(false)
    }
  }

  return (
    <section className="raw-data-panel">
      <Button type="text" size="small" aria-expanded={expanded} onClick={() => setExpanded((current) => !current)}>{title}</Button>
      {expanded && (
        <div className="raw-data-panel__body">
          <div className="raw-data-panel__toolbar">
            <span>{truncated ? '原始数据超过 200KB，已截断显示' : '只读诊断载荷'}</span>
            <Button size="small" aria-label={copied ? '已复制' : '复制'} onClick={() => void copy()}>{copied ? '已复制' : '复制'}</Button>
          </div>
          <pre>{display}</pre>
        </div>
      )}
    </section>
  )
}

export default RawDataPanel
