import React from 'react'
import type { GraphSubgraph } from '../../api/graphContracts'
import GraphMap from './GraphMap'
import DependencyChain from './DependencyChain'

export default function GraphExplorer({ subgraph }: { subgraph: GraphSubgraph }) {
  return <section aria-label="专家关系探索"><h3>专家关系探索</h3><GraphMap subgraph={subgraph} /><div style={{ marginTop: 16 }}><DependencyChain vertices={subgraph.vertices} edges={subgraph.edges} /></div></section>
}
