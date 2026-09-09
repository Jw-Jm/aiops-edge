import React from 'react'
import { Card, Descriptions, Tag } from 'antd'
import type { PlatformResourceRef } from '../../features/resources/types'
import { resourceLocation, resourceTypeLabel } from '../../features/resources/resourceDomain'

export const ResourceIdentity: React.FC<{ resource?: PlatformResourceRef; clusterName?: string }> = ({ resource, clusterName }) => <Card size="small" title="资源身份" className="resource-identity"><Descriptions size="small" column={{ xs: 1, sm: 2, lg: 4 }} items={[
  { key: 'type', label: '类型', children: resource ? resourceTypeLabel(resource.type) : '未选择资源' },
  { key: 'name', label: '名称', children: resource ? resourceLocation(resource) : '未选择资源' },
  { key: 'health', label: '健康', children: <Tag color="default">状态未知</Tag> },
  { key: 'owner', label: '归属', children: '未提供' },
  { key: 'app', label: '所属应用', children: '未提供' },
  { key: 'namespace', label: 'Namespace', children: resource?.namespace || '未提供' },
  { key: 'workload', label: '控制器 / 宿主', children: '未提供' },
  { key: 'cluster', label: '集群', children: clusterName || '未选择' },
  { key: 'change', label: '最近变更', children: '未提供' },
  { key: 'alert', label: '当前告警', children: '未提供' },
]} /></Card>

export default ResourceIdentity
