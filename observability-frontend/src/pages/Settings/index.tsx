import React, { useEffect, useState } from 'react'
import { Card, Form, Input, Select, Button, message, Alert, Space, Tag, Table, Popconfirm, Typography, Modal } from 'antd'
import { SaveOutlined, ApiOutlined, CloudServerOutlined, ReloadOutlined, RadarChartOutlined, PlusOutlined, EditOutlined, DeleteOutlined, CheckCircleOutlined, CloseCircleOutlined } from '@ant-design/icons'
import api from '../../api/client'
import dayjs from 'dayjs'

const { Text } = Typography

interface Provider {
  id: number
  name: string
  vendor: string
  type: string
  base_url: string
  default_model: string
  available: boolean
  enabled: boolean
  api_key_masked: string
  created_at: string
}

// 厂商预设
const VENDOR_PRESETS: Record<string, { base_url: string; models: string[] }> = {
  xiaomi: { base_url: 'https://api.xiaomimimo.com/v1', models: ['mimo-v2.5-pro', 'mimo-v2.5', 'mimo-v2'] },
  deepseek: { base_url: 'https://api.deepseek.com/v1', models: ['deepseek-chat', 'deepseek-reasoner'] },
  openai: { base_url: 'https://api.openai.com/v1', models: ['gpt-4o', 'gpt-4o-mini', 'gpt-4-turbo'] },
  qwen: { base_url: 'https://dashscope.aliyuncs.com/compatible-mode/v1', models: ['qwen-turbo', 'qwen-plus', 'qwen-max'] },
  kimi: { base_url: 'https://api.moonshot.cn/v1', models: ['moonshot-v1-8k', 'moonshot-v1-32k', 'moonshot-v1-128k'] },
}

const Settings: React.FC = () => {
  const [dfForm] = Form.useForm()
  const [providers, setProviders] = useState<Provider[]>([])
  const [providerModalOpen, setProviderModalOpen] = useState(false)
  const [editingProvider, setEditingProvider] = useState<Provider | null>(null)
  const [providerForm] = Form.useForm()
  const [dfStatus, setDfStatus] = useState<string>('检查中...')
  const [enablingId, setEnablingId] = useState<number | null>(null)
  const [testingId, setTestingId] = useState<number | null>(null)
  const [testResults, setTestResults] = useState<Record<number, { ok: boolean; msg: string }>>({})
  // 恢复白名单
  const [policy, setPolicy] = useState<{ allow: string[]; deny: string[] }>({ allow: [], deny: [] })
  const [policyDirty, setPolicyDirty] = useState(false)
  const loadPolicy = async () => {
    try {
      const r = await api.get('/ops/recovery/policy')
      const p = r?.data
      setPolicy({ allow: p?.allow || [], deny: p?.deny || [] })
    } catch { /* 静默 */ }
  }
  useEffect(() => { loadPolicy() }, [])
  const savePolicy = async () => {
    try {
      await api.put('/ops/recovery/policy', policy)
      message.success('恢复白名单已保存')
      setPolicyDirty(false)
    } catch { message.error('保存失败（需管理员/审批人权限）') }
  }
  const [curModels, setCurModels] = useState<string[]>([])
  const [fetchingModels, setFetchingModels] = useState(false)

  useEffect(() => {
    loadProviders()
    const host = window.location.hostname
    const defaultDF = `http://${host}:30417`
    const defaultGF = `http://${host}:32060`
    const dfUrl = localStorage.getItem('deepflowUrl') || defaultDF
    const gfUrl = localStorage.getItem('grafanaUrl') || defaultGF
    dfForm.setFieldsValue({ url: dfUrl, grafana_url: gfUrl })
    checkDeepFlow(dfUrl)
  }, [])

  const loadProviders = async () => {
    try {
      const r = await api.get('/settings/llm/providers')
      setProviders(r.data?.providers || [])
    } catch { }
  }

  const checkDeepFlow = async (url: string) => {
    try {
      const r = await api.get('/deepflow/status')
      const status = r.data?.status || r.data?.data?.status
      setDfStatus(status === 'available' ? '已连接 ✅' : '未连接')
    } catch { setDfStatus('未连接') }
  }

  const saveProvider = async (vals: any) => {
    try {
      if (editingProvider) {
        await api.put(`/settings/llm/providers/${editingProvider.id}`, vals)
        message.success('Provider 已更新')
      } else {
        await api.post('/settings/llm/providers', vals)
        message.success('Provider 已新增')
      }
      setProviderModalOpen(false)
      providerForm.resetFields()
      setEditingProvider(null)
      loadProviders()
    } catch (err: any) {
      message.error(`保存失败: ${err?.response?.data?.error || err?.message}`)
    }
  }

  const deleteProvider = async (id: number) => {
    try {
      await api.delete(`/settings/llm/providers/${id}`)
      message.success('已删除')
      loadProviders()
    } catch (err: any) {
      message.error(`删除失败: ${err?.response?.data?.error || err?.message}`)
    }
  }

  const enableProvider = async (p: Provider) => {
    setEnablingId(p.id)
    message.loading({ content: '正在启用...', key: 'enable' })
    try {
      // 直接启用（启用是主动操作，不强制要求当前可用）
      await api.post(`/settings/llm/providers/${p.id}/enable`)
      message.success({ content: `已启用: ${p.name}`, key: 'enable' })
      loadProviders()
      // 启用后软性检查可用性（仅提示，不阻止）
      try {
        const testR = await api.post('/settings/llm/test', {
          api_key: '',
          base_url: p.base_url,
          model: p.default_model,
          provider_id: String(p.id),
        })
        if (testR.data?.success !== true) {
          message.warning({ content: `已启用，但连接测试失败: ${testR.data?.message || '连接不通'}（可稍后编辑完善 API Key）`, key: 'enable' })
        }
      } catch {
        // 忽略检查错误
      }
    } catch (err: any) {
      message.error({ content: `启用失败: ${err?.response?.data?.error || err?.message}`, key: 'enable' })
    }
    setEnablingId(null)
  }

  const testProviderConnection = async (p: Provider) => {
    setTestingId(p.id)
    try {
      const r = await api.post('/settings/llm/test', {
        api_key: '', // 后端按 provider_id 读取该 provider 的 key
        base_url: p.base_url,
        model: p.default_model,
        provider_id: String(p.id),
      })
      const ok = r.data?.success === true
      const msg = r.data?.message || (ok ? '连接成功' : '连接失败')
      setTestResults(prev => ({ ...prev, [p.id]: { ok, msg } }))
      if (ok) {
        message.success(`${p.name}: ${msg}`)
      } else {
        message.warning(`${p.name}: ${msg}`)
      }
    } catch (err: any) {
      const msg = err?.response?.data?.message || err?.message || '请求失败'
      setTestResults(prev => ({ ...prev, [p.id]: { ok: false, msg } }))
      message.error(`${p.name}: ${msg}`)
    }
    setTestingId(null)
  }

  const openAddProvider = () => {
    setEditingProvider(null)
    providerForm.resetFields()
    setProviderModalOpen(true)
  }

  const onVendorChange = (vendor: string) => {
    const preset = VENDOR_PRESETS[vendor]
    if (preset) {
      providerForm.setFieldsValue({ base_url: preset.base_url, default_model: undefined })
      setCurModels(preset.models)
    }
  }

  const openEditProvider = (p: Provider) => {
    setEditingProvider(p)
    const vendor = p.vendor || ''
    const preset = VENDOR_PRESETS[vendor]
    setCurModels(preset?.models || [])
    providerForm.setFieldsValue({
      name: p.name,
      vendor: vendor,
      type: p.type,
      base_url: p.base_url,
      default_model: p.default_model,
    })
    setProviderModalOpen(true)
  }

  const fetchModelsFromAPI = async () => {
    const vals = providerForm.getFieldsValue()
    if (!vals.api_key || !vals.base_url) { message.warning('请先填写 API Key 和 Base URL'); return }
    setFetchingModels(true)
    try {
      const r = await api.post('/settings/llm/models', { api_key: vals.api_key, base_url: vals.base_url })
      const models = r.data?.models || []
      if (models.length > 0) {
        setCurModels(models.map((m: any) => typeof m === 'string' ? m : m.id))
        message.success(`获取到 ${models.length} 个模型`)
      } else { message.info('未获取到模型列表') }
    } catch (err: any) { message.error('获取模型失败') }
    setFetchingModels(false)
  }

  // ── 渲染 ──
  const columns = [
    { title: '名称', dataIndex: 'name', width: 140 },
    { title: '厂商', dataIndex: 'vendor', width: 90, render: (v: string) => <Tag color="blue">{v || '-'}</Tag> },
    { title: 'Base URL', dataIndex: 'base_url', ellipsis: true, render: (v: string) => <Text code style={{ fontSize: 12 }}>{v || '-'}</Text> },
    { title: '默认模型', dataIndex: 'default_model', width: 150, render: (v: string) => <Text strong>{v || '-'}</Text> },
    {
      title: '连通性', width: 80, render: (_: any, r: Provider) => {
        const tr = testResults[r.id]
        if (!tr) return <Text type="secondary" style={{ fontSize: 12 }}>未测试</Text>
        return tr.ok
          ? <Tag color="success" icon={<CheckCircleOutlined />}>可用</Tag>
          : <Tag color="error" icon={<CloseCircleOutlined />}>不通</Tag>
      }
    },
    { title: '状态', dataIndex: 'enabled', width: 90, render: (v: boolean) => v ? <Tag color="green">当前使用</Tag> : <Tag>未启用</Tag> },
    {
      title: '操作', width: 300, render: (_: any, r: Provider) => (
        <Space size={4}>
          {!r.enabled && (
            <Button size="small" type="primary" loading={enablingId === r.id}
              onClick={() => enableProvider(r)}>设为启用</Button>
          )}
          <Button size="small" loading={testingId === r.id}
            onClick={() => testProviderConnection(r)}>测试连接</Button>
          <Button size="small" type="link" icon={<EditOutlined />} onClick={() => openEditProvider(r)}>编辑</Button>
          <Button size="small" type="link" danger icon={<DeleteOutlined />} onClick={() => deleteProvider(r.id)}>删除</Button>
        </Space>
      )
    },
  ]

  return (
    <div>
      <Card
        title={<Space><ApiOutlined style={{ color: '#1677ff' }} /> 智能体配置</Space>}
        extra={<Button icon={<ReloadOutlined />} onClick={loadProviders}>刷新</Button>}
        size="small" style={{ marginBottom: 12 }}
      >
        <div style={{ marginBottom: 16, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <span style={{ fontSize: 14, fontWeight: 500 }}>模型供应商</span>
          <Button type="primary" size="small" icon={<PlusOutlined />} onClick={openAddProvider}>新增提供商</Button>
        </div>

        <Table
          dataSource={providers}
          columns={columns}
          rowKey="id"
          size="small"
          pagination={false}
          locale={{ emptyText: '暂无 Provider。请点击"新增提供商"添加。' }}
        />
      </Card>

      {/* 新增/编辑 Provider 弹窗 */}
      <Modal
        title={editingProvider ? '编辑模型提供商' : '新增模型提供商'}
        open={providerModalOpen}
        onCancel={() => { setProviderModalOpen(false); setEditingProvider(null); providerForm.resetFields() }}
        onOk={() => providerForm.submit()}
        okText="保存"
        width={600}
      >
        <Form form={providerForm} layout="vertical" onFinish={saveProvider} initialValues={{ type: 'openai_compatible' }}>
          <Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入名称' }]}>
            <Input placeholder="例: 小米生产环境" />
          </Form.Item>
          <Form.Item name="vendor" label="厂商">
            <Select onChange={onVendorChange} allowClear placeholder="选择厂商自动填充" options={[
              { value: 'xiaomi', label: '小米' },
              { value: 'deepseek', label: 'DeepSeek' },
              { value: 'openai', label: 'OpenAI' },
              { value: 'qwen', label: '通义千问' },
              { value: 'kimi', label: 'Kimi (月之暗面)' },
            ]} />
          </Form.Item>
          <Form.Item name="type" label="类型" rules={[{ required: true }]}>
            <Select options={[
              { value: 'openai_compatible', label: 'openai_compatible' },
              { value: 'anthropic', label: 'anthropic' },
              { value: 'azure_openai', label: 'azure_openai' },
            ]} />
          </Form.Item>
          <Form.Item name="api_key" label="API Key" rules={[{ required: !editingProvider, message: '请输入 API Key' }]}>
            <Input.Password placeholder={editingProvider ? '留空表示不修改' : 'sk-...'} />
          </Form.Item>
          <Form.Item name="base_url" label="Base URL" rules={[{ required: true }]}>
            <Input placeholder="https://api.example.com/v1" />
          </Form.Item>
          <Form.Item name="default_model" label="默认模型" rules={[{ required: true }]}>
            <Select options={curModels.map(m => ({ value: m, label: m }))} showSearch allowClear placeholder="选择模型"
              dropdownRender={menu => (<>{menu}<div style={{ padding: '8px 12px', borderTop: '1px solid #f0f0f0' }}><Button type="link" size="small" icon={<ReloadOutlined />} loading={fetchingModels} onClick={fetchModelsFromAPI}>从 API 拉取模型列表</Button></div></>)} />
          </Form.Item>
        </Form>
      </Modal>

      {/* DeepFlow 配置 */}
      <Card title="DeepFlow 配置" size="small" style={{ marginTop: 12 }}>
        <Form form={dfForm} layout="vertical" onFinish={(vals) => {
          localStorage.setItem('deepflowUrl', vals.url)
          if (vals.grafana_url) localStorage.setItem('grafanaUrl', vals.grafana_url)
          message.success('DeepFlow 配置已保存')
          checkDeepFlow(vals.url)
        }} initialValues={{ url: `http://${window.location.hostname}:30417`, grafana_url: `http://${window.location.hostname}:32060` }}>
          <Form.Item label="连接状态">
            <Tag color={dfStatus.includes('已连接') ? 'green' : 'orange'}>{dfStatus}</Tag>
          </Form.Item>
          <Form.Item name="url" label="DeepFlow Server 地址" rules={[{ required: true }]}>
            <Input placeholder={`http://${window.location.hostname}:30417`} />
          </Form.Item>
          <Form.Item name="grafana_url" label="DeepFlow Grafana 地址">
            <Input placeholder={`http://${window.location.hostname}:32060`} />
          </Form.Item>
          <Form.Item label="默认账号"><Tag>admin / deepflow</Tag></Form.Item>
          <Space>
            <Button type="primary" htmlType="submit" icon={<SaveOutlined />}>保存</Button>
            <Button onClick={() => { const url = dfForm.getFieldValue('url'); checkDeepFlow(url) }}>测试连接</Button>
          </Space>
        </Form>
      </Card>

      {/* K8s 集群 */}
      <Card title="K8s 集群" size="small" style={{ marginTop: 12 }}>
        <Space>
          <Tag icon={<CloudServerOutlined />} color="green">本地集群</Tag>
          <Tag color="blue">ServiceAccount</Tag>
          <Tag color="green">已连接</Tag>
        </Space>
        <div style={{ color: '#666', fontSize: 13, marginTop: 8 }}>
          当前平台运行在 Kubernetes 集群中，通过 In-Cluster ServiceAccount 自动获取集群资源。
        </div>
      </Card>

      {/* 恢复白名单（安全边界） */}
      <Card title={<Space><RadarChartOutlined /> 恢复白名单（安全边界）</Space>} size="small" style={{ marginTop: 12 }}
        extra={<Button type="primary" size="small" icon={<SaveOutlined />} disabled={!policyDirty} onClick={savePolicy}>保存</Button>}>
        <Alert type="info" showIcon style={{ marginBottom: 12 }}
          message="告警恢复执行前，恢复命令必须命中白名单。允许列表 = 可自动恢复操作；禁止列表 = 一律拦截。修改需管理员/审批人权限。" />
        <Space direction="vertical" style={{ width: '100%' }} size="middle">
          <div>
            <Typography.Text strong>允许（可自动恢复）</Typography.Text>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, marginTop: 8 }}>
              {policy.allow.map((c, i) => (
                <Tag key={i} closable color="green" onClose={() => { setPolicy({ ...policy, allow: policy.allow.filter((_, j) => j !== i) }); setPolicyDirty(true) }}>
                  {c}
                </Tag>
              ))}
              <Input
                size="small" placeholder="新增允许命令" style={{ width: 260 }}
                onPressEnter={(e: any) => {
                  const v = (e.target.value || '').trim()
                  if (v && !policy.allow.includes(v)) { setPolicy({ ...policy, allow: [...policy.allow, v] }); setPolicyDirty(true) }
                  e.target.value = ''
                }}
              />
            </div>
          </div>
          <div>
            <Typography.Text strong>禁止（一律拦截）</Typography.Text>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, marginTop: 8 }}>
              {policy.deny.map((c, i) => (
                <Tag key={i} closable color="red" onClose={() => { setPolicy({ ...policy, deny: policy.deny.filter((_, j) => j !== i) }); setPolicyDirty(true) }}>
                  {c}
                </Tag>
              ))}
              <Input
                size="small" placeholder="新增禁止命令" style={{ width: 260 }}
                onPressEnter={(e: any) => {
                  const v = (e.target.value || '').trim()
                  if (v && !policy.deny.includes(v)) { setPolicy({ ...policy, deny: [...policy.deny, v] }); setPolicyDirty(true) }
                  e.target.value = ''
                }}
              />
            </div>
          </div>
        </Space>
      </Card>
    </div>
  )
}

export default Settings
