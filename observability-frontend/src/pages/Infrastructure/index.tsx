import React, { useEffect, useState } from 'react'
import { Tabs, Table, Tag, Card, Spin, Button, Space, Badge, Select } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import api from '../../api/client'

interface Node { name: string; status: string; cpu: string; memory: string; version: string }
interface Pod { name: string; namespace: string; status: string; restarts: number; startTime: string }
interface Deployment { name: string; namespace: string; replicas: number; ready: number; available: number }
interface Namespace { name: string; status: string }

const Infrastructure: React.FC = () => {
  const [activeTab, setActiveTab] = useState('namespaces')
  const [namespaces, setNamespaces] = useState<Namespace[]>([])
  const [pods, setPods] = useState<Pod[]>([])
  const [deployments, setDeployments] = useState<Deployment[]>([])
  const [nodes, setNodes] = useState<Node[]>([])
  const [loading, setLoading] = useState(true)
  const [connected, setConnected] = useState<boolean | null>(null)
  const [selectedNs, setSelectedNs] = useState('all')

  const fetchAll = async () => {
    setLoading(true)
    try {
      const [nsRes, podRes, depRes, nodeRes] = await Promise.allSettled([
        api.get('/infrastructure/namespaces'),
        api.get('/infrastructure/pods', { params: { namespace: selectedNs } }),
        api.get('/infrastructure/deployments', { params: { namespace: selectedNs } }),
        api.get('/infrastructure/nodes'),
      ])

      const extract = (r: PromiseSettledResult<any>, key: string): any[] => {
        if (r.status !== 'fulfilled') return []
        const d = r.value.data
        // Handle wrapped {data: {...}} or direct response
        const body = d?.data || d
        return Array.isArray(body?.[key]) ? body[key] : []
      }

      setNamespaces(extract(nsRes, 'namespaces'))
      setPods(extract(podRes, 'pods'))
      setDeployments(extract(depRes, 'deployments'))
      setNodes(extract(nodeRes, 'nodes'))
      setConnected(true)
    } catch {
      setConnected(false)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { fetchAll() }, [selectedNs])

  const statusColor = (status: string) => {
    if (/running|ready|active/i.test(status)) return 'green'
    if (/pending|starting/i.test(status)) return 'orange'
    return 'red'
  }

  const nodeColumns = [
    { title: '节点名称', dataIndex: 'name', key: 'name' },
    { title: '状态', dataIndex: 'status', key: 'status', render: (s: string) => <Tag color={statusColor(s)}>{s}</Tag> },
    { title: 'CPU', dataIndex: 'cpu', key: 'cpu' },
    { title: '内存', dataIndex: 'memory', key: 'memory' },
    { title: '版本', dataIndex: 'version', key: 'version', ellipsis: true },
  ]

  const podColumns = [
    { title: '名称', dataIndex: 'name', key: 'name', ellipsis: true },
    { title: '空间', dataIndex: 'namespace', key: 'namespace', render: (s: string) => <Tag>{s}</Tag> },
    { title: '状态', dataIndex: 'status', key: 'status', render: (s: string) => <Tag color={statusColor(s)}>{s}</Tag> },
    { title: '重启', dataIndex: 'restarts', key: 'restarts', render: (r: number) => r > 0 ? <Tag color="red">{r}</Tag> : <Tag color="green">0</Tag> },
  ]

  const deployColumns = [
    { title: '名称', dataIndex: 'name', key: 'name' },
    { title: '空间', dataIndex: 'namespace', key: 'namespace', render: (s: string) => <Tag>{s}</Tag> },
    { title: '副本', dataIndex: 'replicas', key: 'replicas' },
    { title: '就绪', dataIndex: 'ready', key: 'ready', render: (r: number, rec: Deployment) => r === rec.replicas ? <Tag color="green">{r}</Tag> : <Tag color="orange">{r}</Tag> },
  ]

  const nsColumns = [
    { title: '命名空间', dataIndex: 'name', key: 'name' },
    { title: '状态', dataIndex: 'status', key: 'status', render: (s: string) => <Tag color={statusColor(s)}>{s}</Tag> },
  ]

  const nsOptions = [{ label: '全部', value: 'all' }, ...namespaces.map(ns => ({ label: ns.name, value: ns.name }))]

  const items = [
    { key: 'namespaces', label: '命名空间', children: <Table dataSource={namespaces} columns={nsColumns} rowKey="name" size="middle" /> },
    { key: 'pods', label: 'Pods', children: <>
      <Select value={selectedNs} onChange={setSelectedNs} options={nsOptions} style={{ width: 200, marginBottom: 16 }} />
      <Table dataSource={pods} columns={podColumns} rowKey={(r: Pod) => r.name + r.namespace} size="middle" />
    </> },
    { key: 'deployments', label: 'Deployments', children: <>
      <Select value={selectedNs} onChange={setSelectedNs} options={nsOptions} style={{ width: 200, marginBottom: 16 }} />
      <Table dataSource={deployments} columns={deployColumns} rowKey={(r: Deployment) => r.name + r.namespace} size="middle" />
    </> },
    { key: 'nodes', label: '节点', children: <Table dataSource={nodes} columns={nodeColumns} rowKey="name" size="middle" /> },
  ]

  return (
    <Card title={<Space>基础设施监控 <Badge status={connected === true ? 'success' : connected === false ? 'error' : 'processing'} text={connected === true ? '已连接' : connected === false ? '失败' : '检查中'} /></Space>}
      extra={<Button icon={<ReloadOutlined />} onClick={fetchAll} loading={loading}>刷新</Button>}>
      <Spin spinning={loading}><Tabs activeKey={activeTab} onChange={setActiveTab} items={items} /></Spin>
    </Card>
  )
}

export default Infrastructure
