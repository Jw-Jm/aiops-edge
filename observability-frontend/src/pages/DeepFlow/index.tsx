import React, { useEffect, useState } from 'react'
import { Card, Spin, Tag, Typography, Space, Button, Descriptions, Row, Col } from 'antd'
import { CheckCircleOutlined, CloseCircleOutlined, ReloadOutlined, LinkOutlined, ExportOutlined, ShareAltOutlined, ApiOutlined, BarChartOutlined, FireOutlined, ClusterOutlined, DatabaseOutlined } from '@ant-design/icons'
import api from '../../api/client'

const { Title, Text } = Typography

// DeepFlow Grafana 地址：
// 1. 优先读取设置页保存的 grafanaUrl (localStorage)
// 2. 否则用当前平台主机名 + 默认端口 32060 (deepflow-grafana NodePort)
//    这样浏览器访问 <主机>:30253 时，DeepFlow Grafana 自动指向 <主机>:32060，避免硬编码 localhost
const getDFGrafanaUrl = () => {
  const saved = localStorage.getItem('grafanaUrl')
  if (saved) return saved
  const host = window.location.hostname
  return `http://${host}:32060`
}

const DeepFlow: React.FC = () => {
  const [loading, setLoading] = useState(true)
  const [available, setAvailable] = useState(false)
  const [message, setMessage] = useState('')

  const fetchStatus = async () => {
    setLoading(true)
    try {
      const res = await api.get('/deepflow/status')
      const d = res.data?.data || res.data
      setAvailable(d?.status === 'available' || d?.status === 'ok')
      setMessage(d?.message || '')
    } catch {
      setAvailable(false)
      setMessage('无法连接到 DeepFlow 服务')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { fetchStatus() }, [])

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16 }}>
        <Title level={3} style={{ margin: 0 }}><LinkOutlined /> DeepFlow 可观测性</Title>
        <Button icon={<ReloadOutlined />} onClick={fetchStatus} loading={loading}>刷新状态</Button>
      </div>

      <Spin spinning={loading}>
        <Card style={{ marginBottom: 16 }}>
          <Descriptions bordered size="small" column={2}>
            <Descriptions.Item label="连接状态">
              {available ? (
                <Tag icon={<CheckCircleOutlined />} color="success">已连接</Tag>
              ) : (
                <Tag icon={<CloseCircleOutlined />} color="error">不可用</Tag>
              )}
            </Descriptions.Item>
            <Descriptions.Item label="部署方式">
              <Tag color="blue">Kubernetes (deepflow namespace)</Tag>
            </Descriptions.Item>
            <Descriptions.Item label="采集技术">
              <Text>eBPF 零侵扰 · Wasm 插件</Text>
            </Descriptions.Item>
            <Descriptions.Item label="Grafana">
              <a href={getDFGrafanaUrl()} target="_blank" rel="noopener noreferrer">
                {getDFGrafanaUrl()} <ExportOutlined />
              </a>
            </Descriptions.Item>
          </Descriptions>
        </Card>

        {available && (
          <Card title="DeepFlow 能力概览">
            <Row gutter={[12, 12]}>
              {[
                { title: '分布式追踪', desc: 'eBPF 自动 Trace，零代码', icon: <ShareAltOutlined />, color: '#1677ff' },
                { title: '网络性能', desc: 'TCP 重传/建连时延/流日志', icon: <ApiOutlined />, color: '#13c2c2' },
                { title: '应用指标', desc: 'RED 指标自动采集', icon: <BarChartOutlined />, color: '#52c41a' },
                { title: '持续 Profiling', desc: 'CPU/Memory 火焰图', icon: <FireOutlined />, color: '#fa8c16' },
                { title: 'K8s 可观测', desc: 'Pod/Service/Node 拓扑', icon: <ClusterOutlined />, color: '#722ed1' },
                { title: '数据库监控', desc: 'MySQL/Redis/Kafka 协议解析', icon: <DatabaseOutlined />, color: '#eb2f96' },
              ].map(item => (
                <Col xs={24} sm={12} md={8} key={item.title}>
                  <Card size="small" hoverable styles={{ body: { padding: '16px' } }}>
                    <div style={{ width: 40, height: 40, borderRadius: 10, background: item.color + '1a', color: item.color, display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 20, marginBottom: 10 }}>{item.icon}</div>
                    <div style={{ fontWeight: 600, marginBottom: 4 }}>{item.title}</div>
                    <Text type="secondary" style={{ fontSize: 12 }}>{item.desc}</Text>
                  </Card>
                </Col>
              ))}
            </Row>
            <Button type="primary" icon={<ExportOutlined />} onClick={() => window.open(getDFGrafanaUrl(), '_blank')} block style={{ marginTop: 16, height: 40 }}>
              打开 DeepFlow Grafana 查看详细 Dashboard
            </Button>
          </Card>
        )}

        {!available && !message && (
          <Card>
            <Text type="secondary">DeepFlow 部署验证中...</Text>
          </Card>
        )}
      </Spin>
    </div>
  )
}

export default DeepFlow
