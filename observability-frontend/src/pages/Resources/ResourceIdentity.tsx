import React from 'react'
import { Button, Card, Descriptions, Tag } from 'antd'
import { Link } from 'react-router-dom'
import type { ResourceDetailResponse } from '../../api/resources'
import type { PlatformResourceRef } from '../../features/resources/types'
import { resourceLocation, resourceTypeLabel } from '../../features/resources/resourceDomain'
import { projectResourceDetail } from './resourceDetailModel'

export const ResourceIdentity: React.FC<{ resource?: PlatformResourceRef; clusterName?: string; detail?: ResourceDetailResponse }> = ({ resource, clusterName, detail }) => {
  if (!resource) return <Card size="small" title="资源身份" className="resource-identity"><Descriptions size="small" items={[{ key: 'scope', label: '范围', children: '集群范围' }]} /></Card>
  const sections = detail ? projectResourceDetail(detail) : []
  const identity = sections.find((section) => section.key === 'identity')
  const health = sections.find((section) => section.key === 'health')
  const typeSpecific = sections.find((section) => section.key === 'type-specific')
  const capabilities = detail?.data.capabilities ?? []
  const fieldItems = [...(identity?.fields ?? []), ...(health?.fields ?? []), ...(typeSpecific?.fields ?? [])].map((item) => ({ key: item.key, label: item.label, children: item.key === 'health' ? <Tag color={item.value === 'critical' ? 'red' : item.value === 'healthy' ? 'green' : 'orange'}>{item.value}</Tag> : item.value }))
  return <Card size="small" title="资源身份与详情" className="resource-identity" extra={capabilities.some((capability) => /execute|action|mutat/i.test(capability)) ? <Button size="small" type="primary"><Link to={`/actions?resource=${encodeURIComponent(resource.uid)}`}>进入处置</Link></Button> : null}><Descriptions size="small" column={{ xs: 1, sm: 2, lg: 3 }} items={fieldItems.length ? fieldItems : [
    { key: 'type', label: '类型', children: resourceTypeLabel(resource.type) },
    { key: 'name', label: '名称', children: resourceLocation(resource) },
    { key: 'cluster', label: '集群', children: clusterName || resource.clusterId },
    { key: 'health', label: '健康', children: <Tag>状态未知</Tag> },
  ]} /></Card>
}

export default ResourceIdentity
