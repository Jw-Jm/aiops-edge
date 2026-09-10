import React from 'react'

export default function GraphLegend() {
  return <div className="graph-legend" aria-label="图谱图例"><span><i className="graph-legend__dot graph-legend__dot--critical" />严重</span><span><i className="graph-legend__dot graph-legend__dot--degraded" />降级</span><span><i className="graph-legend__line" />结构关系</span><span><i className="graph-legend__line graph-legend__line--dashed" />推测关系</span><span className="graph-legend__direction">箭头：源资源 → 目标资源</span><span className="graph-legend__direction">红色：故障传播</span></div>
}
