import React, { useState, useEffect, useCallback } from 'react'
import { Card, Table, Tag, Space, Button, Modal, Form, Input, Select, InputNumber, Switch, message } from 'antd'
import { PlusOutlined, ReloadOutlined, DeleteOutlined, EditOutlined } from '@ant-design/icons'
import { listSLOs, createSLO, updateSLO, deleteSLO, SLOTarget } from '../../api/client'

const TYPE_MAP: Record<string, { color: string; label: string }> = {
  availability: { color: 'green', label: '可用性' },
  latency: { color: 'blue', label: '延迟' },
}

const SLO: React.FC = () => {
  const [items, setItems] = useState<SLOTarget[]>([])
  const [loading, setLoading] = useState(false)
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<SLOTarget | null>(null)
  const [form] = Form.useForm()

  const fetch = useCallback(async () => {
    setLoading(true)
    try {
      const r = await listSLOs()
      setItems(r.data?.data || [])
    } catch { /* ignore */ } finally { setLoading(false) }
  }, [])

  useEffect(() => { fetch() }, [fetch])

  const openAdd = () => {
    setEditing(null)
    form.resetFields()
    form.setFieldsValue({ slo_type: 'availability', target: 99.9, window_seconds: 2592000, enabled: true })
    setModalOpen(true)
  }
  const openEdit = (d: SLOTarget) => {
    setEditing(d)
    form.setFieldsValue(d)
    setModalOpen(true)
  }

  const handleSave = async () => {
    const v = await form.validateFields()
    try {
      if (editing) {
        await updateSLO(editing.id, v)
        message.success('已更新')
      } else {
        await createSLO(v)
        message.success('已创建')
      }
      setModalOpen(false); fetch()
    } catch (e: any) { message.error(e?.response?.data?.error || '保存失败') }
  }

  const handleDelete = async (id: string) => {
    try { await deleteSLO(id); message.success('已删除'); fetch() }
    catch (e: any) { message.error(e?.response?.data?.error || '删除失败') }
  }

  const columns = [
    { title: '名称', dataIndex: 'name', key: 'name', width: 150 },
    { title: '服务', dataIndex: 'service', key: 'service', width: 140 },
    { title: '类型', dataIndex: 'slo_type', key: 'slo_type', width: 100,
      render: (v: string) => TYPE_MAP[v] ? <Tag color={TYPE_MAP[v].color}>{TYPE_MAP[v].label}</Tag> : (v || '-') },
    { title: '目标', dataIndex: 'target', key: 'target', width: 110,
      render: (v: number, r: SLOTarget) => r.slo_type === 'latency' ? `${v} ms` : `${v}%` },
    { title: '窗口', dataIndex: 'window_seconds', key: 'window_seconds', width: 110,
      render: (v: number) => `${((v || 0) / 86400).toFixed(0)} 天` },
    { title: '启用', dataIndex: 'enabled', key: 'enabled', width: 80,
      render: (v: boolean) => v ? <Tag color="green">启用</Tag> : <Tag>停用</Tag> },
    { title: '操作', key: 'action', width: 130, fixed: 'right' as const,
      render: (_: unknown, r: SLOTarget) => (
        <Space>
          <Button size="small" type="link" icon={<EditOutlined />} onClick={() => openEdit(r)}>编辑</Button>
          <Button size="small" type="link" danger icon={<DeleteOutlined />} onClick={() => handleDelete(r.id)}>删除</Button>
        </Space>
      ) },
  ]

  return (
    <Card
      title="SLO 目标管理"
      extra={
        <Space>
          <Button icon={<ReloadOutlined />} onClick={fetch}>刷新</Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={openAdd}>新增 SLO</Button>
        </Space>
      }
    >
      <Table rowKey="id" dataSource={items} columns={columns} loading={loading} pagination={{ pageSize: 20 }} />
      <Modal
        title={editing ? '编辑 SLO 目标' : '新增 SLO 目标'}
        open={modalOpen}
        onOk={handleSave}
        onCancel={() => setModalOpen(false)}
        destroyOnClose
      >
        <Form form={form} layout="vertical" preserve={false}>
          <Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入名称' }]}>
            <Input placeholder="如 payments-availability" />
          </Form.Item>
          <Form.Item name="service" label="服务" rules={[{ required: true, message: '请输入服务名' }]}>
            <Input placeholder="如 payments" />
          </Form.Item>
          <Form.Item name="slo_type" label="类型">
            <Select>
              <Select.Option value="availability">可用性</Select.Option>
              <Select.Option value="latency">延迟</Select.Option>
            </Select>
          </Form.Item>
          <Form.Item name="target" label="目标值">
            <InputNumber min={0} step={0.1} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="window_seconds" label="SLO 窗口（秒，30天=2592000）">
            <InputNumber min={60} step={86400} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="enabled" label="启用" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  )
}

export default SLO
