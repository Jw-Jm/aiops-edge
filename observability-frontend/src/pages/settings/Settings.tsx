import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { Alert, Button, Descriptions, Input, Select, Table, Tag } from 'antd'
import { useSearchParams } from 'react-router-dom'
import { api } from '../../api/client'
import DataState from '../../components/display/DataState'
import { PageHeader, PaneCard, StatusBadge, type StatusTone } from '../../components/ui/PageKit'
import { SETTINGS_SECTIONS, type SettingsSectionId } from '../../layout/navConfig'

interface EndpointSpec {
  key: string
  label: string
  path: string
  /** 展示为表格时的字段；为空时按键值渲染 */
  columns?: { title: string; dataIndex: string; key?: string }[]
  /** 只读探测：该接口的安全语义 */
  note?: string
}

/** 每个设置分区绑定的真实后端能力（设计规范 §6.8 / §10） */
const SECTION_ENDPOINTS: Record<SettingsSectionId, EndpointSpec[]> = {
  overview: [],
  integrations: [
    { key: 'clusters', label: '纳管集群', path: '/clusters' },
    { key: 'devices', label: '网络设备', path: '/devices' },
    { key: 'snmp', label: 'SNMP 采集', path: '/snmp' },
    { key: 'slo', label: 'SLO 目标', path: '/slo' },
    { key: 'catalog', label: '服务目录', path: '/catalog/services' },
    { key: 'silences', label: '告警静默', path: '/alerts/silences' },
    { key: 'dataSync', label: '数据同步', path: '/data/sync' },
    { key: 'k8sSettings', label: 'Kubernetes 接入', path: '/settings/k8s' },
  ],
  graph: [
    { key: 'sync', label: '来源同步状态', path: '/ai/kg/ops/sync-states' },
    { key: 'outbox', label: 'Outbox 积压', path: '/ai/kg/ops/outbox' },
    { key: 'aliases', label: 'Schema / Alias', path: '/ai/kg/ops/aliases' },
    { key: 'shadow', label: 'Shadow 差异', path: '/ai/kg/ops/shadow-diff' },
    { key: 'topologySync', label: '拓扑对账与同步', path: '/topology/sync' },
    { key: 'syncCatalog', label: '同步目录', path: '/topology/sync-catalog' },
    { key: 'nodeTypes', label: '节点类型', path: '/topology/node-types' },
    { key: 'relationTypes', label: '关系类型', path: '/topology/relation-types' },
  ],
  llm: [
    { key: 'config', label: '当前 LLM 配置', path: '/settings/llm' },
    { key: 'providers', label: 'Provider', path: '/settings/llm/providers' },
    { key: 'history', label: '配置历史', path: '/settings/llm/history' },
  ],
  workflow: [
    { key: 'agents', label: 'Agent', path: '/ai/agents' },
    { key: 'skills', label: 'Skill', path: '/ai/skills' },
    { key: 'flows', label: 'Workflow', path: '/ai/flows' },
  ],
  mcp: [
    { key: 'tools', label: 'MCP 工具', path: '/mcp/tools' },
  ],
  rag: [
    { key: 'rag', label: 'RAG 索引状态', path: '/ai/knowledge/rag/stats' },
    { key: 'knowledge', label: '知识源', path: '/ai/knowledge' },
  ],
  security: [
    { key: 'users', label: '账户', path: '/users' },
    { key: 'tenants', label: '租户', path: '/tenants' },
    { key: 'policy', label: '告警调查策略', path: '/system/alert-investigation-policy' },
    { key: 'rules', label: 'AI 规则', path: '/ai/rules' },
    { key: 'shellCheck', label: '受控终端能力核查', path: '/ai/shell/check' },
  ],
  health: [
    { key: 'components', label: '平台组件', path: '/system/components' },
    { key: 'status', label: '平台状态', path: '/system/status' },
    { key: 'deepflow', label: 'DeepFlow 采集', path: '/deepflow/status' },
    { key: 'cache', label: '查询缓存', path: '/system/cache' },
    { key: 'cleanup', label: '数据清理预检', path: '/admin/data-cleanups/preview' },
  ],
  evaluation: [
    { key: 'runs', label: '最近运行', path: '/ai/runs?limit=20' },
  ],
}

const SENSITIVE_KEYS = /(secret|password|passwd|token|credential|api_?key|private_?key|kubeconfig)/i

/** 秘密与敏感字段绝不返回浏览器可读值（§6.8 / §13.3） */
export function maskSecrets(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(maskSecrets)
  if (value && typeof value === 'object') {
    const out: Record<string, unknown> = {}
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      out[k] = SENSITIVE_KEYS.test(k) ? (v ? '【已配置，不回传明文】' : '【未配置】') : maskSecrets(v)
    }
    return out
  }
  return value
}

function renderValue(value: unknown): React.ReactNode {
  if (value === null || value === undefined || value === '') return <span className="muted-sm">未提供</span>
  if (typeof value === 'boolean') return value ? '是' : '否'
  if (typeof value === 'number') return String(value)
  if (typeof value === 'string') return value
  return <code className="raw-json">{JSON.stringify(maskSecrets(value), null, 2)}</code>
}

function toRows(payload: unknown): Record<string, unknown>[] | null {
  if (Array.isArray(payload)) return payload as Record<string, unknown>[]
  if (payload && typeof payload === 'object') {
    const obj = payload as Record<string, unknown>
    for (const key of ['items', 'data', 'list', 'results', 'providers', 'agents', 'skills', 'flows', 'tools', 'events', 'runs']) {
      if (Array.isArray(obj[key])) return obj[key] as Record<string, unknown>[]
    }
  }
  return null
}

interface EndpointState {
  state: 'loading' | 'ready' | 'empty' | 'error' | 'forbidden'
  payload?: unknown
  error?: unknown
}

const Settings: React.FC = () => {
  const [searchParams, setSearchParams] = useSearchParams()
  const section = (searchParams.get('section') as SettingsSectionId) || 'overview'
  const [states, setStates] = useState<Record<string, EndpointState>>({})
  const [probeResult, setProbeResult] = useState<{ ok: boolean; message: string } | null>(null)
  const [probing, setProbing] = useState(false)
  const [mcpQuery, setMcpQuery] = useState('')

  const endpoints = SECTION_ENDPOINTS[section] ?? []

  const load = useCallback(() => {
    const specs = SECTION_ENDPOINTS[section] ?? []
    if (specs.length === 0) return () => undefined
    const controller = new AbortController()
    setStates(Object.fromEntries(specs.map((s) => [s.key, { state: 'loading' as const }])))
    for (const spec of specs) {
      const [path, query] = spec.path.split('?')
      api
        .get(path, { params: query ? Object.fromEntries(new URLSearchParams(query)) : undefined, signal: controller.signal })
        .then((res) => {
          const payload = res.data
          const rows = toRows(payload)
          const isEmpty = rows ? rows.length === 0 : payload === null || payload === undefined
          setStates((prev) => ({ ...prev, [spec.key]: { state: isEmpty ? 'empty' : 'ready', payload } }))
        })
        .catch((error) =>
          setStates((prev) => ({
            ...prev,
            [spec.key]: { state: error?.response?.status === 403 ? 'forbidden' : 'error', error },
          })),
        )
    }
    return () => controller.abort()
  }, [section])

  useEffect(() => load(), [load])

  const readyCount = useMemo(() => Object.values(states).filter((s) => s.state === 'ready').length, [states])
  const failedCount = useMemo(() => Object.values(states).filter((s) => s.state === 'error' || s.state === 'forbidden').length, [states])

  const runLlmProbe = async () => {
    setProbing(true)
    setProbeResult(null)
    try {
      const res = await api.post('/settings/llm/test', {})
      const data: any = res.data
      setProbeResult({ ok: Boolean(data?.success ?? data?.ok ?? true), message: data?.message ?? data?.detail ?? '探测已执行' })
    } catch (error: any) {
      setProbeResult({ ok: false, message: error?.response?.data?.message || error?.message || '探测失败' })
    } finally {
      setProbing(false)
    }
  }

  const runMcpProbe = async () => {
    setProbing(true)
    setProbeResult(null)
    try {
      const res = await api.post('/mcp/call', { tool: mcpQuery, params: {}, dry_run: true })
      setProbeResult({ ok: true, message: `工具 ${mcpQuery} 只读探测完成` })
      void res
    } catch (error: any) {
      setProbeResult({ ok: false, message: error?.response?.data?.message || error?.message || 'MCP 探测失败' })
    } finally {
      setProbing(false)
    }
  }

  return (
    <div className="settings-page" data-testid="settings-page">
      <PageHeader
        title="设置"
        desc="平台接入、图谱、LLM、Agent/Workflow、MCP、RAG、策略安全、平台健康与评测；配置状态与真实运行探测必须区分"
        actions={<Button onClick={load}>刷新</Button>}
      />

      <div className="settings-layout">
        <nav className="settings-nav" aria-label="设置分区">
          {SETTINGS_SECTIONS.map((item) => (
            <button
              key={item.id}
              type="button"
              className={item.id === section ? 'is-active' : ''}
              aria-current={item.id === section ? 'page' : undefined}
              onClick={() => setSearchParams({ section: item.id }, { replace: true })}
              data-testid={`settings-section-${item.id}`}
            >
              {item.label}
            </button>
          ))}
        </nav>

        <div className="settings-content">
          {section === 'overview' && (
            <>
              <Alert
                type={failedCount > 0 ? 'error' : readyCount > 0 ? 'success' : 'info'}
                showIcon
                style={{ marginBottom: 12 }}
                message="配置存在不等于真实运行健康"
                description="每个分区同时展示配置状态与真实只读探测结果；探测失败必须显示 Failed/Partial，不得显示为健康。请选择左侧分区查看具体能力。"
              />
              <PaneCard title="分区能力状态">
                <Table
                  size="small"
                  rowKey="id"
                  pagination={false}
                  dataSource={SETTINGS_SECTIONS.filter((s) => s.id !== 'overview').map((s) => ({
                    id: s.id,
                    label: s.label,
                    capabilities: (SECTION_ENDPOINTS[s.id] ?? []).length,
                  }))}
                  columns={[
                    { title: '分区', dataIndex: 'label', key: 'label' },
                    { title: '绑定后端能力', dataIndex: 'capabilities', key: 'capabilities', width: 140, render: (v: number) => `${v} 项` },
                    {
                      title: '入口',
                      key: 'open',
                      width: 120,
                      render: (_: unknown, r: { id: string }) => (
                        <Button size="small" onClick={() => setSearchParams({ section: r.id }, { replace: true })}>
                          打开
                        </Button>
                      ),
                    },
                  ]}
                />
              </PaneCard>
            </>
          )}

          {section !== 'overview' && endpoints.length === 0 && (
            <DataState kind="empty" title="该分区尚未绑定后端能力" description="这是一个后端缺口，将作为契约测试与后端实现项登记，不允许用前端假数据填充。" />
          )}

          {section !== 'overview' &&
            endpoints.map((spec) => {
              const st = states[spec.key] ?? { state: 'loading' as const }
              const rows = toRows(st.payload)
              return (
                <PaneCard
                  key={spec.key}
                  title={spec.label}
                  action={
                    <span className="muted-sm">
                      <code>/api/v1{spec.path}</code>
                    </span>
                  }
                >
                  {st.state === 'loading' && <DataState kind="loading" compact />}
                  {st.state === 'error' && <DataState kind="error" description="接口读取失败" onRetry={load} />}
                  {st.state === 'forbidden' && <DataState kind="forbidden" />}
                  {st.state === 'empty' && <DataState kind="empty" title="该能力没有返回数据" description="可能是尚未配置或来源不可达——这不是 0，也不是健康。" />}
                  {st.state === 'ready' &&
                    (rows ? (
                      <Table
                        size="small"
                        rowKey={(r: any, index?: number) => String(r.id ?? r.name ?? r.key ?? r.uid ?? index)}
                        pagination={{ pageSize: 8 }}
                        dataSource={rows}
                        columns={Object.keys(rows[0] ?? {})
                          .slice(0, 6)
                          .map((k) => ({
                            title: k,
                            dataIndex: k,
                            key: k,
                            render: (v: unknown) => renderValue(maskSecrets(v)),
                          }))}
                      />
                    ) : (
                      <Descriptions size="small" column={1} bordered>
                        {Object.entries((maskSecrets(st.payload) as Record<string, unknown>) ?? {}).map(([k, v]) => (
                          <Descriptions.Item key={k} label={k}>
                            {renderValue(v)}
                          </Descriptions.Item>
                        ))}
                      </Descriptions>
                    ))}
                </PaneCard>
              )
            })}

          {section === 'llm' && (
            <PaneCard title="真实探测（必须验证 egress 与目标模型）" action={<Button type="primary" loading={probing} onClick={runLlmProbe}>运行 LLM 探测</Button>}>
              {probeResult ? (
                <Alert type={probeResult.ok ? 'success' : 'error'} showIcon message={probeResult.ok ? '探测成功' : '探测失败'} description={probeResult.message} />
              ) : (
                <p className="muted-sm">探测会真实调用已配置的模型端点；仅验证配置字段非空不构成健康。</p>
              )}
            </PaneCard>
          )}

          {section === 'mcp' && (
            <PaneCard title="工具只读探测" action={<Button loading={probing} disabled={!mcpQuery} onClick={runMcpProbe}>只读探测</Button>}>
              <Input
                placeholder="输入 MCP 工具名进行 dry-run 探测"
                value={mcpQuery}
                onChange={(e) => setMcpQuery(e.target.value)}
                aria-label="MCP 工具名"
              />
              <Alert
                type="info"
                showIcon
                style={{ marginTop: 8 }}
                message="浏览器不直连 MCP server"
                description="所有调用经后端策略与审计；写工具只能生成 dry-run/草稿，仍需处置确认。"
              />
              {probeResult && <Alert style={{ marginTop: 8 }} type={probeResult.ok ? 'success' : 'error'} showIcon message={probeResult.message} />}
            </PaneCard>
          )}

          {section === 'security' && (
            <PaneCard title="安全边界">
              <ul className="settings-security-notes">
                <li>客户端提交的 tenant/cluster/resource 只是请求参数，服务端必须重建并校验。</li>
                <li>秘密、token、完整连接串与敏感 endpoint 不返回浏览器，也不进入 AI 上下文。</li>
                <li>危险操作默认 fail closed：预检失败、范围漂移、对象版本变化、审计不可用时不得执行。</li>
                <li>统一账户不取消授权：前端不承担最终授权判断。</li>
              </ul>
            </PaneCard>
          )}

          <div className="settings-footer muted-sm">
            配置状态与真实运行健康分开表达；secret、token 与完整连接串不会返回浏览器。
          </div>
        </div>
      </div>
    </div>
  )
}

export { SECTION_ENDPOINTS }
export default Settings
