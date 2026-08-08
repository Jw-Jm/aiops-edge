import React, { useState, useEffect, useCallback } from 'react'
import { Card, Table, Tag, Space, Button, Modal, Form, Input, message, Drawer } from 'antd'
import { ReloadOutlined, DeleteOutlined, CloudSyncOutlined, ClusterOutlined, DatabaseOutlined } from '@ant-design/icons'
import { listClusters, syncClusters, updateCluster, deleteCluster, listClusterNodes, ClusterItem, ClusterNodeItem } from '../../api/client'

const STATUS_MAP: Record<string, { color: string; label: string }> = {
  active: { color: 'green', label: '运行中' },
  degraded: { color: 'orange', label: '降级' },
  down: { color: 'red', label: '下线' },
}

const Clusters: React.FC = () => {
  const [items, setItems] = useState<ClusterItem[]>([])
  const [loading, setLoading] = useState(false)
  const [syncing, setSyncing] = useState(false)
  const [editModal, setEditModal] = useState(false)
  const [editing, setEditing] = useState<ClusterItem | null>(null)
  const [nodeDrawer, setNodeDrawer] = useState(false)
  const [nodes, setNodes] = useState<ClusterNodeItem[]>([])
  const [form] = Form.useForm()

  const fetch = useCallback(async () => {
    setLoading(true)
    try {
      const r = await listClusters()
      setItems(r.data?.clusters || [])
    } catch { /* ignore */ } finally { setLoading(false) }
  }, [])

  useEffect(() => { fetch() }, [fetch])

  const handleSync = async () => {
    setSyncing(true)
    try {
      const r = await syncClusters()
      message.success(r.data?.synced ? '已从 K8s 同步集群' : 'kubectl 不可用或未发现集群')
      fetch()
    } catch (e: any) { message.error(e?.response?.data?.error || '同步失败') } finally { setSyncing(false) }
  }

  const openNodes = async (c: ClusterItem) => {
    try {
      const r = await listClusterNodes(c.id)
      setNodes(r.data?.nodes || [])
      setNodeDrawer(true)
    } catch { message.error('获取节点失败') }
  }

  const openEdit = (c: ClusterItem) => { setEditing(c); form.setFieldsValue(c); setEditModal(true) }
  const handleSave = async () => {
    const v = await form.validateFields()
    try { await updateCluster(editing!.id, v); message.success('已更新'); setEditModal(false); fetch() }
    catch (e: any) { message.error(e?.response?.data?.error || '保存失败') }
  }
  const handleDelete = async (id: number) => {
    try { await deleteCluster(id); message.success('已删除'); fetch() }
    catch { message.error('删除失败') }
  }

  const columns = [
    { title: '集群名', dataIndex: 'name', key: 'name', width: 160,
      render: (v: string) => <><ClusterOutlined /> <b>{v}</b></> },
    { title: 'Provider', dataIndex: 'provider', key: 'provider', width: 110 },
    { title: '版本', dataIndex: 'version', key: 'version', width: 130 },
    { title: '节点数', dataIndex: 'node_count', key: 'node_count', width: 90 },
    { title: '状态', dataIndex: 'status', key: 'status', width: 100,
      render: (v: string) => STATUS_MAP[v] ? <Tag color={STATUS_MAP[v].color}>{STATUS_MAP[v].label}</Tag> : (v || '-') },
    { title: 'API Server', dataIndex: 'api_server', key: 'api_server', ellipsis: true },
    { title: '操作', key: 'action', width: 220, fixed: 'right' as const,
      render: (_: unknown, r: ClusterItem) => (
        <Space>
          <Button size="small" icon={<DatabaseOutlined />} onClick={() => openNodes(r)}>节点</Button>
          <Button size="small" onClick={() => openEdit(r)}>编辑</Button>
          <Button size="small" danger icon={<DeleteOutlined />} onClick={() => handleDelete(r.id)}>删除</Button>
        </Space>
      ) },
  ]

  const nodeColumns = [
    { title: '节点名', dataIndex: 'name', key: 'name', width: 160 },
    { title: '角色', dataIndex: 'role', key: 'role', width: 110,
      render: (v: string) => v === 'control-plane' ? <Tag color="gold">控制面</Tag> : <Tag color="blue">工作节点</Tag> },
    { title: '状态', dataIndex: 'status', key: 'status', width: 100,
      render: (v: string) => v === 'Ready' ? <Tag color="green">Ready</Tag> : <Tag color="red">{v}</Tag> },
    { title: 'IP', dataIndex: 'ip', key: 'ip', width: 130 },
    { title: 'OS', dataIndex: 'os', key: 'os', width: 170 },
    { title: 'CPU', dataIndex: 'cpu', key: 'cpu', width: 80 },
    { title: '内存', dataIndex: 'memory', key: 'memory', width: 100 },
  ]

  return (
    <Card title="集群管理" extra={<Space>
      <Button icon={<ReloadOutlined />} onClick={fetch}>刷新</Button>
      <Button type="primary" icon={<CloudSyncOutlined />} loading={syncing} onClick={handleSync}>从 K8s 同步</Button>
    </Space>}>
      <Table rowKey="id" columns={columns} dataSource={items} loading={loading}
        pagination={{ pageSize: 20, showTotal: (t: number) => `共 ${t} 条` }} />

      <Modal title="编辑集群" open={editModal} onOk={handleSave} onCancel={() => setEditModal(false)} destroyOnClose>
        <Form form={form} layout="vertical">
          <Form.Item name="provider" label="Provider"><Input /></Form.Item>
          <Form.Item name="version" label="版本"><Input /></Form.Item>
          <Form.Item name="node_count" label="节点数"><Input /></Form.Item>
          <Form.Item name="api_server" label="API Server"><Input /></Form.Item>
        </Form>
      </Modal>

      <Drawer title="集群节点" open={nodeDrawer} onClose={() => setNodeDrawer(false)} width={800}>
        <Table rowKey="name" columns={nodeColumns} dataSource={nodes}
          pagination={{ pageSize: 20 }} />
      </Drawer>
    </Card>
  )
}

export default Clusters
