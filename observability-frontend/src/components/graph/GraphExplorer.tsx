import React, { useMemo, useState } from 'react'
import type { GraphSubgraph } from '../../api/graphContracts'
import GraphMap from './GraphMap'
import DependencyChain from './DependencyChain'
import GraphToolbar from './GraphToolbar'
import GraphLegend from './GraphLegend'
import GraphRelationList from './GraphRelationList'
import { buildGraphDisplayModel, layoutForMode, type GraphLayoutMode, type GraphViewMode } from './graphPresentation'

export default function GraphExplorer({ subgraph }: { subgraph: GraphSubgraph }) {
  const [mode, setMode] = useState<GraphViewMode>('expert')
  const [layout, setLayout] = useState<GraphLayoutMode>(layoutForMode('expert'))
  const display = useMemo(() => buildGraphDisplayModel(subgraph, { mode, centerUid: subgraph.center_entity_uid, maxNodes: 80, maxEdges: 200 }), [mode, subgraph])
  return <section aria-label="专家关系探索" className="graph-explorer"><div className="section-heading"><div><h3 style={{ margin: 0 }}>关系探索</h3><span className="page-desc">默认展示资源关系；故障链与专家模式共享同一份原始图谱数据</span></div><GraphToolbar mode={mode} layout={layout} onModeChange={(next) => { setMode(next); setLayout(layoutForMode(next)) }} onLayoutChange={setLayout} /></div><GraphLegend /><GraphMap subgraph={subgraph} mode={mode} layoutOverride={layout} centerUid={subgraph.center_entity_uid} maxNodes={80} maxEdges={200} /><div style={{ marginTop: 12 }}><GraphRelationList rows={display.relationRows} totalEdges={subgraph.total_edges} aggregated={subgraph.aggregated} /></div><div style={{ marginTop: 16 }}><DependencyChain vertices={subgraph.vertices} edges={subgraph.edges} /></div></section>
}
