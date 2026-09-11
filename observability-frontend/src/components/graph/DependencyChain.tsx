import React from 'react'
import { Tag } from 'antd'
import type { GraphEdge, GraphEntity } from '../../api/graphContracts'
import DataState from '../display/DataState'
import { buildRelationChains, edgeFactStatus, edgeStyle, relationLabel, type GraphRelationRow, type GraphViewMode } from './graphPresentation'

export interface DependencyChainProps {
  vertices: GraphEntity[]
  edges: GraphEdge[]
  centerUid?: string
  mode?: GraphViewMode
}

/**
 * 关键关系链：逐行显示「源资源 ─关系→ 目标资源」。
 *
 * 每一行都来自一条真实 edge（含 edge id），不使用 vertices 顺序拼箭头，
 * 因此不会把互不相连的节点串成一条虚构依赖主链。
 */
export default function DependencyChain({ vertices, edges, centerUid = '', mode = 'resource-relations' }: DependencyChainProps) {
  const names = new Map(vertices.map((vertex) => [vertex.entity_uid, vertex.name]))
  const rows: GraphRelationRow[] = edges.map((edge) => ({
    id: edge.edge_uid,
    source: edge.source_uid,
    target: edge.target_uid,
    sourceUid: edge.source_uid,
    targetUid: edge.target_uid,
    sourceName: names.get(edge.source_uid) ?? edge.source_uid,
    targetName: names.get(edge.target_uid) ?? edge.target_uid,
    relationType: edge.relation_type,
    label: relationLabel(edge.relation_type),
    style: edgeStyle(edge, mode),
    propagatesFailure: edge.propagates_failure === true,
    factStatus: edgeFactStatus(edge),
    sourceRef: typeof edge.attrs?.source_field === 'string' ? edge.attrs.source_field : edge.source,
    syncedAt: typeof edge.attrs?.synced_at === 'string' ? edge.attrs.synced_at : '',
  }))

  const chains = buildRelationChains(rows, centerUid, mode)

  return (
    <section aria-label="关键关系链">
      <h3 style={{ marginBottom: 8 }}>关键关系链</h3>
      {chains.length === 0 ? (
        <DataState kind="empty" compact title="尚未形成可验证关系链" description="当前视图没有符合条件的事实关系；切换语义筛选或展开上下文后再查看" />
      ) : (
        <ul className="relation-chain-list">
          {chains.map((chain) => (
            <li className="relation-chain-row" data-testid="relation-chain-row" key={chain.id}>
              <span className="relation-chain-row__source">{chain.source}</span>
              <span className="relation-chain-row__arrow" aria-hidden="true">─</span>
              <span className="relation-chain-row__relation">
                {chain.relation}
                <span aria-hidden="true"> →</span>
              </span>
              <span className="relation-chain-row__target">{chain.target}</span>
              {chain.factStatus === 'inferred' ? <Tag color="warning">推断</Tag> : <Tag>事实</Tag>}
            </li>
          ))}
        </ul>
      )}
      <small style={{ color: 'var(--text-muted)' }}>{chains.length} 条真实关系 · 每行可追溯到 edge 与证据</small>
    </section>
  )
}
