import React from 'react'
import BaseServiceDependencyView from '../../../components/graph/ServiceDependencyView'
import type { GraphEntity } from '../../../api/graphContracts'
import type { ServiceDependenciesResponse } from '../../../api/knowledgeGraph'

export default function ServiceDependencyView({ data, onEntitySelect }: {
  data?: ServiceDependenciesResponse
  onEntitySelect?: (entity: GraphEntity) => void
}) {
  return <BaseServiceDependencyView data={data} onEntitySelect={onEntitySelect} />
}
