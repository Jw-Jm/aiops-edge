import React from 'react'
import DataState, { type DataStateKind } from './DataState'

export type DataRegionState = 'ready' | DataStateKind

export interface ReadMeta {
  generatedAt?: string
  partial?: boolean
  stale?: boolean
  warningCodes?: string[]
}

export interface BoundedDataRegionProps {
  state: DataRegionState
  meta?: ReadMeta
  error?: unknown
  onRetry?: () => void
  children: React.ReactNode
  className?: string
}

const BANNER_COPY: Partial<Record<DataRegionState, { title: string; description: string }>> = {
  partial: { title: '数据不完整', description: '部分来源暂时不可用，以下仍是已读取的事实' },
  stale: { title: '数据可能已过期', description: '当前展示的是最近一次可用快照' },
}

function errorDescription(error: unknown): string | undefined {
  if (error instanceof Error && error.message.trim()) return error.message
  if (typeof error === 'string' && error.trim()) return error
  return undefined
}

export function BoundedDataRegion({ state, meta, error, onRetry, children, className = '' }: BoundedDataRegionProps) {
  const classes = ['bounded-data-region', className].filter(Boolean).join(' ')
  const banner = BANNER_COPY[state]
  if (state === 'ready' || state === 'partial' || state === 'stale') {
    return (
      <section className={classes} data-testid="bounded-data-region" data-state={state} data-generated-at={meta?.generatedAt}>
        {banner && <div className={`bounded-data-region__banner bounded-data-region__banner--${state}`} role="status" aria-live="polite"><strong>{banner.title}</strong><span>{banner.description}</span></div>}
        {children}
      </section>
    )
  }
  return (
    <section className={classes} data-testid="bounded-data-region" data-state={state} data-generated-at={meta?.generatedAt}>
      <DataState kind={state} description={errorDescription(error)} onRetry={onRetry} />
    </section>
  )
}

export default BoundedDataRegion
