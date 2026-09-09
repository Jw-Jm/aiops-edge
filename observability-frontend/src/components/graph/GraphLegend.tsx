import React from 'react'

export default function GraphLegend() {
  return <div className="graph-legend" aria-label="图谱图例"><span><i className="graph-legend__dot graph-legend__dot--critical" />严重</span><span><i className="graph-legend__dot graph-legend__dot--degraded" />降级</span><span><i className="graph-legend__line" />结构关系</span><span><i className="graph-legend__line graph-legend__line--dashed" />推测关系</span></div>
}
