import api, { type ActionProjection } from './client'

// ===== K8s 结构化运维动作（工作流 C4，端点已上线：/api/v1/ops/k8s/*）=====

export type K8sActionName =
  | 'rollout_restart' | 'scale' | 'delete_pod' | 'evict_pod'
  | 'cordon' | 'uncordon' | 'drain'

export const K8S_ACTIONS: K8sActionName[] = [
  'rollout_restart', 'scale', 'delete_pod', 'evict_pod', 'cordon', 'uncordon', 'drain',
]

// 动作 → 允许的 kind（后端 k8s_actions.ACTION_KINDS 一致）
export const K8S_ACTION_KINDS: Record<K8sActionName, string[]> = {
  rollout_restart: ['deployment', 'statefulset', 'daemonset'],
  scale: ['deployment', 'statefulset'],
  delete_pod: ['pod'],
  evict_pod: ['pod'],
  cordon: ['node'],
  uncordon: ['node'],
  drain: ['node'],
}

export interface K8sActionProposalRequest {
  idempotency_key: string
  cluster_id: string
  resource_type: string
  namespace: string
  target_name: string
  operation: K8sActionName
  params: Record<string, unknown>
}

export type K8sActionProjection = ActionProjection & {
  run_status?: string
  created?: boolean
}

export interface K8sActionExecuteResult {
  action_id: string
  status: string
  message?: string
  error_code?: string
}

// Canonical Action boundary: proposal persistence, approval and execution all
// use the same query-api/MySQL owner. No legacy /ops/k8s token is accepted.
export const createK8sActionProposal = (data: K8sActionProposalRequest) =>
  api.post<K8sActionProjection>('/ai/actions', data)

export const getAiAction = (actionId: string) =>
  api.get<K8sActionProjection>(`/ai/actions/${encodeURIComponent(actionId)}`)

export const decideAiAction = (actionId: string, body: {
  decision: 'approved' | 'rejected'
  reason?: string
  idempotency_key: string
  action_version: number
}) => api.post(`/ai/actions/${encodeURIComponent(actionId)}/decision`, body)

export const executeAiAction = (actionId: string) =>
  api.post<K8sActionExecuteResult>(`/ai/actions/${encodeURIComponent(actionId)}/execute`)

// ===== 只读资源列表（query-api 现有端点，复用其 kubectl 只读通道）=====

export interface K8sNamespaceItem { name: string; status: string }
export interface K8sPodItem { name: string; namespace: string; status: string; restarts?: number }
export interface K8sDeploymentItem { name: string; namespace: string; replicas?: number; ready?: number }
export interface K8sNodeItem { name: string; status: string; cpu?: string; memory?: string; version?: string }

// GET /infrastructure/namespaces → { namespaces: [...] }（错误时 { namespaces: [], error }）
export const listK8sNamespaces = () =>
  api.get<{ namespaces?: K8sNamespaceItem[]; error?: string }>('/infrastructure/namespaces')

// GET /infrastructure/pods?namespace= → { pods: [...] }（namespace=all 或省略返回全部）
export const listK8sPods = (namespace?: string) =>
  api.get<{ pods?: K8sPodItem[]; error?: string }>('/infrastructure/pods', {
    params: { namespace: namespace || 'all' },
  })

// GET /infrastructure/deployments?namespace= → { deployments: [...] }
export const listK8sDeployments = (namespace?: string) =>
  api.get<{ deployments?: K8sDeploymentItem[]; error?: string }>('/infrastructure/deployments', {
    params: { namespace: namespace || 'all' },
  })

// GET /infrastructure/nodes → { nodes: [...] }（集群级，不带 namespace）
export const listK8sNodes = () =>
  api.get<{ nodes?: K8sNodeItem[]; error?: string }>('/infrastructure/nodes')
