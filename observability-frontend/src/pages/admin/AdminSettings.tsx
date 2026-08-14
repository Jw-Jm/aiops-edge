import React, { useEffect, useState } from 'react'
import { Form, Input, Select, Tabs, Table, Button, message, Modal, Tag, Space, Popconfirm, Descriptions, Alert } from 'antd'
import { PageHeader, Breadcrumb, StatusBadge, Empty, type StatusTone } from '../../components/ui/PageKit'

// 集群状态 → StatusBadge tone 映射
function clusterTone(s?: string): StatusTone {
  if (s === 'healthy' || s === 'Ready' || s === 'active') return 'ok'
  if (s === 'pending' || s === 'connecting') return 'warn'
  if (s === 'unhealthy' || s === 'error' || s === 'degraded') return 'crit'
  return 'info'
}

// 节点实时用量格式化：CPU（metrics-server 返回 nanocores，如 "2472171400n"，转为核数）
function fmtNodeCpu(s?: string): string {
  if (!s) return '-'
  const t = s.trim()
  if (t.endsWith('n')) {
    const n = parseFloat(t.slice(0, -1))
    return isNaN(n) ? t : `${(n / 1e9).toFixed(2)}`
  }
  if (t.endsWith('m')) {
    const n = parseFloat(t.slice(0, -1))
    return isNaN(n) ? t : `${(n / 1000).toFixed(2)}`
  }
  return t
}

// 节点实时用量格式化：内存（Ki 为基数，转为人类可读的 Gi/Mi）
function fmtNodeMem(s?: string): string {
  if (!s) return '-'
  const t = s.trim()
  const num = parseFloat(t)
  if (isNaN(num)) return t
  const base = t.endsWith('i') ? t.slice(-2) : '' // Ki/Mi/Gi
  let ki: number
  if (base === 'Ki') ki = num
  else if (base === 'Mi') ki = num * 1024
  else if (base === 'Gi') ki = num * 1024 * 1024
  else return t
  if (ki >= 1024 * 1024) return `${(ki / 1024 / 1024).toFixed(1)}Gi`
  if (ki >= 1024) return `${(ki / 1024).toFixed(1)}Mi`
  return `${ki.toFixed(0)}Ki`
}
import {
  getLLMSettings, saveLLMSettings, testLLMConnection, listLLMModels,
  listClusters,
  createCluster,
  deleteCluster,
  syncClusters,
  listClusterNodes,
  getClusterNamespaces,
  getClusterEvents,
  listAuditLogs,
  listUsers,
  getNodeMetrics,
  getSystemComponents,
  type SystemComponent,
  type ClusterItem,
  type ClusterNodeItem,
} from '../../api/client'


// ---- 预设 LLM 厂商：选择后自动填充 base_url ----
const LLM_VENDORS: { key: string; name: string; base_url: string; default_model: string }[] = [
  { key: 'deepseek',  name: 'DeepSeek',        base_url: 'https://api.deepseek.com/v1',                      default_model: 'deepseek-chat' },
  { key: 'xiaomi',    name: '小米 MiMo',        base_url: 'https://api.mimo.xiaomi.com/v1',                  default_model: 'mimo-chat' },
  { key: 'openai',    name: 'OpenAI',          base_url: 'https://api.openai.com/v1',                        default_model: 'gpt-4o-mini' },
  { key: 'qwen',      name: '通义千问 Qwen',     base_url: 'https://dashscope.aliyuncs.com/compatible-mode/v1', default_model: 'qwen-plus' },
  { key: 'ernie',     name: '文心一言',          base_url: 'https://qianfan.baidubce.com/v2',                  default_model: 'ernie-4.0-8k' },
  { key: 'kimi',      name: 'Kimi (Moonshot)',  base_url: 'https://api.moonshot.cn/v1',                       default_model: 'moonshot-v1-8k' },
  { key: 'zhipu',     name: '智谱 GLM',          base_url: 'https://open.bigmodel.cn/api/paas/v4',             default_model: 'glm-4-flash' },
  { key: 'doubao',    name: '火山引擎 豆包',      base_url: 'https://ark.cn-beijing.volces.com/api/v3',         default_model: 'doubao-pro-32k' },
  { key: 'custom',    name: '自定义 (其他)',     base_url: '',                                                 default_model: '' },
]

// ---- 集群管理 ----
function ClusterManager() {
  const [list, setList] = useState<ClusterItem[]>([])
  const [loading, setLoading] = useState(false)
  const [open, setOpen] = useState(false)
  const [form] = Form.useForm()
  const [detail, setDetail] = useState<ClusterItem | null>(null)
  const [detailTab, setDetailTab] = useState<'nodes' | 'namespaces' | 'events'>('nodes')
  const [nodes, setNodes] = useState<ClusterNodeItem[]>([])
  const [nodeMetrics, setNodeMetrics] = useState<Record<string, any>>({})
  const [namespaces, setNamespaces] = useState<string[]>([])
  const [events, setEvents] = useState<unknown[]>([])

  const load = async () => {
    setLoading(true)
    try {
      const res = await listClusters()
      const data = res.data
      setList(Array.isArray(data) ? data : (data?.data ?? data?.clusters ?? []))
    } catch {
      /* 集群接口失败不阻塞 */
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  // viewDetail 加载节点时并行获取实时用量
  useEffect(() => {
    if (!detail) return
    getNodeMetrics().then((r) => {
      const m: Record<string, any> = {}
      ;(r.data?.nodes || []).forEach((n: any) => { m[n.node] = n })
      setNodeMetrics(m)
    }).catch(() => {})
  }, [detail])


  const onSubmit = async () => {
    const v = await form.validateFields()
    try {
      await createCluster(v)
      message.success('集群已添加')
      setOpen(false)
      form.resetFields()
      load()
    } catch (e: any) {
      message.error(e?.response?.data?.message || '添加失败')
    }
  }

  const viewDetail = async (c: ClusterItem) => {
    setDetail(c)
    setDetailTab('nodes')
    await loadNodes(c)
  }

  const loadNodes = async (c: ClusterItem) => {
    try {
      const r = await listClusterNodes(c.id)
      const d = r.data
      setNodes(Array.isArray(d) ? d : (d?.nodes ?? []))
    } catch { setNodes([]) }
  }

  const loadNamespaces = async (c: ClusterItem) => {
    try {
      const r = await getClusterNamespaces(c.id)
      const d = r.data
      setNamespaces(Array.isArray(d) ? d : (d?.namespaces ?? []))
    } catch { setNamespaces([]) }
  }

  const loadEvents = async (c: ClusterItem) => {
    try {
      const r = await getClusterEvents(c.id)
      const d = r.data
      setEvents(Array.isArray(d) ? d : (d?.events ?? []))
    } catch { setEvents([]) }
  }

  const onDetailTab = (k: string) => {
    setDetailTab(k as any)
    if (detail) {
      if (k === 'nodes') loadNodes(detail)
      if (k === 'namespaces') loadNamespaces(detail)
      if (k === 'events') loadEvents(detail)
    }
  }

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <div style={{ fontSize: 15, fontWeight: 600 }}>纳管集群</div>
        <Space>
          <Button size="small" onClick={() => { syncClusters().then(() => { message.success('同步完成'); load() }).catch(() => message.warning('同步失败')) }}>同步集群</Button>
          <Button type="primary" size="small" onClick={() => setOpen(true)}>+ 纳管集群</Button>
        </Space>
      </div>
      <Table
        rowKey="id"
        size="small"
        loading={loading}
        dataSource={list}
        pagination={false}
        columns={[
          { title: '集群', dataIndex: 'name' },
          { title: 'Provider', dataIndex: 'provider', width: 100 },
          { title: '版本', dataIndex: 'version', width: 110 },
          { title: '节点', dataIndex: 'node_count', width: 70 },
          { title: '状态', dataIndex: 'status', width: 100, render: (s) => <StatusBadge text={s || 'unknown'} tone={clusterTone(s)} /> },
          { title: 'API Server', dataIndex: 'api_server', ellipsis: true },
          { title: '操作', width: 200, render: (_, r) => (
            <Space size={0}>
              <Button type="link" size="small" onClick={() => viewDetail(r)}>查看</Button>
              <Popconfirm title="确认删除该集群？" onConfirm={async () => { await deleteCluster(r.id); message.success('已删除'); load() }}>
                <Button type="link" size="small" danger>删除</Button>
              </Popconfirm>
            </Space>
          )},
        ]}
      />

      <Modal title="纳管集群" open={open} onOk={onSubmit} onCancel={() => setOpen(false)} okText="添加" width={620}>
        <Form form={form} layout="vertical">
          <Form.Item name="name" label="集群名称" rules={[{ required: true, message: '请输入集群名称' }]}><Input placeholder="如 production-cluster" /></Form.Item>
          <Form.Item name="provider" label="提供商"><Select options={[{ value: 'k8s', label: 'Kubernetes' }, { value: 'openshift', label: 'OpenShift' }, { value: 'k3s', label: 'K3s' }]} /></Form.Item>
          <Form.Item name="api_server" label="API Server 地址"><Input placeholder="https://192.168.1.10:6443" /></Form.Item>
          <Form.Item name="kubeconfig" label="Kubeconfig" rules={[{ required: true, message: '请粘贴 kubeconfig' }]}>
            <Input.TextArea rows={8} placeholder="粘贴 kubeconfig 内容（含 server / certificate-authority-data / client-certificate-data / client-key-data）" />
          </Form.Item>
          <Form.Item name="region" label="区域"><Input placeholder="可选：如 cn-south-1" /></Form.Item>
        </Form>
      </Modal>

      <Modal
        title={detail ? `集群详情 - ${detail.name}` : '集群详情'}
        open={!!detail}
        onCancel={() => setDetail(null)}
        footer={null}
        width={720}
      >
        {detail && (
          <div>
            <Descriptions size="small" column={3} style={{ marginBottom: 12 }}>
              <Descriptions.Item label="状态"><StatusBadge text={detail.status || 'unknown'} tone={clusterTone(detail.status)} /></Descriptions.Item>
              <Descriptions.Item label="节点">{detail.node_count}</Descriptions.Item>
              <Descriptions.Item label="版本">{detail.version}</Descriptions.Item>
            </Descriptions>
            <Tabs activeKey={detailTab} onChange={onDetailTab} items={[
              { key: 'nodes', label: `节点 (${nodes.length})`, children: (
                <Table rowKey="name" size="small" dataSource={nodes} pagination={false} columns={[
                  { title: '节点', dataIndex: 'name' },
                  { title: '角色', dataIndex: 'role', width: 90 },
                  { title: '状态', dataIndex: 'status', width: 100, render: (s) => <StatusBadge text={s} tone={clusterTone(s)} /> },
                  { title: 'IP', dataIndex: 'ip', width: 130 },
                  { title: 'OS', dataIndex: 'os', ellipsis: true },
                  { title: 'CPU 用量', width: 130, render: (_, r) => { const m = nodeMetrics[r.name]; return m ? `${m.cpu_usage_pct}% (${fmtNodeCpu(m.cpu_usage)}/${m.cpu_capacity})` : (r.cpu || '-') } },
                  { title: '内存用量', width: 150, render: (_, r) => { const m = nodeMetrics[r.name]; return m ? `${m.mem_usage_pct}% (${fmtNodeMem(m.mem_usage)}/${fmtNodeMem(m.mem_capacity)})` : (r.memory || '-') } },
                ]} />
              )},
              { key: 'namespaces', label: `命名空间 (${namespaces.length})`, children: (
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
                  {namespaces.map((n) => <Tag key={n}>{n}</Tag>)}
                </div>
              )},
              { key: 'events', label: `事件 (${events.length})`, children: (
                <Table rowKey={(_, i) => String(i)} size="small" dataSource={events as any[]} pagination={{ pageSize: 10 }} columns={[
                  { title: '时间', dataIndex: 'lastTimestamp', width: 180 },
                  { title: '类型', dataIndex: 'type', width: 90 },
                  { title: '原因', dataIndex: 'reason', width: 140 },
                  { title: '对象', dataIndex: 'involvedObject', render: (o) => o ? `${o.kind}/${o.name}` : '' },
                  { title: '信息', dataIndex: 'message', ellipsis: true },
                ]} />
              )},
            ]} />
          </div>
        )}
      </Modal>
    </div>
  )
}

// ---- 审计日志 ----
// P2-6: 状态词（operator 为审批/执行状态时原值展示并加 Tag，不做用户映射）
const OPERATOR_STATUS_WORDS = new Set([
  'approved', 'rejected', 'success', 'failed', 'pending', 'skipped',
  'executed', 'denied', 'cancelled', 'auto', 'system', 'cron',
])

function operatorView(op: unknown, userMap: Record<string, string>): React.ReactNode {
  if (op === null || op === undefined || op === '') return '-'
  const s = String(op).trim()
  if (!s) return '-'
  // 纯数字 → 用户 ID，映射为 display_name / username
  if (/^\d+$/.test(s)) {
    const name = userMap[s]
    return name ? <span>{name}</span> : <span>用户#{s}</span>
  }
  // 状态词 → 原值 + Tag 标注
  if (OPERATOR_STATUS_WORDS.has(s.toLowerCase())) {
    return <Tag color="blue">{s}</Tag>
  }
  return <span>{s}</span>
}

function AuditLog() {
  const [rows, setRows] = useState<any[]>([])
  const [loading, setLoading] = useState(false)
  // P2-6: 加载用户列表建立 id → 显示名 映射，审计日志 operator 为纯数字用户 ID 时显示用户名
  const [userMap, setUserMap] = useState<Record<string, string>>({})

  useEffect(() => {
    listUsers().then((r) => {
      const d = r.data
      const list = Array.isArray(d) ? d : (d?.users ?? d?.data ?? [])
      const m: Record<string, string> = {}
      list.forEach((u: any) => {
        if (u?.id != null) m[String(u.id)] = u.display_name || u.username || String(u.id)
      })
      setUserMap(m)
    }).catch(() => { /* 用户列表不可用时保持 ID 兜底展示 */ })
  }, [])

  useEffect(() => {
    setLoading(true)
    listAuditLogs({ limit: 100 })
      .then((r) => {
        const d = r.data
        setRows(Array.isArray(d) ? d : (d?.items ?? d?.data ?? []))
      })
      .catch(() => setRows([]))
      .finally(() => setLoading(false))
  }, [])

  return (
    <div>
      <div style={{ fontSize: 15, fontWeight: 600, marginBottom: 16 }}>审计日志</div>
      <Table
        rowKey={(r, i) => r?.id ?? String(i)}
        size="small"
        loading={loading}
        dataSource={rows}
        pagination={{ pageSize: 20 }}
        columns={[
          // Issue8: 后端审计日志返回 created_at（非 timestamp）；缺失时回退 target
          { title: '时间', dataIndex: 'created_at', width: 180, render: (v, r: any) => (r.created_at || r.timestamp || '').replace('T', ' ').slice(0, 19) },
          { title: '操作', dataIndex: 'action', width: 120, render: (v) => <Tag>{v}</Tag> },
          // P2-6: operator 为纯数字用户 ID → 映射用户名；状态词 → Tag；其他原样；全程容错
          { title: '操作人', dataIndex: 'operator', width: 130, render: (v, r: any) => r?.operator_name ? <span>{r.operator_name}</span> : operatorView(r?.operator, userMap) },
          { title: '目标服务', dataIndex: 'target_service', width: 140, render: (v, r: any) => (r.target_service || r.target || '-') },
          { title: '命令', dataIndex: 'command', ellipsis: true },
          { title: '结果', dataIndex: 'result', width: 90 },
        ]}
      />
    </div>
  )
}


// ---- LLM 配置：预设厂商 + 自动拉取模型 + 保存前验证 API Key ----
function LLMConfig() {
  const [form] = Form.useForm()
  const [loading, setLoading] = useState(true)
  const [models, setModels] = useState([])
  const [modelsLoading, setModelsLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [hasSaved, setHasSaved] = useState(false)
  const [cfg, setCfg] = useState<{ configured: boolean; api_key_set: boolean } | null>(null)
  const [testing, setTesting] = useState(false)
  // P0-1 感知: 页面加载时自动探测 LLM 配置是否可用，异常时顶部提示
  const [configAlert, setConfigAlert] = useState(false)

  useEffect(() => {
    getLLMSettings().then((r) => {
      const d = r.data?.data || r.data
      if (d) {
        form.setFieldsValue({
          provider: d.active_provider || d.provider || 'deepseek',
          base_url: d.base_url || '',
          model: d.model || '',
          // 后端已脱敏（api_key_masked），前端直接回显，不二次脱敏
          api_key: d.api_key_masked || '',
        })
        setCfg({ configured: !!d.configured, api_key_set: !!d.api_key_set })
        // P0-1: 已配置时自动测一次连接（空 body），失败则提示重新填写 API Key
        if (d.configured) {
          testLLMConnection({}).then((tr) => {
            if (tr.data && tr.data.success === false) setConfigAlert(true)
          }).catch(() => { /* 接口异常不阻塞页面 */ })
        }
      }
    }).catch(() => {}).finally(() => setLoading(false))
  }, [])

  const onProviderChange = (key: string) => {
    const v = LLM_VENDORS.find((x) => x.key === key)
    if (v) {
      form.setFieldsValue({ base_url: v.base_url, model: v.default_model || '' })
      setModels([])
    }
  }

  const fetchModels = async () => {
    const base_url = form.getFieldValue('base_url')
    const api_key = form.getFieldValue('api_key')
    if (!base_url) { message.warning('请先填写 Base URL'); return }
    setModelsLoading(true)
    try {
      const r = await listLLMModels({ base_url, api_key })
      const list = Array.isArray(r.data) ? r.data : r.data?.models || r.data?.data || []
      const names = list.map((m: any) => (typeof m === 'string' ? m : m.id || m.name || m.model)).filter(Boolean)
      setModels(names)
      if (names.length && !form.getFieldValue('model')) form.setFieldValue('model', names[0])
      message.success(names.length ? ('拉取到 ' + names.length + ' 个模型') : '未返回模型')
    } catch (e) {
      message.error((e as any)?.response?.data?.message || '拉取模型失败')
    } finally {
      setModelsLoading(false)
    }
  }

  const onSave = async () => {
    const v = await form.validateFields()
    setSaving(true)
    try {
      await testLLMConnection({ provider: v.provider, base_url: v.base_url, model: v.model, api_key: v.api_key })
      await saveLLMSettings({ provider: v.provider, base_url: v.base_url, model: v.model, api_key: v.api_key })
      setHasSaved(true)
      setCfg({ configured: true, api_key_set: !!v.api_key })
      message.success('配置已保存')
    } catch (e) {
      message.error((e as any)?.response?.data?.message || 'API Key 验证失败，未保存')
    } finally {
      setSaving(false)
    }
  }

  // 测试当前生效配置是否可用（主动确认）
  const testCurrent = async () => {
    const v = await form.validateFields().catch(() => null)
    if (!v) return
    setTesting(true)
    try {
      await testLLMConnection({ provider: v.provider, base_url: v.base_url, model: v.model, api_key: v.api_key })
      message.success('连接正常：API Key 与模型可用')
    } catch (e) {
      message.error((e as any)?.response?.data?.message || '连接失败：请检查 API Key / Base URL')
    } finally {
      setTesting(false)
    }
  }

  return (
    <div style={{ maxWidth: 720 }}>
      {configAlert && (
        <Alert type="warning" showIcon closable style={{ marginBottom: 16 }}
          message="检测到 LLM 配置异常（API Key 可能已失效），请重新填写 API Key" />
      )}
      <div className="card" style={{ padding: 20 }}>
        <div style={{ fontSize: 15, fontWeight: 600, marginBottom: 4 }}>AI 模型配置</div>
        <div style={{ fontSize: 12, color: 'var(--text-muted)', marginBottom: 16 }}>
          选择厂商后自动填充 Base URL；可点击"获取模型"从接口拉取模型列表。保存时先验证 API Key 可用，通过后才写入数据库。
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 16, padding: '10px 14px', borderRadius: 8, background: 'var(--surface-2)' }}>
          <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--text)' }}>当前配置状态</span>
          {cfg ? (cfg.configured
            ? <Tag color="green" style={{ margin: 0 }}>已配置</Tag>
            : <Tag style={{ margin: 0 }}>未配置</Tag>) : <Tag style={{ margin: 0 }}>未知</Tag>}
          <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>
            {cfg?.configured ? (() => { const p = form.getFieldValue('provider'); const m = form.getFieldValue('model'); const b = form.getFieldValue('base_url'); return `生效：${p || '-'} · ${m || '-'}${b ? ' · ' + b : ''}` })() : '尚未保存任何 LLM 配置，AI 能力暂不可用'}
          </span>
          <Button size="small" loading={testing} onClick={testCurrent} style={{ marginLeft: 'auto' }}>测试当前配置</Button>
        </div>
        <Alert type="info" showIcon style={{ marginBottom: 16 }} message="不同厂商 /models 路径统一为 OpenAI 兼容风格。" />
        <Form form={form} layout="vertical" disabled={loading}>
          <Form.Item name="provider" label="模型供应商" rules={[{ required: true }]}>
            <Select showSearch onChange={onProviderChange} options={LLM_VENDORS.map((v) => ({ value: v.key, label: v.name }))} />
          </Form.Item>
          <Form.Item name="base_url" label="Base URL" rules={[{ required: true, message: '请输入 Base URL' }]}>
            <Input placeholder="https://api.deepseek.com/v1" />
          </Form.Item>
          <Form.Item name="model" label="模型" rules={[{ required: true, message: '请选择或输入模型' }]}>
            <Select showSearch allowClear options={models.map((m) => ({ value: m, label: m }))} notFoundContent="可手动输入或先获取模型" />
          </Form.Item>
          <Form.Item name="api_key" label="API Key" rules={[{ required: true, message: '请输入 API Key' }]}>
            <Input.Password placeholder="sk-..." />
          </Form.Item>
          <Form.Item>
            <Space>
              <Button type="primary" loading={saving} onClick={onSave}>保存</Button>
              <Button loading={modelsLoading} onClick={fetchModels}>获取模型</Button>
              {hasSaved && <Tag color="green" style={{ margin: 0 }}>已保存</Tag>}
            </Space>
          </Form.Item>
        </Form>
      </div>
    </div>
  )
}

// ---- 平台健康（5s 轮询组件状态）----
function PlatformHealth() {
  const [list, setList] = useState<SystemComponent[]>([])
  const [loading, setLoading] = useState(true)

  const load = async (silent = false) => {
    if (!silent) setLoading(true)
    try {
      const r = await getSystemComponents()
      const d = r.data as any
      setList(Array.isArray(d) ? d : (d?.components ?? d?.items ?? d?.data ?? []))
    } catch {
      setList([])
    } finally {
      if (!silent) setLoading(false)
    }
  }

  useEffect(() => {
    load(false)
    const timer = setInterval(() => load(true), 5000) // 5s 轮询
    return () => clearInterval(timer)
  }, [])

  // 状态 → Tag 颜色：ok=绿 degraded=橙 down=红
  const tone = (s?: string) => {
    if (s === 'ok' || s === 'healthy' || s === 'up' || s === 'running') return 'green'
    if (s === 'degraded' || s === 'warning' || s === 'warn' || s === 'unknown') return 'orange'
    if (s === 'down' || s === 'error' || s === 'failed' || s === 'critical' || s === 'stopped') return 'red'
    return 'default'
  }

  return (
    <div>
      <div style={{ fontSize: 15, fontWeight: 600, marginBottom: 4 }}>平台健康</div>
      <div style={{ fontSize: 12, color: 'var(--text-muted)', marginBottom: 16 }}>平台各系统组件运行状态，每 5 秒自动刷新。</div>
      <Table
        rowKey={(r) => `${r.type}-${r.name}`}
        size="small"
        loading={loading}
        dataSource={list}
        pagination={false}
        locale={{ emptyText: <Empty text="暂无组件状态数据" hint="后端 /system/components 尚未上报组件状态" /> }}
        columns={[
          { title: '组件', dataIndex: 'name' },
          { title: '类型', dataIndex: 'type', width: 140 },
          { title: '状态', dataIndex: 'status', width: 120, render: (s?: string) => <Tag color={tone(s)}>{s || '-'}</Tag> },
          { title: '延迟', dataIndex: 'latency_ms', width: 100, render: (v?: number) => (v != null ? `${v}ms` : '-') },
          { title: '详情', dataIndex: 'detail', ellipsis: true },
        ]}
      />
    </div>
  )
}

const AdminSettings: React.FC = () => {
  const [form] = Form.useForm()
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    getLLMSettings().then((r) => {
      const d = r.data?.data || r.data
      if (d) {
        form.setFieldsValue({
          provider: d.active_provider || d.provider || '',
          base_url: d.base_url || '',
          model: d.model || '',
          api_key: d.api_key ? String(d.api_key).slice(0, 4) + '***' : '',
        })
      }
    }).catch(() => {}).finally(() => setLoading(false))
  }, [])

  const llmTab = (
    <div style={{ maxWidth: 640 }}>
      <div className="card" style={{ padding: 20 }}>
        <div style={{ fontSize: 15, fontWeight: 600, marginBottom: 16 }}>AI 模型配置</div>
        <Form form={form} layout="vertical">
          <Form.Item name="provider" label="模型供应商">
            <Select options={[{ value: 'deepseek', label: 'DeepSeek' }, { value: 'openai', label: 'OpenAI' }, { value: 'qwen', label: '通义千问' }]} />
          </Form.Item>
          <Form.Item name="base_url" label="Base URL"><Input placeholder="https://api.deepseek.com" /></Form.Item>
          <Form.Item name="model" label="模型"><Input placeholder="deepseek-chat" /></Form.Item>
          <Form.Item name="api_key" label="API Key"><Input.Password placeholder="sk-..." /></Form.Item>
        </Form>
      </div>
    </div>
  )

  return (
    <div>
      <Breadcrumb items={[{ t: '系统管理' }, { t: '系统设置' }]} />
      <PageHeader title="系统设置" desc="AI 模型、纳管集群与平台基础配置" />
      <Tabs
        items={[
          { key: 'llm', label: 'AI 模型配置', children: <LLMConfig /> },
          { key: 'clusters', label: '纳管集群', children: <ClusterManager /> },
          { key: 'audit', label: '审计日志', children: <AuditLog /> },
          { key: 'health', label: '平台健康', children: <PlatformHealth /> },
        ]}
      />
    </div>
  )
}

export default AdminSettings
