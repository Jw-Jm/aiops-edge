import React, { useState, useEffect, useCallback } from 'react'
import { Card, Table, Tag, Space, Button, Modal, Form, Input, Select, Switch, message, Typography } from 'antd'
import { PlusOutlined, ReloadOutlined, DeleteOutlined, EditOutlined } from '@ant-design/icons'
import api from '../../api/client'

const { Text } = Typography

interface Rule {
  rule_key: string; name: string; kind: string; severity: string; enabled: boolean
  scope_type: string; join_mode: string; conditions_json: any; source_type: string
}

const KIND_MAP: Record<string, { color: string; label: string }> = {
  metric: { color: 'blue', label: '指标' },
  log:    { color: 'purple', label: '日志' },
  trace:  { color: 'geekblue', label: '链路' },
  approval: { color: 'orange', label: '审批' },
  flow:   { color: 'cyan', label: '工作流' },
}

const SEV_MAP: Record<string, { color: string; label: string }> = {
  warning:  { color: 'orange', label: '警告' },
  critical: { color: 'red', label: '严重' },
  info:     { color: 'blue', label: '信息' },
}

const Rules: React.FC = () => {
  const [rules, setRules] = useState<Rule[]>([])
  const [loading, setLoading] = useState(false)
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<Rule | null>(null)
  const [form] = Form.useForm()

  const fetch = useCallback(async () => {
    setLoading(true)
    try {
      const r = await api.get('/ai/rules')
      setRules(r.data?.rules || [])
    } catch { /* ignore */ }
    setLoading(false)
  }, [])

  useEffect(() => { fetch() }, [fetch])

  const openAdd = () => { setEditing(null); form.resetFields(); setModalOpen(true) }
  const openEdit = (r: Rule) => {
    setEditing(r)
    form.setFieldsValue({ ...r, conditions_json: r.conditions_json ? JSON.stringify(r.conditions_json) : '' })
    setModalOpen(true)
  }

  const handleSave = async () => {
    const v = await form.validateFields()
    let conditions_json = v.conditions_json
    try { conditions_json = conditions_json ? JSON.parse(conditions_json) : {} } catch { message.error('条件表达式不是合法 JSON'); return }
    const payload = { ...v, conditions_json, source_type: editing?.source_type || 'custom' }
    try {
      await api.post('/ai/rules', payload)
      message.success(editing ? '已更新' : '已创建')
      setModalOpen(false)
      fetch()
    } catch (e: any) { message.error(e?.response?.data?.detail || '保存失败') }
  }

  const handleDelete = async (ruleKey: string) => {
    try {
      await api.delete(`/ai/rules/${encodeURIComponent(ruleKey)}`)
      message.success('已删除')
      fetch()
    } catch { message.error('删除失败') }
  }

  const handleToggle = async (rule: Rule) => {
    try {
      await api.post(`/ai/rules/${encodeURIComponent(rule.rule_key)}/toggle`)
      fetch()
    } catch { message.error('切换失败') }
  }

  const columns = [
    { title: '规则键', dataIndex: 'rule_key', key: 'rule_key', width: 160 },
    { title: '名称', dataIndex: 'name', key: 'name', width: 180 },
    { title: '类型', dataIndex: 'kind', key: 'kind', width: 100,
      render: (v: string) => KIND_MAP[v] ? <Tag color={KIND_MAP[v].color}>{KIND_MAP[v].label}</Tag> : v },
    { title: '级别', dataIndex: 'severity', key: 'severity', width: 90,
      render: (v: string) => SEV_MAP[v] ? <Tag color={SEV_MAP[v].color}>{SEV_MAP[v].label}</Tag> : v },
    { title: '来源', dataIndex: 'source_type', key: 'source_type', width: 90,
      render: (v: string) => <Tag>{v === 'builtin' ? '内置' : '自定义'}</Tag> },
    { title: '启用', dataIndex: 'enabled', key: 'enabled', width: 80,
      render: (v: boolean, r: Rule) => <Switch size="small" checked={!!v} onChange={() => handleToggle(r)} /> },
    { title: '条件', dataIndex: 'conditions_json', key: 'conditions_json', ellipsis: true,
      render: (v: any) => v ? <Text code style={{ fontSize: 11 }}>{typeof v === 'string' ? v : JSON.stringify(v)}</Text> : '-' },
    { title: '操作', key: 'action', width: 130, fixed: 'right' as const,
      render: (_: unknown, r: Rule) => (
        <Space>
          <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(r)}>编辑</Button>
          {r.source_type !== 'builtin' && (
            <Button size="small" danger icon={<DeleteOutlined />} onClick={() => handleDelete(r.rule_key)}>删除</Button>
          )}
        </Space>
      ) },
  ]

  return (
    <Card
      title="规则管理"
      extra={<Space>
        <Button icon={<ReloadOutlined />} onClick={fetch}>刷新</Button>
        <Button type="primary" icon={<PlusOutlined />} onClick={openAdd}>新增规则</Button>
      </Space>}
    >
      <Table rowKey="rule_key" columns={columns} dataSource={rules} loading={loading}
        pagination={{ pageSize: 20, showTotal: (t: number) => `共 ${t} 条` }} />

      <Modal title={editing ? '编辑规则' : '新增规则'} open={modalOpen} onOk={handleSave} onCancel={() => setModalOpen(false)} destroyOnClose width={560}>
        <Form form={form} layout="vertical" initialValues={{ kind: 'metric', severity: 'warning', enabled: true, scope_type: 'global', join_mode: 'all' }}>
          <Form.Item name="rule_key" label="规则键" rules={[{ required: true, message: '请输入唯一规则键' }]}>
            <Input placeholder="如 cpu_high" disabled={!!editing} />
          </Form.Item>
          <Form.Item name="name" label="规则名称" rules={[{ required: true, message: '请输入名称' }]}>
            <Input placeholder="如 CPU 使用率过高" />
          </Form.Item>
          <Space size="large" style={{ display: 'flex' }}>
            <Form.Item name="kind" label="类型" style={{ width: 160 }}>
              <Select options={Object.entries(KIND_MAP).map(([k, v]) => ({ value: k, label: v.label }))} />
            </Form.Item>
            <Form.Item name="severity" label="级别" style={{ width: 160 }}>
              <Select options={Object.entries(SEV_MAP).map(([k, v]) => ({ value: k, label: v.label }))} />
            </Form.Item>
            <Form.Item name="enabled" label="启用" valuePropName="checked">
              <Switch />
            </Form.Item>
          </Space>
          <Form.Item name="conditions_json" label="条件表达式（JSON，类型决定解释）">
            <Input.TextArea rows={3} placeholder='{"expr": "cpu>80", "window": "5m"}' />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  )
}

export default Rules
