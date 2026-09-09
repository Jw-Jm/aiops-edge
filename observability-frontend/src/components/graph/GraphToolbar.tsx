import React from 'react'
import type { GraphLayoutMode, GraphViewMode } from './graphPresentation'

export default function GraphToolbar({ mode, layout, onModeChange, onLayoutChange }: { mode: GraphViewMode; layout: GraphLayoutMode; onModeChange: (mode: GraphViewMode) => void; onLayoutChange: (layout: GraphLayoutMode) => void }) {
  return <div className="graph-toolbar" aria-label="图谱工具栏"><label>视图 <select aria-label="图谱视图" value={mode} onChange={(event) => onModeChange(event.target.value as GraphViewMode)}><option value="resource-relations">资源关系</option><option value="failure-chain">故障链</option><option value="expert">专家探索</option></select></label><label>布局 <select aria-label="图谱布局" value={layout} onChange={(event) => onLayoutChange(event.target.value as GraphLayoutMode)}><option value="radial">环形</option><option value="hierarchy-tb">上下层级</option><option value="dag-lr">左右依赖</option></select></label></div>
}
