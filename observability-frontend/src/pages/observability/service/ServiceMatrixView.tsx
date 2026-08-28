import React from 'react'
import CallMatrix from '../../../components/graph/CallMatrix'
import type { GraphEdge, GraphEntity } from '../../../api/graphContracts'
import type { ServiceMatrixCell, ServiceMatrixResponse } from '../../../api/knowledgeGraph'

export default function ServiceMatrixView({ matrix, vertices, edges, onCellSelect }: {
  matrix?: ServiceMatrixResponse
  vertices?: GraphEntity[]
  edges?: GraphEdge[]
  onCellSelect?: (cell: ServiceMatrixCell) => void
}) {
  return <CallMatrix matrix={matrix} vertices={vertices} edges={edges} onCellSelect={onCellSelect} />
}
