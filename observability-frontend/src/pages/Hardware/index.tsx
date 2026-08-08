import React, { useState, useEffect, useCallback } from 'react'
import { Card, Table, Tag, Space, Button, Row, Col, Input, message } from 'antd'
import { ReloadOutlined, HddOutlined } from '@ant-design/icons'
import { listNodeHealth, listIpmiSensors, NodeHealthRow, IpmiSensor } from '../../api/client'

const COMP_MAP: Record<string, string> = {
  cpu: 'CPU', memory: '内存', disk: '磁盘', network: '网卡',
}
const COMP_COLOR: Record<string, string> = { cpu: 'blue', memory: 'purple', disk: 'green', network: 'cyan' }

const STATUS_MAP: Record<string, { color: string; label: string }> = {
  healthy: { color: 'green', label: '正常' },
  degraded: { color: 'orange', label: '降级' },
  fault: { color: 'red', label: '故障' },
}

const TYPE_MAP: Record<string, { color: string; label: string }> = {
  Temperature: { color: 'red', label: '温度' },
  Fan: { color: 'blue', label: '风扇' },
  Voltage: { color: 'orange', label: '电压' },
  Power: { color: 'green', label: '电源' },
}

const Hardware: React.FC = () => {
  const [health, setHealth] = useState<NodeHealthRow[]>([])
  const [sensors, setSensors] = useState<IpmiSensor[]>([])
  const [loading, setLoading] = useState(false)
  const [node, setNode] = useState('')

  const fetch = useCallback(async () => {
    setLoading(true)
    try {
      const params: Record<string, string> = {}
      if (node) params.node = node
      const [h, s] = await Promise.all([
        listNodeHealth(params),
        listIpmiSensors(params),
      ])
      setHealth(h.data?.health || [])
      setSensors(s.data?.sensors || [])
    } catch { /* ignore */ } finally { setLoading(false) }
  }, [node])

  useEffect(() => { fetch() }, [fetch])

  // 按节点分组部件可用性
  const byNode: Record<string, Record<string, string>> = {}
  health.forEach(r => {
    if (!byNode[r.node_name]) byNode[r.node_name] = {}
    byNode[r.node_name][r.component] = r.status
  })

  const compCols = (nodeName: string) => {
    const cols = (['cpu', 'memory', 'disk', 'network'] as const).map(c => ({
      title: COMP_MAP[c], key: c, dataIndex: c,
      render: (v: string) => {
        const st = v || 'unknown'
        return STATUS_MAP[st] ? <Tag color={STATUS_MAP[st].color}>{STATUS_MAP[st].label}</Tag> : <Tag>{st}</Tag>
      },
    }))
    return [
      { title: '节点', dataIndex: 'node', key: 'node', fixed: 'left' as const, width: 150 },
      ...cols,
    ]
  }

  const nodeRows = Object.keys(byNode).map(n => ({ node: n, ...byNode[n] }))

  const sensorCols = [
    { title: '节点', dataIndex: 'node_name', key: 'node_name', width: 130 },
    { title: '类型', dataIndex: 'sensor_type', key: 'sensor_type', width: 110,
      render: (v: string) => TYPE_MAP[v] ? <Tag color={TYPE_MAP[v].color}>{TYPE_MAP[v].label}</Tag> : v },
    { title: '传感器', dataIndex: 'sensor_name', key: 'sensor_name', width: 150 },
    { title: '读数', dataIndex: 'reading', key: 'reading', width: 130 },
    { title: '状态', dataIndex: 'status', key: 'status', width: 100,
      render: (v: string) => v === 'ok' ? <Tag color="green">OK</Tag> : <Tag color="red">{v || 'unknown'}</Tag> },
  ]

  return (
    <div>
      <Card title="服务器部件可用性" extra={<Space>
        <Input placeholder="节点名过滤" allowClear style={{ width: 180 }} onChange={e => setNode(e.target.value)} />
        <Button icon={<ReloadOutlined />} onClick={fetch}>刷新</Button>
      </Space>} style={{ marginBottom: 16 }}>
        <Table rowKey="node" columns={compCols('')} dataSource={nodeRows} loading={loading}
          pagination={false} scroll={{ x: 'max-content' }} />
      </Card>

      <Card title="IPMI 硬件健康（本地 /dev/ipmi0 采集）">
        <Table rowKey={(r, i) => `${r.node_name}-${i}`} columns={sensorCols} dataSource={sensors} loading={loading}
          pagination={{ pageSize: 20, showTotal: (t: number) => `共 ${t} 条` }} />
      </Card>
    </div>
  )
}

export default Hardware
