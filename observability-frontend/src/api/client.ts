import axios from 'axios'

// 租户 ID 从构建环境变量注入（可移植），默认单租户 'default'
export const TENANT_ID = (import.meta.env.VITE_TENANT_ID as string) || 'default'

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

// Request interceptor: set Authorization header + 多集群过滤参数
api.interceptors.request.use((config) => {
  const t = localStorage.getItem('token')
  if (t) {
    config.headers.Authorization = `Bearer ${t}`
  }
  const cid = readCurrentClusterId()
  // 跳过对集群管理接口自身注入 cluster_id（避免干扰集群 CRUD）
  const url = config.url || ''
  const isClusterApi = url.startsWith('/clusters')
  if (!isClusterApi && cid !== 'all') {
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
export const getServices = () => api.get('/services')
export const getServiceDetail = (name: string) => api.get(`/services/${name}`)
export const getTraces = (params?: Record<string, unknown>) => api.get('/traces', { params })
export const getTraceDetail = (id: string) => api.get(`/traces/${id}`)
export const getTraceContext = (id: string) => api.get(`/traces/${id}/context`)
export const getTopology = (params?: Record<string, unknown>) => api.get('/topology/global', { params })
export const getTopologyNodeDetail = (name: string, params?: Record<string, unknown>) => api.get(`/topology/node/${encodeURIComponent(name)}`, { params })

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
export const rejectTask = (id: string) => api.post(`/ops/tasks/${id}/reject`)
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
export const getAlertEvents = (params?: Record<string, unknown>) => api.get('/alerts/events', { params })
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
  id: number
  title: string
  content: string
  source: string
  tags: string
  code_ref: any
  created_at?: string
  updated_at?: string
}
export interface RagStats { collection: string; total: number }
export const listKnowledge = (params?: Record<string, unknown>) => api.get('/ai/knowledge', { params })
export const addKnowledge = (data: Record<string, unknown>) => api.post('/ai/knowledge', data)
export const deleteKnowledge = (id: number) => api.delete(`/ai/knowledge/${id}`)
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
  id: number; name: string; provider: string; region: string; version: string
  node_count: number; status: string; api_server: string
}
export interface ClusterNodeItem {
  name: string; role: string; status: string; ip: string; os: string; cpu: string; memory: string; kubelet: string
}
export const listClusters = () => api.get('/clusters')
export const syncClusters = () => api.post('/clusters/sync')
export const updateCluster = (id: number, data: Record<string, unknown>) => api.put(`/clusters/${id}`, data)
export const deleteCluster = (id: number) => api.delete(`/clusters/${id}`)
export const listClusterNodes = (id: number) => api.get(`/clusters/${id}/nodes`)
export const getNodeMetrics = () => api.get('/nodes/metrics')

// ===== SNMP 网络设备 =====
export interface SnmpDevice {
  id: number; hostname: string; ip: string; community: string; snmp_version: string
  vendor: string; model: string; status: string; last_collect_at: string
}
export interface SnmpInterface {
  id: number; device_id: number; if_index: number; if_name: string
  if_oper_status: string; if_in_octets: number; if_out_octets: number; if_in_errors: number
}
export const listSnmpDevices = () => api.get('/snmp/devices')
export const createSnmpDevice = (data: Record<string, unknown>) => api.post('/snmp/devices', data)
export const deleteSnmpDevice = (id: number) => api.delete(`/snmp/devices/${id}`)
export const listSnmpInterfaces = (id: number) => api.get(`/snmp/devices/${id}/interfaces`)
export const collectSnmpDevice = (id: number) => api.post(`/snmp/devices/${id}/collect`)

// ===== IPMI 硬件 + 部件可用性 =====
export interface IpmiSensor { node_name: string; sensor_name: string; sensor_type: string; reading: string; status: string }
export interface NodeHealthRow { node_name: string; component: string; status: string; updated_at: string }
export const listIpmiSensors = (params?: Record<string, unknown>) => api.get('/ipmi/sensors', { params })
export const listNodeHealth = (params?: Record<string, unknown>) => api.get('/node/health', { params })

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
