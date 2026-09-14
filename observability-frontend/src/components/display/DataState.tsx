import React from 'react'
import { Button, Result, Skeleton } from 'antd'

export type DataStateKind = 'loading' | 'empty' | 'error' | 'partial' | 'stale' | 'forbidden'

const DEFAULT_COPY: Record<DataStateKind, { title: string; description: string }> = {
  loading: { title: '正在加载', description: '正在读取当前范围的数据' },
  empty: { title: '暂无数据', description: '当前范围暂无可用数据' },
  error: { title: '加载失败', description: '数据读取失败，请稍后重试' },
  partial: { title: '数据不完整', description: '部分来源暂时不可用，以下结果可能不完整' },
  stale: { title: '数据可能已过期', description: '当前展示的是最近一次可用快照' },
  forbidden: { title: '无权访问', description: '当前角色没有读取这部分数据的权限' },
}

export interface DataStateProps {
  kind: DataStateKind
  title?: string
  description?: string
  onRetry?: () => void
  compact?: boolean
}

export function DataState({ kind, title, description, onRetry, compact = false }: DataStateProps) {
  const copy = DEFAULT_COPY[kind]
  const label = title ?? copy.title
  const detail = description ?? copy.description
  const role = kind === 'error' || kind === 'forbidden' ? 'alert' : 'status'

  if (kind === 'loading') {
    return <div className={`data-state data-state--loading${compact ? ' data-state--compact' : ''}`} role={role} aria-live="polite" aria-label={label}><Skeleton active paragraph={{ rows: compact ? 1 : 2 }} /></div>
  }
  return (
    <div className={`data-state data-state--${kind}${compact ? ' data-state--compact' : ''}`} role={role} aria-live={role === 'status' ? 'polite' : undefined}>
      <Result
        status={kind === 'error' ? 'error' : kind === 'forbidden' ? '403' : kind === 'empty' ? 'info' : 'warning'}
        title={label}
        subTitle={detail}
        extra={kind === 'error' && onRetry ? <Button size="small" aria-label="重试" onClick={onRetry}>重试</Button> : undefined}
      />
    </div>
  )
}

export default DataState
