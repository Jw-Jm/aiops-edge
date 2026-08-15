import api from './client'

// =====================================================================
//  AI Workflows（自研 flow_engine，/api/v1/ai/workflows）
//  所有函数复用 client.ts 的共享 axios 实例（baseURL /api/v1，自动注入
//  X-Tenant-ID / Bearer / cluster_id）。路径与后端 flow_api.py 契约一致。
// =====================================================================

export interface NodeConfigField {
  name: string
  label?: string
  type?: string          // text | textarea | number | select | boolean
  required?: boolean
  default?: unknown
  options?: string[] | Array<{ label: string; value: string }>
}

export interface NodeSpec {
  type: string
  kind: string           // trigger | action | control | data
  category: string
  label: string
  ports: Array<string | { name: string; direction?: string; [k: string]: unknown }>
  config_fields?: NodeConfigField[]
  output_shape?: unknown
}

export interface FlowNodeWire {
  id: string
  type: string
  name: string
  config: Record<string, unknown>
  position: { x: number; y: number }
}

export interface FlowEdgeWire {
  id: string
  source: string
  sourcePort: string
  target: string
}

export interface FlowGraphWire {
  nodes: FlowNodeWire[]
  edges: FlowEdgeWire[]
}

export interface FlowItem {
  id: string
  name: string
  description?: string
  enabled?: boolean
  version?: number
  graph?: FlowGraphWire
  created_at?: string
  updated_at?: string
}

export interface RunNodeItem {
  run_id?: string
  node_id: string
  node_type?: string
  node_name?: string
  status?: string
  input_json?: string
  output_json?: string
  fired_port?: string
  error?: string
}

export interface FlowRunItem {
  run_id: string
  flow_id?: string
  flow_version?: number
  status: string
  trigger_type?: string
  trigger_json?: string
  context_json?: string
  error?: string
  created_at?: string
  nodes?: RunNodeItem[]
}

// ===== 列表 / CRUD =====
export const listWorkflows = () => api.get('/ai/workflows')

export const createWorkflow = (data: { name: string; enabled?: boolean; graph: FlowGraphWire }) =>
  api.post('/ai/workflows', data)

export const getWorkflow = (id: string) => api.get(`/ai/workflows/${encodeURIComponent(id)}`)

export const updateWorkflow = (id: string, data: Partial<FlowItem> & { graph?: FlowGraphWire }) =>
  api.put(`/ai/workflows/${encodeURIComponent(id)}`, data)

export const deleteWorkflow = (id: string) => api.delete(`/ai/workflows/${encodeURIComponent(id)}`)

export const toggleWorkflow = (id: string) => api.post(`/ai/workflows/${encodeURIComponent(id)}/toggle`)

// ===== 运行 =====
export const runWorkflow = (id: string, body: { trigger?: Record<string, unknown>; message?: string; service?: string }) =>
  api.post(`/ai/workflows/${encodeURIComponent(id)}/run`, body)

export const listFlowRuns = (id: string) => api.get(`/ai/workflows/${encodeURIComponent(id)}/runs`)

export const getFlowRun = (id: string, runId: string) =>
  api.get(`/ai/workflows/${encodeURIComponent(id)}/runs/${encodeURIComponent(runId)}`)

export const resumeFlowRun = (id: string, runId: string, approved: boolean) =>
  api.post(`/ai/workflows/${encodeURIComponent(id)}/runs/${encodeURIComponent(runId)}/resume`, { approved })

// ===== 编辑器辅助 =====
export const listWorkflowNodeTypes = () => api.get('/ai/workflows/node-types')

export const generateWorkflow = (data: { prompt: string }) => api.post('/ai/workflows/generate', data)

export const testFlowNode = (data: { type: string; config?: Record<string, unknown>; trigger?: Record<string, unknown> }) =>
  api.post('/ai/workflows/test-node', data)

// ===== 运行状态文本（各页面共用）=====
export const runStatusText: Record<string, string> = {
  succeeded: '已成功', failed: '已失败', running: '执行中', pending: '等待中',
  waiting_approval: '待审批', cancelled: '已取消',
}

export function runStatusTone(status?: string): 'ok' | 'crit' | 'info' | 'warn' | 'muted' {
  if (status === 'succeeded' || status === 'success' || status === 'completed') return 'ok'
  if (status === 'failed' || status === 'error') return 'crit'
  if (status === 'running' || status === 'pending') return 'info'
  if (status === 'waiting_approval') return 'warn'
  return 'muted'
}

// 后端节点输出存于 output_json（JSON 字符串），安全解析。
export function parseRunOutput(rn: RunNodeItem | undefined | null): unknown {
  const raw = rn?.output_json
  if (raw === undefined || raw === null || raw === '') return undefined
  if (typeof raw === 'string') {
    try {
      return JSON.parse(raw)
    } catch {
      return { raw }
    }
  }
  return raw
}
