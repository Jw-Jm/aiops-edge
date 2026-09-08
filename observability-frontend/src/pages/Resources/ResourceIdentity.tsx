import React from 'react'
import { Card, Descriptions, Tag } from 'antd'
import type { ResourceRef } from '../../features/scope/types'

export const ResourceIdentity: React.FC<{ resource?: ResourceRef; clusterName?: string }> = ({ resource, clusterName }) => <Card size="small" title="资源身份" className="resource-identity"><Descriptions size="small" column={{ xs: 1, sm: 2, lg: 4 }} items={[
  { key: 'type', label: '类型', children: resource?.type || '未知' },
  { key: 'name', label: '名称', children: resource?.label || resource?.id || '未选择资源' },
  { key: 'health', label: '健康', children: <Tag color="default">未知</Tag> },
  { key: 'owner', label: 'Owner', children: '未知' },
  { key: 'app', label: '所属应用', children: '未知' },
  { key: 'namespace', label: 'Namespace', children: '未知' },
  { key: 'workload', label: 'Deployment / Node', children: '未知' },
  { key: 'cluster', label: '集群', children: clusterName || '未选择' },
  { key: 'change', label: '最近变更', children: '未提供' },
  { key: 'alert', label: '当前告警', children: '未提供' },
]} /></Card>

export default ResourceIdentity
