import React, { useState, useEffect, useCallback } from 'react'
import { Card, Table, Tag, Space, Button, Modal, Form, Input, Select, InputNumber, message } from 'antd'
import { PlusOutlined, ReloadOutlined, DeleteOutlined, EditOutlined } from '@ant-design/icons'
import { listDevices, createDevice, updateDevice, deleteDevice, DeviceItem } from '../../api/client'

const STATUS_MAP: Record<string, { color: string; label: string }> = {
  online: { color: 'green', label: '在线' },
  offline: { color: 'red', label: '离线' },
  maintenance: { color: 'orange', label: '维护中' },
}

const Devices: React.FC = () => {
  const [items, setItems] = useState<DeviceItem[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<DeviceItem | null>(null)
  const [form] = Form.useForm()

  const fetch = useCallback(async () => {
    setLoading(true)
    try {
      const r = await listDevices({ page: 1, size: 200 })
      setItems(r.data?.devices || [])
      setTotal(r.data?.total || 0)
    } catch { /* ignore */ } finally { setLoading(false) }
  }, [])

  useEffect(() => { fetch() }, [fetch])

  const openAdd = () => { setEditing(null); form.resetFields(); setModalOpen(true) }
  const openEdit = (d: DeviceItem) => {
    setEditing(d)
    form.setFieldsValue(d)
    setModalOpen(true)
  }

  const handleSave = async () => {
    const v = await form.validateFields()
    try {
      if (editing) {
        await updateDevice(editing.id, v)
        message.success('已更新')
      } else {
        await createDevice(v)
        message.success('已创建')
      }
      setModalOpen(false); fetch()
    } catch (e: any) { message.error(e?.response?.data?.error || '保存失败') }
  }

  const handleDelete = async (id: number) => {
    try { await deleteDevice(id); message.success('已删除'); fetch() }
    catch { message.error('删除失败') }
  }

  const columns = [
    { title: 'ID', dataIndex: 'id', key: 'id', width: 70 },
    { title: '主机名', dataIndex: 'hostname', key: 'hostname', width: 150 },
    { title: 'IP', dataIndex: 'ip', key: 'ip', width: 130 },
    { title: 'OS', dataIndex: 'os', key: 'os', width: 130 },
    { title: 'CPU核', dataIndex: 'cpu_cores', key: 'cpu_cores', width: 90 },
    { title: '内存', dataIndex: 'memory_mb', key: 'memory_mb', width: 100,
      render: (v: number) => `${((v || 0) / 1024).toFixed(1)}G` },
    { title: '角色', dataIndex: 'role', key: 'role', width: 90 },
    { title: '状态', dataIndex: 'status', key: 'status', width: 100,
      render: (v: string) => STATUS_MAP[v] ? <Tag color={STATUS_MAP[v].color}>{STATUS_MAP[v].label}</Tag> : (v || '-') },
    { title: '位置', dataIndex: 'location', key: 'location', width: 110 },
    { title: '操作', key: 'action', width: 130, fixed: 'right' as const,
      render: (_: unknown, r: DeviceItem) => (
        <Space>
          <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(r)}>编辑</Button>
          <Button size="small" danger icon={<DeleteOutlined />} onClick={() => handleDelete(r.id)}>删除</Button>
        </Space>
      ) },
  ]

  return (
    <Card title={`设备管理（${total}）`} extra={<Space>
      <Button icon={<ReloadOutlined />} onClick={fetch}>刷新</Button>
      <Button type="primary" icon={<PlusOutlined />} onClick={openAdd}>新增设备</Button>
    </Space>}>
      <Table rowKey="id" columns={columns} dataSource={items} loading={loading}
        pagination={{ pageSize: 20, showTotal: (t: number) => `共 ${t} 条` }} scroll={{ x: 'max-content' }} />

      <Modal title={editing ? '编辑设备' : '新增设备'} open={modalOpen} onOk={handleSave} onCancel={() => setModalOpen(false)} destroyOnClose>
        <Form form={form} layout="vertical" initialValues={{ status: 'online', role: 'node' }}>
          <Form.Item name="hostname" label="主机名" rules={[{ required: true, message: '请输入主机名' }]}>
            <Input disabled={!!editing} placeholder="如 node-01" />
          </Form.Item>
          <Space size="large" style={{ display: 'flex' }}>
            <Form.Item name="ip" label="IP" style={{ width: 200 }}><Input /></Form.Item>
            <Form.Item name="os" label="操作系统" style={{ width: 200 }}><Input /></Form.Item>
          </Space>
          <Space size="large" style={{ display: 'flex' }}>
            <Form.Item name="cpu_cores" label="CPU核数" style={{ width: 150 }}><InputNumber min={0} style={{ width: '100%' }} /></Form.Item>
            <Form.Item name="memory_mb" label="内存(MB)" style={{ width: 150 }}><InputNumber min={0} style={{ width: '100%' }} /></Form.Item>
          </Space>
          <Space size="large" style={{ display: 'flex' }}>
            <Form.Item name="role" label="角色" style={{ width: 180 }}>
              <Select options={[{ value: 'node', label: '节点' }, { value: 'worker', label: '工作节点' }, { value: 'edge', label: '边缘' }]} />
            </Form.Item>
            <Form.Item name="status" label="状态" style={{ width: 180 }}>
              <Select options={Object.entries(STATUS_MAP).map(([k, v]) => ({ value: k, label: v.label }))} />
            </Form.Item>
          </Space>
          <Space size="large" style={{ display: 'flex' }}>
            <Form.Item name="location" label="位置" style={{ width: 200 }}><Input /></Form.Item>
            <Form.Item name="tags" label="标签" style={{ width: 200 }}><Input /></Form.Item>
          </Space>
        </Form>
      </Modal>
    </Card>
  )
}

export default Devices
