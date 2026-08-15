import api from './client'

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

// destructive 动作需 approval_task_id（后端 DESTRUCTIVE_ACTIONS）
export const K8S_DESTRUCTIVE: K8sActionName[] = ['delete_pod', 'evict_pod', 'cordon', 'drain']

export interface K8sPreflightResult {
  ok?: boolean
  error?: string
  preflight_token?: string
  resource_version?: string
  command?: string
  category?: string
}

export interface K8sExecuteResult {
  ok?: boolean
  output?: string
}

export interface K8sActionPayload {
  action: string
  kind: string
  namespace: string
  name: string
  extra?: Record<string, unknown>
  preflight_token?: string
  expected_resource_version?: string
  approval_task_id?: string
}

// 预检：dry-run 生成一次性 token + resourceVersion（乐观锁依据）
export const k8sPreflight = (data: Omit<K8sActionPayload, 'preflight_token' | 'expected_resource_version' | 'approval_task_id'>) =>
  api.post<K8sPreflightResult>('/ops/k8s/preflight', data)

// 执行：必须携带 preflight_token + expected_resource_version；destructive 需 approval_task_id
export const k8sExecute = (data: K8sActionPayload) =>
  api.post<K8sExecuteResult>('/ops/k8s/execute', data)

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
