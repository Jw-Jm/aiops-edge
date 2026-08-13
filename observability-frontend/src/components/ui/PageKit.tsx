import React from 'react'

// =====================================================================
//  共享页面模板组件（v3.0 亮色极简）
// =====================================================================

export type StatusTone = 'ok' | 'warn' | 'crit' | 'info' | 'muted'

export const StatusBadge: React.FC<{ text: string; tone: StatusTone }> = ({ text, tone }) => (
  <span className={`status status--${tone}`}><span className="dot" />{text}</span>
)

export const Breadcrumb: React.FC<{ items: { t: string; href?: string }[]; onClick?: (href: string) => void }> = ({ items, onClick }) => (
  <div className="breadcrumb">
    {items.map((it, i) => (
      <React.Fragment key={i}>
        {i > 0 && <span className="sep">/</span>}
        {it.href
          ? <a onClick={() => onClick?.(it.href!)}>{it.t}</a>
          : <span>{it.t}</span>}
      </React.Fragment>
    ))}
  </div>
)

export const PageHeader: React.FC<{ title: string; desc?: React.ReactNode; actions?: React.ReactNode }> = ({ title, desc, actions }) => (
  <div className="page-header">
    <div>
      <h1 className="page-title">{title}</h1>
      {desc && <div className="page-desc">{desc}</div>}
    </div>
    {actions && <div className="page-actions">{actions}</div>}
  </div>
)

export const PaneCard: React.FC<{
  title?: React.ReactNode
  action?: React.ReactNode
  flush?: boolean
  className?: string
  style?: React.CSSProperties
  children: React.ReactNode
}> = ({ title, action, flush, className, style, children }) => (
  <section className={`card ${className || ''}`} style={style}>
    {title && <div className="card__head"><div className="card__title">{title}</div>{action}</div>}
    <div className={`card__body${flush ? ' card__body--flush' : ''}`}>{children}</div>
  </section>
)

export const Sparkline: React.FC<{ pts: string; color?: string; w?: number; h?: number }> = ({ pts, color, w = 120, h = 40 }) => (
  <svg style={{ display: 'block' }} width={w} height={h}>
    <polyline fill="none" stroke={color || 'var(--text-muted)'} strokeWidth={2} points={pts} />
  </svg>
)

export const StatCard: React.FC<{
  label: string
  value: React.ReactNode
  unit?: string
  trend?: string
  trendDir?: 'up' | 'down' | 'flat'
  spark?: string
  sparkColor?: string
  style?: React.CSSProperties
}> = ({ label, value, unit, trend, trendDir = 'flat', spark, sparkColor, style }) => (
  <div className="stat" style={{ height: '100%', ...style }}>
    <div className="stat__label">{label}</div>
    <div className="stat__value">{value}{unit && <span className="unit">{unit}</span>}</div>
    {trend && <div className={`stat__trend ${trendDir}`}>{trendDir === 'up' ? '▲' : trendDir === 'down' ? '▼' : '—'} {trend}</div>}
    {spark && <Sparkline pts={spark} color={sparkColor} />}
  </div>
)

export const Empty: React.FC<{ text?: string; hint?: string }> = ({ text = '暂无数据', hint }) => (
  <div style={{ textAlign: 'center', padding: 48, color: 'var(--text-muted)' }}>
    <div style={{ fontSize: 30, marginBottom: 8, opacity: .5 }}>▢</div>
    <div style={{ fontSize: 13, color: 'var(--text-secondary)' }}>{text}</div>
    {hint && <div style={{ fontSize: 12, marginTop: 6 }}>{hint}</div>}
  </div>
)
