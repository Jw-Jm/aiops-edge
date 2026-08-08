import React, { useState, useEffect, useCallback } from 'react'
import { Card, Table, Tag, Space, Button, Modal, Form, Input, Select, message, Drawer } from 'antd'
import { PlusOutlined, ReloadOutlined, DeleteOutlined, ThunderboltOutlined, ClusterOutlined } from '@ant-design/icons'
import { listSnmpDevices, createSnmpDevice, deleteSnmpDevice, listSnmpInterfaces, collectSnmpDevice, SnmpDevice, SnmpInterface } from '../../api/client'

const Snmp: React.FC = () => {
  const [items, setItems] = useState<SnmpDevice[]>([])
  const [loading, setLoading] = useState(false)
  const [modalOpen, setModalOpen] = useState(false)
  const [form] = Form.useForm()
  const [ifDrawer, setIfDrawer] = useState(false)
  const [interfaces, setInterfaces] = useState<SnmpInterface[]>([])
  const [ifDevice, setIfDevice] = useState('')
  const [collecting, setCollecting] = useState<number | null>(null)

  const fetch = useCallback(async () => {
    setLoading(true)
    try {
      const r = await listSnmpDevices()
      setItems(r.data?.devices || [])
    } catch { /* ignore */ } finally { setLoading(false) }
  }, [])

  useEffect(() => { fetch() }, [fetch])

  const handleAdd = async () => {
    const v = await form.validateFields()
    try {
      await createSnmpDevice(v)
      message.success('已添加')
      setModalOpen(false); form.resetFields(); fetch()
    } catch (e: any) { message.error(e?.response?.data?.detail || '添加失败') }
  }

  const handleDelete = async (id: number) => {
    try { await deleteSnmpDevice(id); message.success('已删除'); fetch() }
    catch { message.error('删除失败') }
  }

  const handleCollect = async (id: number) => {
    setCollecting(id)
    try {
      const r = await collectSnmpDevice(id)
      message.success(`采集完成，接口 ${r.data?.interfaces || 0} 个`)
      fetch()
    } catch (e: any) { message.error(e?.response?.data?.detail || '采集失败') } finally { setCollecting(null) }
  }

  const openInterfaces = async (d: SnmpDevice) => {
    try {
      const r = await listSnmpInterfaces(d.id)
      setInterfaces(r.data?.interfaces || [])
      setIfDevice(`${d.hostname} (${d.ip})`)
      setIfDrawer(true)
    } catch { message.error('获取接口失败') }
  }

  const columns = [
    { title: '主机名', dataIndex: 'hostname', key: 'hostname', width: 140,
      render: (v: string) => <><ClusterOutlined /> <b>{v || '-'}</b></> },
    { title: 'IP', dataIndex: 'ip', key: 'ip', width: 140 },
    { title: '厂商', dataIndex: 'vendor', key: 'vendor', width: 100 },
    { title: '型号', dataIndex: 'model', key: 'model', width: 100 },
    { title: '版本', dataIndex: 'snmp_version', key: 'snmp_version', width: 80 },
    { title: '状态', dataIndex: 'status', key: 'status', width: 90,
      render: (v: string) => v === 'active' ? <Tag color="green">启用</Tag> : <Tag color="red">禁用</Tag> },
    { title: '最后采集', dataIndex: 'last_collect_at', key: 'last_collect_at', width: 170,
      render: (v: string) => v || '-' },
    { title: '操作', key: 'action', width: 250, fixed: 'right' as const,
      render: (_: unknown, r: SnmpDevice) => (
        <Space>
          <Button size="small" icon={<ThunderboltOutlined />} loading={collecting === r.id} onClick={() => handleCollect(r.id)}>采集</Button>
          <Button size="small" onClick={() => openInterfaces(r)}>接口</Button>
          <Button size="small" danger icon={<DeleteOutlined />} onClick={() => handleDelete(r.id)}>删除</Button>
        </Space>
      ) },
  ]

  const ifColumns = [
    { title: '接口', dataIndex: 'if_name', key: 'if_name', width: 130 },
    { title: '索引', dataIndex: 'if_index', key: 'if_index', width: 80 },
    { title: '状态', dataIndex: 'if_oper_status', key: 'if_oper_status', width: 100,
      render: (v: string) => v === 'up' ? <Tag color="green">UP</Tag> : <Tag color="red">{v || 'down'}</Tag> },
    { title: '入字节', dataIndex: 'if_in_octets', key: 'if_in_octets', render: (v: number) => (Number(v || 0) / 1024 / 1024).toFixed(1) + ' MB' },
    { title: '出字节', dataIndex: 'if_out_octets', key: 'if_out_octets', render: (v: number) => (Number(v || 0) / 1024 / 1024).toFixed(1) + ' MB' },
    { title: '入错误', dataIndex: 'if_in_errors', key: 'if_in_errors', width: 100 },
  ]

  return (
    <Card title="SNMP 网络设备" extra={<Space>
      <Button icon={<ReloadOutlined />} onClick={fetch}>刷新</Button>
      <Button type="primary" icon={<PlusOutlined />} onClick={() => setModalOpen(true)}>添加设备</Button>
    </Space>}>
      <Table rowKey="id" columns={columns} dataSource={items} loading={loading}
        pagination={{ pageSize: 20, showTotal: (t: number) => `共 ${t} 条` }} />

      <Modal title="添加 SNMP 设备" open={modalOpen} onOk={handleAdd} onCancel={() => setModalOpen(false)} destroyOnClose>
        <Form form={form} layout="vertical" initialValues={{ snmp_version: 'v2c', community: 'public', status: 'active' }}>
          <Form.Item name="hostname" label="主机名"><Input placeholder="如 sw-core-01" /></Form.Item>
          <Form.Item name="ip" label="IP" rules={[{ required: true, message: '请输入 IP' }]}><Input placeholder="管理网可达的设备 IP" /></Form.Item>
          <Space size="large" style={{ display: 'flex' }}>
            <Form.Item name="community" label="Community" style={{ width: 180 }}><Input /></Form.Item>
            <Form.Item name="snmp_version" label="SNMP 版本" style={{ width: 180 }}>
              <Select options={[{ value: 'v2c', label: 'v2c' }, { value: 'v3', label: 'v3' }]} />
            </Form.Item>
          </Space>
          <Space size="large" style={{ display: 'flex' }}>
            <Form.Item name="vendor" label="厂商" style={{ width: 180 }}><Input /></Form.Item>
            <Form.Item name="model" label="型号" style={{ width: 180 }}><Input /></Form.Item>
          </Space>
        </Form>
      </Modal>

      <Drawer title={`${ifDevice} — 接口`} open={ifDrawer} onClose={() => setIfDrawer(false)} width={700}>
        <Table rowKey="id" columns={ifColumns} dataSource={interfaces} pagination={{ pageSize: 20 }} />
      </Drawer>
    </Card>
  )
}

export default Snmp
