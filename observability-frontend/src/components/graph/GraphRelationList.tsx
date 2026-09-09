import React, { useMemo, useState } from 'react'
import type { GraphRelationRow } from './graphPresentation'

export default function GraphRelationList({ rows, onLocate }: { rows: GraphRelationRow[]; onLocate?: (edgeId: string) => void }) {
  const [descending, setDescending] = useState(false)
  const sorted = useMemo(() => [...rows].sort((left, right) => `${left.sourceName}${left.label}${left.targetName}`.localeCompare(`${right.sourceName}${right.label}${right.targetName}`) * (descending ? -1 : 1)), [descending, rows])
  return <section className="graph-relation-list" aria-label="关系明细"><div className="section-heading"><strong>关系明细</strong><button type="button" onClick={() => setDescending((value) => !value)}>{descending ? '恢复顺序' : '按名称排序'}</button></div>{sorted.length ? sorted.map((row) => <button type="button" key={row.id} className="graph-relation-list__row" aria-label={`${row.sourceName} ${row.label} ${row.targetName}`} onClick={() => onLocate?.(row.id)} onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); onLocate?.(row.id) } }}><span>{row.sourceName}</span><em>{row.label}</em><span>{row.targetName}</span></button>) : <div className="graph-relation-list__empty">暂无关系</div>}</section>
}
