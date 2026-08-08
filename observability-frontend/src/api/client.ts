import axios from 'axios'

const api = axios.create({
  baseURL: '/api/v1',
  headers: { 'X-Tenant-ID': 'default' },
  timeout: 15000,
})

// Read token from localStorage on init
const token = localStorage.getItem('token')
if (token) {
  api.defaults.headers.common['Authorization'] = `Bearer ${token}`
}

// Request interceptor: set Authorization header
api.interceptors.request.use((config) => {
  const t = localStorage.getItem('token')
  if (t) {
    config.headers.Authorization = `Bearer ${t}`
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
export const getChatSessions = () => api.get('/ai/sessions')
export const getSession = (sid: string) => api.get(`/ai/session/${sid}`)
export const deleteSession = (sid: string) => api.delete(`/ai/session/${sid}`)

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

// ===== 审计日志 =====
export const listAuditLogs = (params?: Record<string, unknown>) => api.get('/ops/audit-logs', { params })

// ===== 知识库 =====
export const listKnowledge = (params?: Record<string, unknown>) => api.get('/ai/knowledge', { params })
export const addKnowledge = (data: Record<string, unknown>) => api.post('/ai/knowledge', data)
export const deleteKnowledge = (id: number) => api.delete(`/ai/knowledge/${id}`)

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

// Infrastructure
export const getInfrastructureNodes = () => api.get('/infrastructure/nodes')
export const getInfrastructurePods = (namespace?: string) => api.get('/infrastructure/pods', { params: { namespace } })
export const getInfrastructureDeployments = (namespace?: string) => api.get('/infrastructure/deployments', { params: { namespace } })
export const getInfrastructureNamespaces = () => api.get('/infrastructure/namespaces')

// LLM Settings
export const getLLMSettings = () => api.get('/settings/llm')
export const saveLLMSettings = (data: Record<string, unknown>) => api.post('/settings/llm', data)
export const testLLMConnection = (data: Record<string, unknown>) => api.post('/settings/llm/test', data)

// Auth
export const login = (username: string, password: string) => api.post('/auth/login', { username, password })

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

// Alert → RCA (告警根因分析联动)
export const rcaAlertAnalysis = (data: Record<string, unknown>) =>
  api.post('/ops/rca/alert', data, { timeout: 120000 })

// DeepFlow
export const getDeepFlowStatus = () => api.get('/deepflow/status')

// Logs
export const queryLogs = (params: Record<string, unknown>) => api.get('/logs/query', { params })

// ===== FlowEditor (self-built engine) =====
export const listNodeTypes = () => api.get('/ai/flows/node-types')
export const createFlow = (data: Record<string, unknown>) => api.post('/ai/flows', data)
export const updateFlow = (id: string, data: Record<string, unknown>) => api.put(`/ai/flows/${encodeURIComponent(id)}`, data)
export const deleteFlow = (id: string) => api.delete(`/ai/flows/${encodeURIComponent(id)}`)
export const toggleFlow = (id: string) => api.post(`/ai/flows/${encodeURIComponent(id)}/toggle`)
export const runFlowAsync = (id: string, trigger: Record<string, unknown>) => api.post(`/ai/flows/${encodeURIComponent(id)}/run`, { trigger })
export const listFlowRuns = (id: string) => api.get(`/ai/flows/${encodeURIComponent(id)}/runs`)
export const getFlowRun = (id: string, runId: string) => api.get(`/ai/flows/${encodeURIComponent(id)}/runs/${encodeURIComponent(runId)}`)
export const resumeFlowRun = (id: string, runId: string, approved: boolean) => api.post(`/ai/flows/${encodeURIComponent(id)}/runs/${encodeURIComponent(runId)}/resume`, { approved })

export default api
