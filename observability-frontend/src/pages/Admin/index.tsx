import { useEffect, useState } from 'react'
import {
  Tabs, Table, Button, Form, Input, Select, Modal, message, Space, Tag, Typography,
} from 'antd'
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons'
import {
  listUsers, updateUserScope, UserItem,
  listAuditLogs,
  listClusters, createCluster, updateCluster, deleteCluster,
  listClusterNodes, getClusterNamespaces, getClusterEvents,
  ClusterItem,
} from '../../api/client'

// ─── 用户 Tab ─────────────────────────────────────────────
function UsersTab() {
  const [users, setUsers] = useState<UserItem[]>([])
  const [loading, setLoading] = useState(false)
  const [editScope, setEditScope] = useState<UserItem | null>(null)
  const [scopeForm] = Form.useForm()

  const fetchUsers = async () => {
    setLoading(true)
    try {
      const r = await listUsers()
      setUsers((r.data?.users) || [])
    } finally { setLoading(false) }
  }
  useEffect(() => { fetchUsers() }, [])

  const openScope = (u: UserItem) => {
    setEditScope(u)
    let parsed: Record<string, string[]> = {}
    try { parsed = u.scope ? JSON.parse(u.scope) : {} } catch { parsed = {} }
    scopeForm.setFieldsValue({
      services: (parsed.services || []).join(','),
      clusters: (parsed.clusters || []).join(','),
      devices: (parsed.devices || []).join(','),
    })
  }

  const saveScope = async () => {
    if (!editScope) return
    const v = scopeForm.getFieldsValue()
    const scope = {
      services: (v.services || '').split(',').map((s: string) => s.trim()).filter(Boolean),
      clusters: (v.clusters || '').split(',').map((s: string) => s.trim()).filter(Boolean),
      devices: (v.devices || '').split(',').map((s: string) => s.trim()).filter(Boolean),
    }
    try {
      await updateUserScope(editScope.id, { scope: JSON.stringify(scope) })
      message.success('scope 已更新')
      setEditScope(null)
      fetchUsers()
    } catch { message.error('更新失败') }
  }

  return (
    <div>
      <Space style={{ marginBottom: 12 }}>
        <Typography.Text strong>用户与数据范围（scope）</Typography.Text>
        <Button size="small" icon={<ReloadOutlined />} onClick={fetchUsers}>刷新</Button>
      </Space>
      <Table
        rowKey="id" dataSource={users} loading={loading} size="small" pagination={{ pageSize: 10 }}
        columns={[
          { title: '用户名', dataIndex: 'username' },
          { title: '显示名', dataIndex: 'display_name' },
          { title: '角色', dataIndex: 'role', render: (r) => <Tag color={r === 'admin' ? 'red' : 'blue'}>{r}</Tag> },
          {
            title: '数据范围 scope',
            dataIndex: 'scope',
            render: (s) => {
              try {
                const p = s ? JSON.parse(s) : {}
                const n = (p.services?.length || 0) + (p.clusters?.length || 0) + (p.devices?.length || 0)
                return n > 0 ? <Tag>{`${n} 项`}</Tag> : <Tag color={r_role_admin(s) ? 'red' : 'default'}>全量</Tag>
              } catch { return <Tag>全量</Tag> }
            },
          },
          {
            title: '操作', width: 120,
            render: (_, r) => (
              <Button size="small" type="primary" onClick={() => openScope(r)} disabled={r.role === 'admin'}>配置 scope</Button>
            ),
          },
        ]}
      />
      <Modal title={`配置 ${editScope?.username} 的数据范围`} open={!!editScope} onOk={saveScope}
        onCancel={() => setEditScope(null)}>
        <Form form={scopeForm} layout="vertical">
          <Form.Item label="服务（逗号分隔）" name="services">
            <Input placeholder="如: frontend, query-api（空=不限制该维度）" />
          </Form.Item>
          <Form.Item label="集群（逗号分隔）" name="clusters">
            <Input placeholder="如: prod-a, prod-b" />
          </Form.Item>
          <Form.Item label="设备（逗号分隔）" name="devices">
            <Input placeholder="如: node-1, node-2" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}

// scope 是否 admin（admin 全量）的辅助：admin 角色用户不可配 scope
function r_role_admin(_s: string) { return false }

// ─── 审计 Tab ─────────────────────────────────────────────
function AuditTab() {
  const [rows, setRows] = useState<Record<string, unknown>[]>([])
  const [loading, setLoading] = useState(false)
  useEffect(() => {
    setLoading(true)
    listAuditLogs()
      .then((r) => setRows((r.data?.logs) || (r.data?.audit_logs) || []))
      .finally(() => setLoading(false))
  }, [])
  return (
    <Table
      rowKey={(r) => String((r as any).id ?? JSON.stringify(r))}
      dataSource={rows} loading={loading} size="small" pagination={{ pageSize: 10 }}
      columns={[
        { title: '操作者', dataIndex: 'operator' },
        { title: '动作', dataIndex: 'action' },
        { title: '目标', dataIndex: 'target' },
        { title: '详情', dataIndex: 'detail', ellipsis: true },
        { title: '时间', dataIndex: 'created_at', width: 180 },
      ]}
    />
  )
}

// ─── 集群 Tab ─────────────────────────────────────────────
function ClustersTab() {
  const [clusters, setClusters] = useState<ClusterItem[]>([])
  const [loading, setLoading] = useState(false)
  const [modal, setModal] = useState(false)
  const [detail, setDetail] = useState<ClusterItem | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailTab, setDetailTab] = useState('nodes')
  const [detailRows, setDetailRows] = useState<Record<string, unknown>[]>([])
  const [form] = Form.useForm()

  const fetchClusters = async () => {
    setLoading(true)
    try {
      const r = await listClusters()
      setClusters((r.data?.clusters) || (r.data?.data) || [])
    } finally { setLoading(false) }
  }
  useEffect(() => { fetchClusters() }, [])

  const createOrUpdate = async () => {
    const v = form.getFieldsValue()
    try {
      if (v.id) {
        await updateCluster(v.id, { name: v.name, api_server: v.api_server, kubeconfig: v.kubeconfig })
      } else {
        await createCluster({ name: v.name, provider: v.provider || 'onprem', api_server: v.api_server, kubeconfig: v.kubeconfig })
      }
      message.success('已保存')
      setModal(false)
      fetchClusters()
    } catch { message.error('保存失败') }
  }

  const openDetail = async (c: ClusterItem) => {
    setDetail(c)
    setDetailTab('nodes')
    setDetailRows([])
    setDetailLoading(true)
    try {
      const r = await listClusterNodes(c.id)
      setDetailRows((r.data?.nodes) || (r.data?.data) || [])
    } finally { setDetailLoading(false) }
  }

  const loadDetail = async (tab: string) => {
    if (!detail) return
    setDetailTab(tab)
    setDetailLoading(true)
    setDetailRows([])
    try {
      if (tab === 'nodes') {
        const r = await listClusterNodes(detail.id)
        setDetailRows((r.data?.nodes) || (r.data?.data) || [])
      } else if (tab === 'namespaces') {
        const r = await getClusterNamespaces(detail.id)
        setDetailRows((r.data?.namespaces) || (r.data?.data) || [])
      } else if (tab === 'events') {
        const r = await getClusterEvents(detail.id)
        setDetailRows((r.data?.events) || (r.data?.data) || [])
      }
    } finally { setDetailLoading(false) }
  }

  const detailCols = detailTab === 'nodes'
    ? [
        { title: '节点', dataIndex: 'name' },
        { title: '角色', dataIndex: 'role' },
        { title: '状态', dataIndex: 'status' },
        { title: 'IP', dataIndex: 'ip' },
        { title: 'OS', dataIndex: 'os' },
      ]
    : detailTab === 'namespaces'
      ? [{ title: '命名空间', dataIndex: 'name' }]
      : [
          { title: '时间', dataIndex: 'last_timestamp' },
          { title: '类型', dataIndex: 'type' },
          { title: '原因', dataIndex: 'reason' },
          { title: '对象', dataIndex: 'involved_object' },
          { title: '消息', dataIndex: 'message', ellipsis: true },
        ]

  return (
    <div>
      <Space style={{ marginBottom: 12 }}>
        <Typography.Text strong>集群管理（kubeconfig 多集群）</Typography.Text>
        <Button size="small" type="primary" icon={<PlusOutlined />} onClick={() => { form.resetFields(); form.setFieldsValue({ id: undefined }); setModal(true) }}>
          新增集群
        </Button>
        <Button size="small" icon={<ReloadOutlined />} onClick={fetchClusters}>刷新</Button>
      </Space>
      <Table
        rowKey="id" dataSource={clusters} loading={loading} size="small" pagination={{ pageSize: 10 }}
        columns={[
          { title: '名称', dataIndex: 'name' },
          { title: 'Provider', dataIndex: 'provider' },
          { title: 'API Server', dataIndex: 'api_server', ellipsis: true },
          { title: '状态', dataIndex: 'status', render: (s) => <Tag color={s === 'Active' ? 'green' : 'default'}>{s || '-'}</Tag> },
          {
            title: '操作', width: 180,
            render: (_, r) => (
              <Space>
                <Button size="small" onClick={() => openDetail(r)}>详情</Button>
                <Button size="small" onClick={() => { form.setFieldsValue({ id: r.id, name: r.name, provider: r.provider, api_server: r.api_server }); setModal(true) }}>
                  编辑
                </Button>
                <Button size="small" danger onClick={async () => { await deleteCluster(r.id); fetchClusters() }}>删除</Button>
              </Space>
            ),
          },
        ]}
      />
      <Modal title="新增/编辑集群" open={modal} onOk={createOrUpdate} onCancel={() => setModal(false)}>
        <Form form={form} layout="vertical">
          <Form.Item label="集群名称" name="name" rules={[{ required: true, message: '必填' }]}>
            <Input />
          </Form.Item>
          <Form.Item label="Provider" name="provider"><Input placeholder="如 onprem / aws" /></Form.Item>
          <Form.Item label="API Server" name="api_server"><Input placeholder="https://api.cluster.example:6443" /></Form.Item>
          <Form.Item label="Kubeconfig" name="kubeconfig"><Input.TextArea rows={6} placeholder="粘贴该集群的 kubeconfig（含 token）" /></Form.Item>
        </Form>
      </Modal>
      <Modal title={`集群 ${detail?.name}`} width={760} open={!!detail} onCancel={() => setDetail(null)}
        footer={<Button onClick={() => setDetail(null)}>关闭</Button>}>
        <Tabs activeKey={detailTab} onChange={loadDetail}
          items={[
            { key: 'nodes', label: '节点' },
            { key: 'namespaces', label: '命名空间' },
            { key: 'events', label: '事件' },
          ]}
        />
        <Table rowKey={(r, i) => String(i ?? 0)} dataSource={detailRows} loading={detailLoading}
          size="small" pagination={{ pageSize: 10 }} columns={detailCols as any} />
      </Modal>
    </div>
  )
}

export default function Admin() {
  return (
    <div style={{ padding: 24 }}>
      <Typography.Title level={4} style={{ marginTop: 0 }}>管理门户</Typography.Title>
      <Tabs
        defaultActiveKey="users"
        items={[
          { key: 'users', label: '用户管理', children: <UsersTab /> },
          { key: 'audit', label: '审计日志', children: <AuditTab /> },
          { key: 'clusters', label: '集群管理', children: <ClustersTab /> },
        ]}
      />
    </div>
  )
}
