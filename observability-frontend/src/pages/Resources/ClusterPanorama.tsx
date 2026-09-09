import React, { useEffect, useMemo, useState } from 'react'
import { Card, Col, Row, Statistic, Tag, Typography } from 'antd'
import { getResourceSummary, type ResourceDomainSummary, type ResourceReadMetaView } from '../../api/resources'
import DataState from '../../components/display/DataState'
import type { ResourceDomain } from '../../features/resources/types'

const DOMAIN_LABELS: Record<ResourceDomain, string> = { compute: '计算', network: '网络', storage: '存储', kubernetes: 'Kubernetes', application: '应用服务' }
const DOMAINS: ResourceDomain[] = ['compute', 'network', 'storage', 'kubernetes', 'application']

export interface ClusterPanoramaProps {
  clusterId: string
  clusterName?: string
}

function healthLabel(health: Record<string, number>): string {
  if (health.critical) return `严重 ${health.critical}`
  if (health.degraded) return `降级 ${health.degraded}`
  if (health.risk) return `风险 ${health.risk}`
  return '状态正常'
}

export function ClusterPanorama({ clusterId, clusterName }: ClusterPanoramaProps) {
  const [domains, setDomains] = useState<ResourceDomainSummary[]>([])
  const [meta, setMeta] = useState<ResourceReadMetaView | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(false)

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    void getResourceSummary(controller.signal)
      .then((response) => { setDomains(response.domains); setMeta(response.meta); setError(false) })
      .catch(() => { if (!controller.signal.aborted) setError(true) })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [clusterId])

  const summaries = useMemo(() => DOMAINS.map((domain) => domains.find((item) => item.domain === domain) ?? { domain, count: 0, health: {}, incomplete: false }), [domains])
  if (loading) return <DataState kind="loading" />
  if (error) return <DataState kind="error" title="集群全景读取失败" onRetry={() => { setLoading(true); setError(false) }} />
  return (
    <div className="cluster-panorama" data-testid="cluster-panorama">
      <Row gutter={[12, 12]}>
        <Col xs={24} md={8}><Card size="small" title="集群身份"><Typography.Title level={4} style={{ margin: 0 }}>{clusterName || clusterId || '未选择集群'}</Typography.Title><Typography.Text type="secondary">{clusterId || '集群范围'} · 生产平台</Typography.Text></Card></Col>
        <Col xs={24} md={8}><Card size="small" title="采集完整度"><Statistic value={meta?.partial ? '部分' : '完整'} suffix={meta?.stale ? ' · 可能过期' : ''} valueStyle={{ color: meta?.partial || meta?.stale ? 'var(--warning)' : 'var(--success)', fontSize: 24 }} /></Card></Col>
        <Col xs={24} md={8}><Card size="small" title="当前范围"><Typography.Text>集群范围</Typography.Text><br /><Typography.Text type="secondary">物理机、虚拟机、网络、存储均归属于本集群</Typography.Text></Card></Col>
      </Row>
      <div className="section-heading" style={{ marginTop: 18 }}><div><Typography.Title level={4} style={{ margin: 0 }}>五域健康</Typography.Title><Typography.Text type="secondary">按平台资源域查看数量与健康分布</Typography.Text></div></div>
      <Row gutter={[12, 12]}>
        {summaries.map((summary) => <Col key={summary.domain} xs={24} sm={12} lg={8} xl={Math.floor(24 / 5)}><Card size="small" className="resource-domain-card" title={DOMAIN_LABELS[summary.domain as ResourceDomain]}><Statistic value={summary.count} suffix="项" /><Tag color={summary.health.critical ? 'red' : summary.health.degraded || summary.health.risk ? 'orange' : 'green'}>{healthLabel(summary.health)}</Tag>{summary.incomplete && <Typography.Paragraph type="warning" style={{ margin: '8px 0 0', fontSize: 11 }}>数据不完整</Typography.Paragraph>}</Card></Col>)}
      </Row>
      <Row gutter={[12, 12]} style={{ marginTop: 18 }}>
        <Col xs={24} md={8}><Card size="small" title="异常队列"><Typography.Text type="secondary">当前范围暂无已接入的异常队列投影</Typography.Text><div style={{ marginTop: 8 }}><Tag>集群范围</Tag></div></Card></Col>
        <Col xs={24} md={8}><Card size="small" title="容量风险"><Typography.Text type="secondary">容量风险将在资源事实同步后显示</Typography.Text></Card></Col>
        <Col xs={24} md={8}><Card size="small" title="近期变更"><Typography.Text type="secondary">当前范围暂无近期变更投影</Typography.Text></Card></Col>
      </Row>
    </div>
  )
}

export default ClusterPanorama
