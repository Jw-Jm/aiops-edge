import React from 'react'
import { RELATION_SEMANTIC_LABELS, RELATION_SEMANTICS } from './graphPresentation'

/**
 * 图谱图例：说明七类关系语义、健康色与故障传播色型。
 * 颜色只增强语义，任何可见边都必须能直接读出中文关系名。
 */
export default function GraphLegend() {
  return (
    <div className="graph-legend" aria-label="图谱图例">
      <span><i className="graph-legend__dot graph-legend__dot--critical" />严重</span>
      <span><i className="graph-legend__dot graph-legend__dot--degraded" />降级</span>
      <span><i className="graph-legend__dot graph-legend__dot--healthy" />健康</span>
      <span><i className="graph-legend__dot graph-legend__dot--unknown" />未知</span>
      <span><i className="graph-legend__line" />事实关系（实线）</span>
      <span><i className="graph-legend__line graph-legend__line--dashed" />推断关系（虚线）</span>
      <span><i className="graph-legend__line graph-legend__line--failure" />故障传播（仅故障链）</span>
      <span className="graph-legend__direction">箭头：源资源 → 目标资源</span>
      <span className="graph-legend__direction">关系语义：{RELATION_SEMANTICS.map((semantic) => RELATION_SEMANTIC_LABELS[semantic]).join(' / ')}</span>
    </div>
  )
}
