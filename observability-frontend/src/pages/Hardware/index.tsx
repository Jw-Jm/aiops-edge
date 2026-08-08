import React, { useState, useEffect, useCallback } from 'react'
import { Card, Table, Tag, Space, Button, Row, Col, Input, Drawer, Alert, Typography } from 'antd'
import { ReloadOutlined, SettingOutlined } from '@ant-design/icons'
import { listNodeHealth, listIpmiSensors, NodeHealthRow, IpmiSensor } from '../../api/client'

const { Paragraph, Text } = Typography

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
  const [cfgDrawer, setCfgDrawer] = useState(false)

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
        <Button icon={<SettingOutlined />} onClick={() => setCfgDrawer(true)}>配置说明</Button>
        <Button icon={<ReloadOutlined />} onClick={fetch}>刷新</Button>
      </Space>} style={{ marginBottom: 16 }}>
        <Table rowKey="node" columns={compCols('')} dataSource={nodeRows} loading={loading}
          pagination={false} scroll={{ x: 'max-content' }} />
      </Card>

      <Card title="IPMI 硬件健康（本地 /dev/ipmi0 采集）">
        <Table rowKey={(r, i) => `${r.node_name}-${i}`} columns={sensorCols} dataSource={sensors} loading={loading}
          pagination={{ pageSize: 20, showTotal: (t: number) => `共 ${t} 条` }} />
      </Card>

      <Drawer title="IPMI / 部件可用性采集配置说明" open={cfgDrawer} onClose={() => setCfgDrawer(false)} width={560}>
        <Alert type="info" showIcon style={{ marginBottom: 12 }}
          message="四网段隔离：IPMI 用本地 /dev/ipmi0 采集（不走带外网），结果经管理网上报" />
        <Paragraph>
          <Text strong>ipmi-exporter（DaemonSet）</Text>：每 K8s 节点一台，privileged + hostPath <Text code>/dev/ipmi0</Text>，
          用 <Text code>ipmitool sensor list</Text> 读 BMC 温度/风扇/电压/电源。
        </Paragraph>
        <Paragraph>
          <Text strong>采集配置</Text>：
          <pre style={{ background: '#1a1a1a', padding: 12, borderRadius: 8, fontSize: 12, color: '#7ec699' }}>
{`ipmiExporter:
  enabled: true
  image: "ipmi-exporter:latest"
  collectInterval: "120"   # 采集间隔(秒)`}
          </pre>
        </Paragraph>
        <Paragraph>
          <Text strong>服务器侧要求</Text>：
          <ul>
            <li>主板开启 IPMI（BMC）</li>
            <li>内核 <Text code>ipmi_si</Text> 驱动加载，<Text code>/dev/ipmi0</Text> 可用</li>
            <li>无需带外网 IP 可达（本地 KCS 接口）</li>
          </ul>
        </Paragraph>
        <Paragraph>
          <Text strong>部件可用性</Text>：聚合 node_exporter（OS 层 CPU/内存/磁盘/网卡）+ IPMI（温度/电源），
          判定 CPU/内存/磁盘/网卡 的 healthy/degraded/fault 状态。
        </Paragraph>
        <Paragraph>
          <Text strong>排障</Text>：
          <ul>
            <li>节点无传感器 → 该节点 <Text code>/dev/ipmi0</Text> 不可用（非物理机或驱动未加载）</li>
            <li>全部无数据 → 检查 DaemonSet 是否 Running、privileged+hostPath 生效</li>
          </ul>
        </Paragraph>
        <Paragraph type="secondary">完整配置见文档 <Text code>deploy/SNMP_IPMI_DEPLOYMENT.md</Text></Paragraph>
      </Drawer>
    </div>
  )
}

export default Hardware
