import React from 'react'
import BaseServiceMapView from '../../../components/graph/ServiceMapView'
import type { PanoramaGroupEdge, ServiceMapResponse } from '../../../api/knowledgeGraph'

/** Page-level adapter: the panorama owns selection state, the graph component owns rendering. */
export default function ServiceMapView({ data, selectedService, onServiceSelect, onGroupEdgeSelect }: {
  data?: ServiceMapResponse
  selectedService?: string
  onServiceSelect?: (serviceName: string) => void
  onGroupEdgeSelect?: (edge: PanoramaGroupEdge) => void
}) {
  return <BaseServiceMapView data={data} selectedService={selectedService} onServiceSelect={onServiceSelect} onGroupEdgeSelect={onGroupEdgeSelect} />
}
