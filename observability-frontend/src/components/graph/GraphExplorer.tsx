import React, { useMemo, useState } from 'react'
import { Card, Descriptions, Tag } from 'antd'
import type { GraphSubgraph } from '../../api/graphContracts'
import GraphMap from './GraphMap'
import DependencyChain from './DependencyChain'
import GraphToolbar from './GraphToolbar'
import GraphLegend from './GraphLegend'
import GraphRelationList from './GraphRelationList'
import { buildGraphDisplayModel, layoutForMode, type GraphLayoutMode, type GraphRelationRow, type GraphViewMode } from './graphPresentation'

export default function GraphExplorer({ subgraph }: { subgraph: GraphSubgraph }) {
  const [mode, setMode] = useState<GraphViewMode>('resource-relations')
  const [layout, setLayout] = useState<GraphLayoutMode>(layoutForMode('resource-relations'))
  const [selectedEdge, setSelectedEdge] = useState<GraphRelationRow | null>(null)
  const display = useMemo(() => buildGraphDisplayModel(subgraph, { mode, centerUid: subgraph.center_entity_uid, maxNodes: 80, maxEdges: 200 }), [mode, subgraph])
  return <section aria-label="专家关系探索" className="graph-explorer"><div className="section-heading"><div><h3 style={{ margin: 0 }}>关系探索</h3><span className="page-desc">默认展示资源关系；故障链与专家模式共享同一份原始图谱数据</span></div><GraphToolbar mode={mode} layout={layout} onModeChange={(next) => { setMode(next); setLayout(layoutForMode(next)); setSelectedEdge(null) }} onLayoutChange={setLayout} /></div><GraphLegend /><GraphMap subgraph={subgraph} mode={mode} layoutOverride={layout} centerUid={subgraph.center_entity_uid} maxNodes={80} maxEdges={200} /><div style={{ marginTop: 12 }}><GraphRelationList rows={display.relationRows} totalEdges={subgraph.total_edges} aggregated={subgraph.aggregated} onLocate={(edgeId) => setSelectedEdge(display.relationRows.find((row) => row.id === edgeId) ?? null)} /></div>{selectedEdge && <Card size="small" title="关系检查器" style={{ marginTop: 12 }} extra={<Tag>{selectedEdge.factStatus === 'fact' ? '事实' : '推断'}</Tag>}><Descriptions size="small" column={1}><Descriptions.Item label="源资源">{selectedEdge.sourceName}</Descriptions.Item><Descriptions.Item label="关系">{selectedEdge.label} →</Descriptions.Item><Descriptions.Item label="目标资源">{selectedEdge.targetName}</Descriptions.Item><Descriptions.Item label="事实来源">{selectedEdge.sourceRef || '未提供'}</Descriptions.Item><Descriptions.Item label="同步时间">{selectedEdge.syncedAt || '未提供'}</Descriptions.Item></Descriptions></Card>}<div style={{ marginTop: 16 }}><DependencyChain vertices={subgraph.vertices} edges={subgraph.edges} centerUid={subgraph.center_entity_uid} mode={mode} /></div></section>
}
