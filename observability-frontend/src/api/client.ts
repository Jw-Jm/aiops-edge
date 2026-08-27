import axios from 'axios'

// Canonical tenant identity is tracked in source so a GitHub checkout and the
// local build produce the same request context even when .env is absent.
// VITE_TENANT_ID remains an explicit deployment override, never a legacy alias.
const DEFAULT_TENANT_ID = '7ed01afc-cc79-4ecd-8767-a2befa6168ad'
export const TENANT_ID = (import.meta.env.VITE_TENANT_ID as string) || DEFAULT_TENANT_ID

const api = axios.create({
  baseURL: '/api/v1',
  headers: { 'X-Tenant-ID': TENANT_ID },
  timeout: 15000,
})

// Read token from localStorage on init
const token = localStorage.getItem('token')
if (token) {
  api.defaults.headers.common['Authorization'] = `Bearer ${token}`
}

// 从持久化的 uiStore 读取当前集群选择（'all' = 全部集群），避免与 uiStore 循环依赖。
function readCurrentClusterId(): string {
  try {
    const raw = localStorage.getItem('aiops-ui-v3')
    if (!raw) return 'all'
    const parsed = JSON.parse(raw)
    const cid = parsed?.state?.currentClusterId
    return cid || 'all'
  } catch {
    return 'all'
  }
}

// 全局端点白名单：这些接口与具体集群无关，注入 cluster_id 会静默缩小结果集（见 F3）。
// 仅对集群级端点注入 cluster_id；/clusters 自身也跳过（避免干扰集群 CRUD）。
const GLOBAL_PATHS = [
  '/clusters',
  '/users',
  '/ops/tasks',
  '/ops/audit-logs',
  '/ops/reports',
  '/ops/changes',
  '/node/health',
  '/ipmi',
  '/settings',
  '/auth',
  '/slo',
  '/ai/sessions',
  '/ai/session',
  '/ai/knowledge',
  '/ai/runs',
  '/ai/actions',
  '/ai/skills',
  '/ai/workflows',
  '/ai/flows',
  '/mcp',
  '/grafana',
  '/system',
]

// Request interceptor: set Authorization header + 多集群过滤参数
api.interceptors.request.use((config) => {
  const t = localStorage.getItem('token')
  if (t) {
    config.headers.Authorization = `Bearer ${t}`
  }
  const cid = readCurrentClusterId()
  const url = config.url || ''
  const isGlobal = GLOBAL_PATHS.some((p) => url.startsWith(p))
  if (!isGlobal && cid !== 'all') {
    config.params = { ...(config.params || {}), cluster_id: cid }
  }
  return config
})

// Response interceptor: if 401, redirect to /login
api.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response?.status === 401) {
      localStorage.removeItem('token')
      // Only redirect if not already on login page
      if (window.location.pathname !== '/login') {
        window.location.href = '/login'
      }
    }
    return Promise.reject(error)
  }
)

// Services
export const getServices = (params?: Record<string, unknown>) => api.get('/services', { params })
export const getServiceDetail = (name: string, params?: Record<string, unknown>) => api.get(`/services/${name}`, { params })
export const getTraces = (params?: Record<string, unknown>) => api.get('/traces', { params })
export const getTraceDetail = (id: string) => api.get(`/traces/${id}`)
export const getTraceContext = (id: string) => api.get(`/traces/${id}/context`)
export const getTopology = (params?: Record<string, unknown>) => api.get('/topology/global', { params })
export const getTopologyNodeDetail = (name: string, params?: Record<string, unknown>) => api.get(`/topology/node/${encodeURIComponent(name)}`, { params })

// ===== Run 调查（P12：智能调查中心数据源）=====
export interface RunSummary {
  run_id: string
  request_id: string
  status: string
  tenant_id?: string
  created_by?: string | null
  principal_id?: string | null
  principal_type?: string | null
  primary_cluster_id?: string | null
  intent?: string
  action_mode?: string
  target_type?: string | null
  target_resource_id?: string | null
  created_at?: string | null
  root_cause?: string | null
  confidence?: number | null
  plan_steps?: { step_id: string; seq: number; step_type: string; status: string; description?: string }[]
  actions?: { action_id: string; action_type: string; status: string; authoritative_risk?: string; execution_status?: string; target_name?: string; target_uid?: string }[]
  approvals?: { approval_id: string; action_id: string; decision: string; approver?: string; reason?: string }[]
  hypotheses?: { hypothesis_id: string; content: string; confidence: number; status: string; confirmed_by_evidence?: boolean }[]
  latest_action?: { action_id: string; action_type: string; status: string; authoritative_risk?: string; execution_status?: string; target_name?: string; target_uid?: string }
  latest_verification?: { verification_id: string; action_id: string; status: string; summary?: string } | null
  verifications?: { verification_id: string; action_id: string; status: string; summary?: string }[]
}
export interface CreateRunResponse {
  run_id: string
  request_id: string
  status: string
  created_at?: string
}
export interface RunListResponse { runs: RunSummary[] }
export const listRuns = (params?: Record<string, unknown>) =>
  api.get<RunListResponse>('/ai/runs', { params })
export const getRun = (runId: string) => api.get<{ run: RunSummary }>(`/ai/runs/${encodeURIComponent(runId)}`)
export const createRun = (data: Record<string, unknown>) =>
  api.post<CreateRunResponse>('/ai/runs', data)

// ===== Canonical Action approval/read model =====
export interface ActionProjection {
  action_id: string
  run_id: string
  cluster_id?: string
  action_type: string
  action_hash: string
  hash_schema_version: number
  action_version: number
  policy_version?: string
  preflight_status: string
  target_resource_type: string
  status: 'proposed' | 'approved' | 'rejected' | string
  dry_run: boolean
  target_name: string
  target_uid: string
  resource_version: string
  namespace: string
  operation: string
  execution_status: string
  params?: Record<string, unknown>
  error_code?: string
  created_at?: string
  updated_at?: string
}
export const listActions = (params?: { status?: string; limit?: number }) =>
  api.get<{ actions: ActionProjection[]; count: number }>('/ai/actions', { params })
export const getAction = (actionId: string) =>
  api.get<ActionProjection>(`/ai/actions/${encodeURIComponent(actionId)}`)
export const decideAction = (actionId: string, body: {
  decision: 'approved' | 'rejected'
  reason?: string
  idempotency_key: string
  action_version: number
}) => api.post(`/ai/actions/${encodeURIComponent(actionId)}/decision`, body)

export interface RunEvent {
  sequence?: number
  event_id?: string
  event_type?: string
  payload?: unknown
  error?: string
}

// 公共 Run SSE 需要 JWT，不能使用原生 EventSource（无法附加 Authorization）。
export async function streamRunEvents(
  runId: string,
  onEvent: (event: RunEvent) => void,
  signal?: AbortSignal,
  options?: { afterSequence?: number; maxReconnects?: number },
): Promise<void> {
  const auth = localStorage.getItem('token')
  let afterSequence = options?.afterSequence ?? 0
  let lastEventId = afterSequence > 0 ? String(afterSequence) : ''
  const maxReconnects = Math.max(0, options?.maxReconnects ?? 5)
  let reconnects = 0
  while (true) {
    const query = afterSequence > 0 ? `?after_sequence=${afterSequence}` : ''
    const response = await fetch(`/api/v1/ai/runs/${encodeURIComponent(runId)}/events${query}`, {
      headers: {
        Accept: 'text/event-stream',
        ...(lastEventId ? { 'Last-Event-ID': lastEventId } : {}),
        ...(auth ? { Authorization: `Bearer ${auth}` } : {}),
      },
      signal,
    })
    if (!response.ok || !response.body) {
      if (reconnects++ >= maxReconnects) throw new Error(`Run SSE failed: ${response.status}`)
      await new Promise((resolve) => setTimeout(resolve, Math.min(1000 * 2 ** reconnects, 8000)))
      continue
    }
    const reader = response.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''
    while (true) {
      const { value, done } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      const frames = buffer.split('\n\n')
      buffer = frames.pop() ?? ''
      for (const frame of frames) {
        const frameId = frame.split('\n').find((line) => line.startsWith('id:'))?.slice(3).trim()
        if (frameId && /^\d+$/.test(frameId)) {
          const sequence = Number(frameId)
          if (sequence > afterSequence) {
            afterSequence = sequence
            lastEventId = frameId
          }
        }
        const data = frame.split('\n').find((line) => line.startsWith('data:'))?.slice(5).trim()
        if (!data) continue
        try {
          const event = JSON.parse(data) as RunEvent
          if (typeof event.sequence === 'number' && event.sequence > afterSequence) {
            afterSequence = event.sequence
            lastEventId = String(event.sequence)
          }
          onEvent(event)
        } catch { /* ignore comments/heartbeats */ }
      }
    }
    // A clean close is still reconnectable until the bounded budget is spent;
    // Last-Event-ID/after_sequence prevents replay gaps after disconnects.
    if (signal?.aborted) return
    if (reconnects++ >= maxReconnects) return
    await new Promise((resolve) => setTimeout(resolve, Math.min(1000 * 2 ** reconnects, 8000)))
  }
}

// ===== Evidence Detail（tenant+cluster+run 三元授权，只读）=====
export interface RunEvidence {
  id: string
  type: string
  source: string
  reliability: number | string
  fact: string
  [k: string]: unknown
}
export const listRunEvidences = (runId: string, params: { tenant_id: string; cluster_id: string }) =>
  api.get<{ run_id: string; evidences: RunEvidence[]; count: number }>(`/ai/runs/${encodeURIComponent(runId)}/evidences`, { params })
export const getRunEvidence = (runId: string, evidenceId: string, params: { tenant_id: string; cluster_id: string }) =>
  api.get<{ evidence: RunEvidence }>(`/ai/runs/${encodeURIComponent(runId)}/evidences/${encodeURIComponent(evidenceId)}`, { params })

// ===== C2-4：Run Tool Activity（真实 ai_tool_runs，不推断冒充）=====
export interface RunTool {
  tool_run_id: string
  step_id?: string
  tool_name: string
  status: string
  result_quality?: string
  executor_id?: string
  lease_epoch_at_start?: number
  eligible_for_evidence?: boolean
  result_digest_sha256?: string
  result_truncated?: boolean
  result_count?: number
  error_message?: string
}
export const listRunTools = (runId: string) =>
  api.get<{ tools: RunTool[]; total: number }>(`/ai/runs/${encodeURIComponent(runId)}/tools`)

// ===== Chat Sessions =====
export const getSession = (sid: string) => api.get(`/ai/session/${sid}`)

// ===== AI Skills / Agents =====
export const listSkills = () => api.get('/ai/skills')
export const getSkill = (key: string) => api.get(`/ai/skills/${encodeURIComponent(key)}`)
export const executeSkill = (key: string, params: Record<string, unknown>) => api.post(`/ai/skills/${encodeURIComponent(key)}/execute`, { params })
export const listAgents = () => api.get('/ai/agents')
export const getAgent = (name: string) => api.get(`/ai/agents/${encodeURIComponent(name)}`)
export const createAgent = (data: Record<string, unknown>) => api.post('/ai/agents', data)
export const updateAgent = (name: string, data: Record<string, unknown>) => api.put(`/ai/agents/${encodeURIComponent(name)}`, data)
export const deleteAgent = (name: string) => api.delete(`/ai/agents/${encodeURIComponent(name)}`)

// ===== AI Workflows =====
export const listFlows = () => api.get('/ai/flows')
export const getFlow = (key: string) => api.get(`/ai/flows/${encodeURIComponent(key)}`)
export const runFlow = (key: string, params: Record<string, unknown>) => api.post(`/ai/flows/${encodeURIComponent(key)}/run`, params)

// ===== Task Approval =====
export const approveTask = (id: string) => api.post(`/ops/tasks/${id}/approve`)
// reject 可选携带 reason 写入审计；不传时保持原行为（向后兼容）
export const rejectTask = (id: string, reason?: string) =>
  api.post(`/ops/tasks/${id}/reject`, reason ? { reason } : undefined)
export const listApprovalTasks = (params?: Record<string, unknown>) => api.get('/ops/tasks', { params })
export const genRecoveryPlan = (data: Record<string, unknown>) => api.post('/ops/recovery/plan', data)
export const getRecoveryPolicy = () => api.get('/ops/recovery/policy')
export const saveRecoveryPolicy = (data: Record<string, unknown>) => api.put('/ops/recovery/policy', data)

// ===== 审计日志 =====
export const listAuditLogs = (params?: Record<string, unknown>) => api.get('/ops/audit-logs', { params })

// ===== 规则管理 =====
export const listRules = () => api.get('/ai/rules')
export const saveRule = (data: Record<string, unknown>) => api.post('/ai/rules', data)
export const deleteRule = (ruleKey: string) => api.delete(`/ai/rules/${encodeURIComponent(ruleKey)}`)
export const toggleRule = (ruleKey: string) => api.post(`/ai/rules/${encodeURIComponent(ruleKey)}/toggle`)

export const chatWithAI = (data: Record<string, unknown>) =>
  api.post('/ai/chat', data, {
    timeout: 120000,          // LLM analysis takes 30-90s
    responseType: 'text',     // backend returns text/markdown, not JSON
  })

// LLM Settings
export const getLLMSettings = () => api.get('/settings/llm')
export const getLLMAdminConfig = () => api.get('/settings/llm/config')
export const saveLLMSettings = (data: Record<string, unknown>) => api.post('/settings/llm', data)
export const testLLMConnection = (data: Record<string, unknown>) => api.post('/settings/llm/test', data)
export const listLLMModels = (data: Record<string, unknown>) => api.post('/settings/llm/models', data)
export const listLLMProviders = () => api.get('/settings/llm/providers')
export const createLLMProvider = (data: Record<string, unknown>) => api.post('/settings/llm/providers', data)
export const updateLLMProvider = (id: number, data: Record<string, unknown>) => api.put(`/settings/llm/providers/${id}`, data)
export const deleteLLMProvider = (id: number) => api.delete(`/settings/llm/providers/${id}`)
export const enableLLMProvider = (id: number) => api.post(`/settings/llm/providers/${id}/enable`)

// Auth
export const login = (username: string, password: string) => api.post('/auth/login', { username, password })
export const changePassword = (data: { current_password: string; new_password: string; confirm_password: string }) =>
  api.post('/auth/change-password', data)

// ===== 需求2/3: aichat 内嵌审批——确认执行处置命令（AI 建议或用户自定义）=====
export const executeSuggestion = (data: {
  thread_id?: string; script: string; service?: string; context?: string; approved?: boolean
}) => api.post('/ai/suggestion/execute', data)
// 最终版本报告：LLM 长耗时生成，覆盖默认 15s 超时为 180s
export const finalReport = (data: { session_id?: string; service?: string }) =>
  api.post('/ai/final_report', data, { timeout: 180000 })

// Alert Rules
export const getAlertRules = () => api.get('/alerts/rules')
export const createAlertRule = (data: Record<string, unknown>) => api.post('/alerts/rules', data)
export const updateAlertRule = (id: string, data: Record<string, unknown>) => api.put(`/alerts/rules/${id}`, data)
export const deleteAlertRule = (id: string) => api.delete(`/alerts/rules/${id}`)

// Alert Events
export interface DashboardAlertEvent {
  id: string | number
  rule_name?: string; service?: string; severity?: string; message?: string
  status?: string; count?: number; first_timestamp?: string; last_timestamp?: string
}
export interface DashboardAlertResponse { data?: DashboardAlertEvent[]; events?: DashboardAlertEvent[]; total?: number }
export const getAlertEvents = (params?: Record<string, unknown>) => api.get<DashboardAlertResponse | DashboardAlertEvent[]>('/alerts/events', { params })
export const getAlertEventByID = (id: string) => api.get(`/alerts/events/${id}`)
export const ackAlertEvent = (id: string) => api.post(`/alerts/events/${id}/ack`)
export const resolveAlertEvent = (id: string) => api.post(`/alerts/events/${id}/resolve`)
export const saveAlertInvestigation = (id: string, investigation: string) =>
  api.post(`/alerts/events/${id}/investigation`, { investigation })
export const deleteAlertEvent = (id: string) => api.delete(`/alerts/events/${id}`)

// Alert → RCA (告警根因分析联动)
export const rcaAlertAnalysis = (data: Record<string, unknown>) =>
  api.post('/ops/rca/alert', data, { timeout: 120000 })

// Logs
export const queryLogs = (params: Record<string, unknown>) => api.get('/logs/query', { params })
export const aggregateLogs = (params: Record<string, unknown>) => api.get('/logs/aggregate', { params })

// ===== 拓扑目录（typed property graph）=====
export interface TopologyNodeItem {
  id: number; type: string; name: string; props_json?: string; created_at: string; updated_at: string
}
export interface TopologyRelationItem {
  id: number; src_id: number; dst_id: number; type: string; props_json?: string; created_at: string
}
export interface TopologyNodeTypeItem {
  name: string; display_name: string; display_name_en?: string; builtin: boolean; tier: number; description: string
}
export interface TopologyRelationTypeItem {
  name: string; display_name: string; display_name_en?: string; builtin: boolean
  propagates_failure: boolean; direction: string; semantics_tag: string; description: string
}
export const topoListNodes = (params?: Record<string, unknown>) => api.get('/topology/nodes', { params })
export const topoCreateNode = (data: Record<string, unknown>) => api.post('/topology/nodes', data)
export const topoUpdateNode = (id: number, data: Record<string, unknown>) => api.put(`/topology/nodes/${id}`, data)
export const topoDeleteNode = (id: number) => api.delete(`/topology/nodes/${id}`)
export const topoListRelations = (params?: Record<string, unknown>) => api.get('/topology/relations', { params })
export const topoCreateRelation = (data: Record<string, unknown>) => api.post('/topology/relations', data)
export const topoDeleteRelation = (id: number) => api.delete(`/topology/relations/${id}`)
export const topoListNodeTypes = (params?: Record<string, unknown>) => api.get('/topology/node-types', { params })
export const topoCreateNodeType = (data: Record<string, unknown>) => api.post('/topology/node-types', data)
export const topoDeleteNodeType = (name: string) => api.delete(`/topology/node-types/${encodeURIComponent(name)}`)
export const topoListRelationTypes = (params?: Record<string, unknown>) => api.get('/topology/relation-types', { params })
export const topoCreateRelationType = (data: Record<string, unknown>) => api.post('/topology/relation-types', data)
export const topoDeleteRelationType = (name: string) => api.delete(`/topology/relation-types/${encodeURIComponent(name)}`)
export const topoSyncCatalog = () => api.post('/topology/sync-catalog')

// ===== FlowEditor (self-built engine) =====
// 用户自定义工作流 CRUD 走 /ai/workflows（内置 DAG 描述走 /ai/flows，路径分离避免冲突）
export const listWorkflows = () => api.get('/ai/workflows')
export const listNodeTypes = () => api.get('/ai/workflows/node-types')
export const createFlow = (data: Record<string, unknown>) => api.post('/ai/workflows', data)
export const updateFlow = (id: string, data: Record<string, unknown>) => api.put(`/ai/workflows/${encodeURIComponent(id)}`, data)
export const deleteFlow = (id: string) => api.delete(`/ai/workflows/${encodeURIComponent(id)}`)
export const toggleFlow = (id: string) => api.post(`/ai/workflows/${encodeURIComponent(id)}/toggle`)
export const runFlowAsync = (id: string, trigger: Record<string, unknown>) => api.post(`/ai/workflows/${encodeURIComponent(id)}/run`, { trigger })
export const listFlowRuns = (id: string) => api.get(`/ai/workflows/${encodeURIComponent(id)}/runs`)
export const getFlowRun = (id: string, runId: string) => api.get(`/ai/workflows/${encodeURIComponent(id)}/runs/${encodeURIComponent(runId)}`)
export const resumeFlowRun = (id: string, runId: string, approved: boolean) => api.post(`/ai/workflows/${encodeURIComponent(id)}/runs/${encodeURIComponent(runId)}/resume`, { approved })

// ===== Dashboard =====
export interface DashboardStats {
  services: number; edges: number; total_calls: number; total_errors: number
  error_rate: number; latency_p95: number
  top_services: Array<{ service: string; calls: number; errors: number; lat_sum_ns: number; error_rate: number; avg_latency_ms: number }>
  trend: Array<{ t: string; calls: number; errors: number }>
  top_errors: Array<{ service: string; errors: number }>
  alerts: {
    total: number; critical: number; warning: number; info: number
    by_service: Array<{ service: string; critical: number; warning: number; info: number; total: number }>
  }
  data_gaps?: string[]  // P1-3：缺失的小时窗口（"MM-DD HH:00 ~ MM-DD HH:00"）
}
export const getDashboardStats = () => api.get<DashboardStats>('/dashboard/stats')

// ===== 工作台集群资源（用户需求：工作台展示当前集群资源情况）=====
export interface ClusterResource {
  metric: string; current: number | null; threshold: number; ett_seconds: number
}
export interface DashboardResources {
  cluster_id: string; node_count: number
  resources: ClusterResource[]
}
export const getDashboardResources = (params?: Record<string, unknown>) =>
  api.get<DashboardResources>('/dashboard/resources', { params })

// ===== 知识库 =====
export interface KnowledgeItem {
  id: string
  type?: string        // case | knowledge
  title: string
  content: string
  source: string
  tags: string
  code_ref: any
  service?: string
  root_cause?: string
  plan?: string
  outcome?: string
  validated?: string
  weight?: number
  created_at?: string
  updated_at?: string
}
export interface RagStats { collection: string; total: number; cases?: number; knowledge?: number }
export const listKnowledge = (params?: Record<string, unknown>) => api.get('/ai/knowledge', { params })
export const addKnowledge = (data: Record<string, unknown>) => api.post('/ai/knowledge', data)
export const deleteKnowledge = (id: string) => api.delete(`/ai/knowledge/${id}`)
// 需求：报告/AI 对话一键沉淀为故障案例。body 支持 { report_id } 或 { service, symptom, root_cause, plan }
export const addKnowledgeCase = (data: Record<string, unknown>) => api.post('/ai/knowledge/case', data)
export const getRagStats = () => api.get('/ai/knowledge/rag/stats')
export const reloadRagKnowledge = () => api.post('/ai/knowledge/rag/reload')

// ===== NL2SQL =====
export const nl2sqlTranslate = (data: { question: string }) => api.post('/ai/nl2sql/translate', data)
export const nl2sqlExecute = (id: string) => api.post(`/ai/nl2sql/${id}/execute`)
export const nl2sqlGet = (id: string) => api.get(`/ai/nl2sql/${id}`)

// ===== 用户管理 =====
export interface UserItem {
  id: number; username: string; display_name: string; role: string; email: string; status: number; created_at: string
  scope?: string
}
export const listUsers = (params?: Record<string, unknown>) => api.get('/users', { params })
export const createUser = (data: Record<string, unknown>) => api.post('/users', data)
export const updateUser = (id: number, data: Record<string, unknown>) => api.put(`/users/${id}`, data)
export const deleteUser = (id: number) => api.delete(`/users/${id}`)
export const getMe = () => api.get('/me')

// ===== 报告中心 =====
export const listReports = (params?: Record<string, unknown>) => api.get('/ops/reports/history', { params })
export const reportTrend = (params?: Record<string, unknown>) => api.get('/ops/reports/trend', { params })

// ===== 服务目录 =====
export interface CatalogItem {
  id: number; service_name: string; display_name: string; description: string
  owner: string; team: string; tags: string; status: string
}
export const listCatalog = (params?: Record<string, unknown>) => api.get('/catalog/services', { params })
export const createCatalog = (data: Record<string, unknown>) => api.post('/catalog/services', data)
export const updateCatalog = (id: number, data: Record<string, unknown>) => api.put(`/catalog/services/${id}`, data)
export const deleteCatalog = (id: number) => api.delete(`/catalog/services/${id}`)

// ===== 设备管理 =====
export interface DeviceItem {
  id: number; hostname: string; ip: string; os: string; cpu_cores: number
  memory_mb: number; status: string; role: string; location: string; tags: string
}
export const listDevices = (params?: Record<string, unknown>) => api.get('/devices', { params })
export const createDevice = (data: Record<string, unknown>) => api.post('/devices', data)
export const updateDevice = (id: number, data: Record<string, unknown>) => api.put(`/devices/${id}`, data)
export const deleteDevice = (id: number) => api.delete(`/devices/${id}`)

// ===== SLO 目标管理 =====
export interface SLOTarget {
  id: string
  name: string
  service: string
  slo_type: string        // availability | latency
  target: number          // 如 99.9
  window_seconds: number
  enabled: boolean
}
export const listSLOs = () => api.get('/slo')
export const createSLO = (data: Record<string, unknown>) => api.post('/slo', data)
export const updateSLO = (id: string, data: Record<string, unknown>) => api.put(`/slo/${id}`, data)
export const deleteSLO = (id: string) => api.delete(`/slo/${id}`)

// ===== Monitor 看板面板 =====
export interface DashboardPanel {
  id: string
  title: string
  query: string
  chart_type: string   // line | bar | area | gauge | table
  grid_x: number
  grid_y: number
  grid_w: number
  grid_h: number
  span: number
  sort: number
  enabled: boolean
}
export const listPanels = () => api.get('/dashboard/panels')
export const createPanel = (data: Record<string, unknown>) => api.post('/dashboard/panels', data)
export const updatePanel = (id: string, data: Record<string, unknown>) => api.put(`/dashboard/panels/${id}`, data)
export const deletePanel = (id: string) => api.delete(`/dashboard/panels/${id}`)

// ===== 产物中心（C3） =====
export interface Artifact {
  type: string       // report | approval | flow_run
  id: string
  title: string
  status: string
  service: string
  time: string
  summary: string
  detail_url: string
}
export const listArtifacts = (params?: { limit?: number; type_filter?: string }) =>
  api.get('/ops/artifacts', { params })

// ===== 集群管理 =====
export interface ClusterItem {
  id: number; cluster_id?: string; tenant_id?: string; slug?: string
  name: string; provider: string; region: string; environment?: string; version: string
  node_count: number; status: string; lifecycle_status?: string; api_server: string
}
export interface ClusterNodeItem {
  name: string; role: string; status: string; ip: string; os: string; cpu: string; memory: string; kubelet: string
}
export const listClusters = () => api.get('/clusters')
export const syncClusters = () => api.post('/clusters/sync')
export const updateCluster = (id: number, data: Record<string, unknown>) => api.put(`/clusters/${id}`, data)
export const deleteCluster = (id: number) => api.delete(`/clusters/${id}`)
export const listClusterNodes = (id: number) => api.get(`/clusters/${id}/nodes`)
export interface NodeMetric {
  node?: string; cpu_usage_pct?: number; mem_usage_pct?: number
  cpu_capacity?: number; mem_capacity?: number; cpu_usage?: number; mem_usage?: number
}
export interface NodeMetricsResponse { nodes?: NodeMetric[] }
export const getNodeMetrics = () => api.get<NodeMetricsResponse>('/nodes/metrics')

// ===== IPMI 硬件 + 部件可用性 =====
export interface IpmiSensor { id?: number; node_name: string; sensor_name: string; sensor_type: string; reading: string; status: string }
export interface NodeHealthRow { node_name: string; component: string; status: string; detail?: string; updated_at?: string }
// SEL 系统事件日志（后端返回字段做兼容兜底：node/type/message 与 node_name/sensor/event_desc 并存）
export interface IpmiEvent {
  id?: number | string
  node_name?: string; node?: string
  event_id?: string
  event_time?: string; time?: string; created_at?: string
  sensor?: string; event_type?: string; type?: string
  event_desc?: string; message?: string
  severity?: string
}
export const listIpmiSensors = (params?: Record<string, unknown>) => api.get('/ipmi/sensors', { params })
export const listNodeHealth = (params?: Record<string, unknown>) => api.get('/node/health', { params })
export const listIpmiEvents = (params?: Record<string, unknown>) => api.get('/ipmi/events', { params })

// ===== 变更管理（变更时间线）=====
export interface ChangeItem {
  id?: number | string
  cluster_id?: string | number
  service?: string
  change_type?: string
  operator?: string
  content?: string
  created_at?: string; time?: string
}
export const getChanges = (params?: Record<string, unknown>) => api.get('/ops/changes', { params })
export const postChange = (data: Record<string, unknown>) => api.post('/ops/changes', data)

// ===== 知识图谱 =====
// 字段对齐后端 kg_api.kg_graph_full：节点 {id:int,type,name,props}，边 {id:int,src:int,dst:int,type,props}。
// 兼容旧写法 source/target（部分 mock/降级路径可能使用），实际以 src/dst 为准。
export interface KgNode { id?: string | number; name: string; type?: string; props?: Record<string, unknown> }
export interface KgEdge {
  id?: number
  source?: string | number
  target?: string | number
  src?: string | number
  dst?: string | number
  type?: string
  value?: number
  calls?: number
  errors?: number
  props?: { calls?: number; errors?: number; cluster_id?: string; created_by?: string; [k: string]: unknown }
}
export interface KgGraph {
  nodes?: KgNode[]
  edges?: KgEdge[]
  links?: KgEdge[]
  cluster_id?: string
  unavailable?: boolean  // 后端 API 未就绪标记（兼容旧容错路径）
}
// B12: 移除 .catch 吞错——错误传播到调用方（KnowledgeGraph 经 ErrorState 展示 + 重试），
// 不再静默返回空图，避免"加载失败"与"无数据"混淆。
export const getKgGraph = (params?: Record<string, unknown>) =>
  api.get<KgGraph>('/ai/kg/graph', { params })

// ===== 系统健康组件（平台健康页）=====
export interface SystemComponent {
  name: string; type: string; status: string; latency_ms?: number; detail?: string
}
export const getSystemComponents = () => api.get<SystemComponent[]>('/system/components')

// ===== KubeVirt 虚拟机 =====
export interface VmItem {
  name: string; namespace: string; status: string; node?: string; cpu?: string; memory?: string
}
export const listVms = (params?: Record<string, unknown>) => api.get('/infrastructure/vms', { params })
export const getVm = (ns: string, name: string) =>
  api.get(`/infrastructure/vms/${encodeURIComponent(ns)}/${encodeURIComponent(name)}`)

// ===== MCP 工具目录 =====
export const getMcpTools = () => api.get('/mcp/tools')
export const callMcpTool = (name: string, args: Record<string, any>) => api.post('/mcp/call', { name, args })

// ===== Admin: 用户 scope =====
export interface ScopePayload {
  services?: string[]
  clusters?: string[]
  devices?: string[]
}
export const updateUserScope = (id: number, data: Record<string, unknown>) => api.put(`/users/${id}`, data)

// ===== Admin: 集群 kubeconfig 多集群 =====
export const createCluster = (data: Record<string, unknown>) => api.post('/clusters', data)
export const getClusterNamespaces = (id: number) => api.get(`/clusters/${id}/namespaces`)
export const getClusterEvents = (id: number) => api.get(`/clusters/${id}/events`)

// ===== 容量预测（Capacity Forecast）=====
export interface ForecastSeries {
  values: number[]
  ett_seconds: number
  within_horizon: boolean
  already_breached: boolean
}
export interface CapacityForecast {
  metric: string
  instance: string
  threshold: number
  current: number
  change_pct: number
  timestamps: number[]
  history: number[]
  forecasts: {
    linear: ForecastSeries
    ewma: ForecastSeries
  }
}
export const getCapacityForecast = (params: {
  metric: string
  instance?: string
  hours?: number
  step?: number
  horizon?: number
  threshold?: number
}) => api.get<CapacityForecast>('/capacity/forecast', { params })

// node 实例列表（供容量预测页 node 选择器；值为 VM 真实 instance 标签）
export const getCapacityInstances = () =>
  api.get<{ instances: string[]; count: number }>('/capacity/instances')

export default api
