import type { ActionProjection } from '../../api/client'
import { resourceDomainOf, resourceLocation, resourceTypeLabel } from '../../features/resources/resourceDomain'

export function canDecideAction(role: string): boolean {
  return role === 'approver' || role === 'admin'
}

const CONTROLLED_TYPES = new Set(['physical_server', 'k8s_node', 'deployment', 'replicaset', 'statefulset', 'daemonset', 'pod', 'container', 'k8s_service', 'workload'])
const CAPABILITY_ALIASES: Record<string, string[]> = {
  physical_server: ['physical_server', 'hardware'],
  k8s_node: ['k8s_node', 'kubernetes.node', 'node'],
  deployment: ['workload', 'deployment'], replicaset: ['workload', 'replicaset'], statefulset: ['workload', 'statefulset'],
  daemonset: ['workload', 'daemonset'], pod: ['workload', 'pod'], container: ['workload', 'container'], k8s_service: ['workload', 'service'], workload: ['workload'],
}

/** Client-side visibility gate only; the server remains the authorization authority. */
export function canControlResource(resourceType: string, capabilities: string[]): boolean {
  if (!CONTROLLED_TYPES.has(resourceType) || !Array.isArray(capabilities) || capabilities.length === 0) return false
  const aliases = CAPABILITY_ALIASES[resourceType] ?? [resourceType]
  return capabilities.some((capability) => {
    const value = String(capability).toLowerCase()
    return aliases.some((alias) => value.includes(alias)) && /(action|execute|mutat|write|control|restart|approve)/i.test(value)
  })
}

export function toActionViewModel(action: ActionProjection & { risk_score?: number; risk_level?: string; impact_summary?: string; approval_status?: string; verification_status?: string; rollback_summary?: string }) {
  const riskLevel = action.risk_level || (action.risk_score == null ? 'unknown' : String(action.risk_score))
  const resourceDomain = resourceDomainOf(action.target_resource_type as never)
  const targetName = action.namespace ? `${action.namespace} / ${action.target_name}` : action.target_name
  return {
    actionId: action.action_id,
    runId: action.run_id,
    runHref: `/investigation/${action.run_id}`,
    operation: action.operation,
    target: resourceDomain ? `${resourceTypeLabel(action.target_resource_type)} · ${targetName}` : targetName,
    targetType: action.target_resource_type,
    targetUid: action.target_uid,
    targetDomain: resourceDomain,
    targetLocation: resourceDomain ? resourceLocation({ clusterId: action.cluster_id || '', uid: action.target_uid, type: action.target_resource_type, domain: resourceDomain, name: action.target_name, ...(action.namespace ? { namespace: action.namespace } : {}) }) : targetName,
    risk: { level: riskLevel, label: riskLevel === 'unknown' ? '未评估' : riskLevel },
    impact: action.impact_summary || '未提供',
    approvalStatus: action.approval_status || (action.status === 'proposed' ? '待审批' : action.status),
    executionStatus: action.execution_status || '未执行',
    verificationStatus: action.verification_status || '未验证',
    preflightStatus: action.preflight_status,
    resourceVersion: action.resource_version,
    actionVersion: action.action_version,
    actionHash: action.action_hash,
    rollback: action.rollback_summary || '未提供',
  }
}
