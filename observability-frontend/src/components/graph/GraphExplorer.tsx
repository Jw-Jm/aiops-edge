import React, { useMemo, useState } from 'react'
import { Button, Card, Descriptions, Drawer, Tag } from 'antd'
import type { GraphSubgraph } from '../../api/graphContracts'
import GraphMap from './GraphMap'
import DependencyChain from './DependencyChain'
import GraphToolbar from './GraphToolbar'
import GraphLegend from './GraphLegend'
import GraphRelationList from './GraphRelationList'
import DataState from '../display/DataState'
import {
  buildGraphDisplayModel,
  filterBySemantics,
  focusGraph,
  isContextEntityType,
  layoutForMode,
  RELATION_SEMANTIC_LABELS,
  RELATION_SEMANTICS,
  relationSemantic,
  type GraphLayoutMode,
  type GraphRelationRow,
  type GraphViewMode,
  type RelationSemantic,
} from './graphPresentation'

function RelationInspector({ edge }: { edge: GraphRelationRow }) {
  return (
    <Descriptions size="small" column={1}>
      <Descriptions.Item label="源资源">{edge.sourceName}</Descriptions.Item>
      <Descriptions.Item label="关系">{edge.label} →</Descriptions.Item>
      <Descriptions.Item label="目标资源">{edge.targetName}</Descriptions.Item>
      <Descriptions.Item label="方向说明">箭头表示源资源指向目标资源</Descriptions.Item>
      <Descriptions.Item label="事实/推断">{edge.factStatus === 'fact' ? '事实' : '推断（疑似导致）'}</Descriptions.Item>
      <Descriptions.Item label="是否传播故障">{edge.propagatesFailure ? '是' : '否'}</Descriptions.Item>
      <Descriptions.Item label="权威字段">{edge.sourceRef || '未提供'}</Descriptions.Item>
      <Descriptions.Item label="同步时间">{edge.syncedAt || '未提供'}</Descriptions.Item>
      <Descriptions.Item label="原始关系类型">{edge.relationType}</Descriptions.Item>
    </Descriptions>
  )
}

export default function GraphExplorer({ subgraph }: { subgraph: GraphSubgraph }) {
  const [mode, setMode] = useState<GraphViewMode>('resource-relations')
  const [layout, setLayout] = useState<GraphLayoutMode>(layoutForMode('resource-relations'))
  const [selectedEdge, setSelectedEdge] = useState<GraphRelationRow | null>(null)
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null)
  const [enabledSemantics, setEnabledSemantics] = useState<Set<RelationSemantic>>(new Set(RELATION_SEMANTICS))
  const [showContextNodes, setShowContextNodes] = useState(false)
  const [hideOrphans, setHideOrphans] = useState(false)
  const [inspectorOpen, setInspectorOpen] = useState(false)

  const baseModel = useMemo(
    () => buildGraphDisplayModel(subgraph, {
      mode,
      centerUid: subgraph.center_entity_uid,
      maxNodes: 80,
      maxEdges: 200,
      ...(showContextNodes ? { expandedContextUids: new Set(subgraph.vertices.filter((vertex) => isContextEntityType(vertex.entity_type)).map((vertex) => vertex.entity_uid)) } : {}),
    }),
    [mode, showContextNodes, subgraph],
  )

  const display = useMemo(() => {
    const filtered = filterBySemantics(baseModel, enabledSemantics)
    const focal = focusGraph(filtered, { nodeId: selectedNodeId, edgeId: selectedEdge?.id ?? null })
    if (!hideOrphans) return focal
    const connected = new Set<string>()
    focal.edges.forEach((edge) => { connected.add(edge.source); connected.add(edge.target) })
    return { ...focal, nodes: focal.nodes.filter((node) => node.aggregate || node.id === subgraph.center_entity_uid || connected.has(node.id)) }
  }, [baseModel, enabledSemantics, hideOrphans, selectedEdge, selectedNodeId, subgraph.center_entity_uid])

  const toggleSemantic = (semantic: RelationSemantic) => {
    setEnabledSemantics((current) => {
      const next = new Set(current)
      if (next.has(semantic)) next.delete(semantic)
      else next.add(semantic)
      return next
    })
  }

  const clearFocus = () => { setSelectedNodeId(null); setSelectedEdge(null) }

  return (
    <section aria-label="专家关系探索" className="graph-explorer">
      <div className="section-heading">
        <div>
          <h3 style={{ margin: 0 }}>关系探索</h3>
          <span className="page-desc">默认展示资源关系；故障链与专家模式共享同一份原始图谱数据</span>
        </div>
        <GraphToolbar
          mode={mode}
          layout={layout}
          onModeChange={(next) => { setMode(next); setLayout(layoutForMode(next)); setSelectedEdge(null); setSelectedNodeId(null) }}
          onLayoutChange={setLayout}
        />
      </div>
      <GraphLegend />
      <div className="graph-semantic-filters" role="group" aria-label="关系语义筛选">
        {RELATION_SEMANTICS.map((semantic) => (
          <Button
            key={semantic}
            size="small"
            type={enabledSemantics.has(semantic) ? 'primary' : 'default'}
            aria-pressed={enabledSemantics.has(semantic)}
            onClick={() => toggleSemantic(semantic)}
          >
            {RELATION_SEMANTIC_LABELS[semantic]}
          </Button>
        ))}
        <Button size="small" aria-pressed={showContextNodes} onClick={() => setShowContextNodes((value) => !value)}>
          {showContextNodes ? '折叠 Namespace/Node' : '展开 Namespace/Node'}
        </Button>
        <Button size="small" aria-pressed={hideOrphans} onClick={() => setHideOrphans((value) => !value)}>
          {hideOrphans ? '显示孤点' : '隐藏孤点'}
        </Button>
        <Button size="small" onClick={clearFocus} disabled={!selectedNodeId && !selectedEdge}>清除聚焦</Button>
      </div>
      {display.edges.length === 0 ? (
        <DataState kind="empty" title="当前筛选没有匹配的关系" description="调整上方关系语义筛选或展开上下文节点后重试" />
      ) : (
        <GraphMap
          subgraph={subgraph}
          mode={mode}
          layoutOverride={layout}
          centerUid={subgraph.center_entity_uid}
          maxNodes={80}
          maxEdges={200}
          focusedNodeId={selectedNodeId}
          focusedEdgeId={selectedEdge?.id ?? null}
          {...(showContextNodes ? { expandedContextUids: new Set(subgraph.vertices.filter((vertex) => isContextEntityType(vertex.entity_type)).map((vertex) => vertex.entity_uid)) } : {})}
          onNodeSelect={(nodeId) => { setSelectedNodeId(nodeId); setSelectedEdge(null) }}
          onEdgeSelect={(edgeId) => { setSelectedEdge(display.relationRows.find((row) => row.id === edgeId) ?? null); setSelectedNodeId(null) }}
          onCanvasSelect={clearFocus}
        />
      )}
      <div style={{ marginTop: 12 }}>
        <GraphRelationList
          rows={display.relationRows}
          totalEdges={subgraph.total_edges}
          aggregated={subgraph.aggregated}
          onLocate={(edgeId) => { setSelectedEdge(display.relationRows.find((row) => row.id === edgeId) ?? null); setSelectedNodeId(null) }}
        />
      </div>
      <Button className="graph-inspector-trigger" disabled={!selectedEdge} onClick={() => setInspectorOpen(true)}>查看关系</Button>
      {selectedEdge && (
        <div className="graph-inline-inspector">
          <Card size="small" title="关系检查器" style={{ marginTop: 12 }} extra={<Tag>{selectedEdge.factStatus === 'fact' ? '事实' : '推断'}</Tag>}>
            <RelationInspector edge={selectedEdge} />
          </Card>
        </div>
      )}
      <Drawer title="关系检查器" open={inspectorOpen} onClose={() => setInspectorOpen(false)} width="min(420px, 100vw)">
        {selectedEdge ? <RelationInspector edge={selectedEdge} /> : <DataState kind="empty" compact title="请选择一条关系" />}
      </Drawer>
      <div style={{ marginTop: 16 }}>
        <DependencyChain vertices={subgraph.vertices} edges={subgraph.edges} centerUid={subgraph.center_entity_uid} mode={mode} />
      </div>
    </section>
  )
}
