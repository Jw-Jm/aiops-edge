export type GraphEntityType =
  | 'business' | 'application' | 'service' | 'middleware' | 'k8s_cluster' | 'namespace' | 'k8s_node'
  | 'deployment' | 'replicaset' | 'statefulset' | 'daemonset' | 'job' | 'cronjob' | 'pod' | 'container' | 'k8s_service' | 'ingress'
  | 'endpoint_slice' | 'pvc' | 'pv' | 'storage_class' | 'nad' | 'network' | 'data_volume' | 'disk_device' | 'volume' | 'virtual_interface' | 'cni' | 'vm' | 'vmi' | 'migration'
  | 'physical_server' | 'cpu' | 'dimm' | 'nic' | 'disk' | 'mainboard' | 'bmc' | 'psu' | 'fan'
  | 'switch' | 'switch_port' | 'alert' | 'change' | 'case' | 'sel_event' | string

export type GraphEntityTypeKnown =
  | 'business' | 'application' | 'service' | 'middleware' | 'k8s_cluster' | 'namespace' | 'k8s_node'
  | 'deployment' | 'replicaset' | 'statefulset' | 'daemonset' | 'job' | 'cronjob' | 'pod' | 'container' | 'k8s_service' | 'ingress'
  | 'endpoint_slice' | 'pvc' | 'pv' | 'storage_class' | 'nad' | 'network' | 'data_volume' | 'disk_device' | 'volume' | 'virtual_interface' | 'cni' | 'vm' | 'vmi' | 'migration'
  | 'physical_server' | 'cpu' | 'dimm' | 'nic' | 'disk' | 'mainboard' | 'bmc' | 'psu' | 'fan'
  | 'switch' | 'switch_port' | 'alert' | 'change' | 'case' | 'sel_event'

export interface GraphEntity {
  entity_uid: string
  entity_type: GraphEntityType
  tenant_id: string
  cluster_id: string
  namespace?: string
  name: string
  name_key: string
  source: string
  source_uid?: string
  status: string
  health?: string
  resolution?: string
  confidence: number
  first_seen_ms?: number
  last_seen_ms?: number
  generation: number
  attrs_version: number
  attrs?: Record<string, unknown>
}

export interface GraphEdge {
  edge_uid: string
  source_uid: string
  target_uid: string
  relation_type: string
  tenant_id: string
  cluster_id: string
  status: string
  source: string
  confidence: number
  generation: number
  first_seen_ms?: number
  last_seen_ms?: number
  valid_from_ms?: number
  valid_to_ms?: number
  propagates_failure: boolean
  candidate_direction: string
  impact_direction: string
  attrs_version: number
  attrs?: Record<string, unknown>
}

export interface GraphEdgeView {
  id: string
  source: string
  target: string
  relation: string
  relationLabel: string
  factStatus: 'fact' | 'inferred'
  sourceRef: string
  syncedAt: string
  aggregateCount?: number
}

export interface GraphMeta {
  contract_version: 'graph-dto-v1' | string
  schema_version: number
  partial: boolean
  stale: boolean
  generated_at: string
  warning_codes: string[]
}

export interface GraphSubgraph {
  center_entity_uid: string
  vertices: GraphEntity[]
  edges: GraphEdge[]
  total_nodes?: number
  total_edges?: number
  next_cursor?: string
  aggregated?: boolean
  meta: GraphMeta
}

export interface GraphPage extends GraphSubgraph {
  total_nodes: number
  total_edges: number
  aggregated: boolean
}

export interface GraphErrorResponse {
  error: { code: string; message: string; request_id?: string }
}

export interface GraphHealth {
  ready: boolean
  backend: string
  schema_version: number
  schema_checksum?: string
  error_code?: string
}
