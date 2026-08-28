import React from 'react'
import { Button, Card } from 'antd'
import GraphExplorer from '../../../components/graph/GraphExplorer'
import ImpactTree from '../../../components/graph/ImpactTree'
import type { GraphSubgraph } from '../../../api/graphContracts'

export default function ServiceExploreView({ graph, impactGraph, onRefresh }: { graph: GraphSubgraph; impactGraph: GraphSubgraph; onRefresh: () => void }) {
  return <Card title="专家关系探索" extra={<Button size="small" onClick={onRefresh}>刷新结构</Button>}>
    <GraphExplorer subgraph={graph} />
    <div style={{ marginTop: 16 }}><ImpactTree subgraph={impactGraph} /></div>
  </Card>
}
