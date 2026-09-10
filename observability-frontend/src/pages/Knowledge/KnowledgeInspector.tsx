import React from 'react'
import { Descriptions, Empty, Tag, Typography } from 'antd'
import type { KnowledgeItem, KnowledgeVersion } from '../../api/knowledge'

export default function KnowledgeInspector({ item, version, indexAvailable }: { item?: KnowledgeItem; version?: KnowledgeVersion; indexAvailable: boolean }) {
  if (!item) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="选择一条知识查看正文" />
  return <div className="knowledge-inspector" data-testid="knowledge-inspector">
    <div className="knowledge-inspector__title"><div><Typography.Title level={4}>{item.title}</Typography.Title><Typography.Text type="secondary">{item.summary || '暂无摘要'}</Typography.Text></div><Tag>{item.status}</Tag></div>
    <Descriptions size="small" column={2} style={{ marginTop: 18 }} items={[
      { key: 'type', label: '类型', children: item.knowledge_type },
      { key: 'scope', label: '范围', children: item.scope_type === 'platform_common' ? '平台通用' : item.cluster_id || '当前集群' },
      { key: 'version', label: '当前版本', children: item.current_version_id || version?.version_id || '未提供' },
      { key: 'source', label: '来源', children: version?.source_kind || item.source_kind || '人工新增' },
    ]} />
    <section className="knowledge-inspector__content"><div className="section-heading"><strong>正文</strong><small>{version?.source_revision || item.source_revision || '未关联 revision'}</small></div><Typography.Paragraph>{version?.content || '当前版本暂无正文。'}</Typography.Paragraph></section>
  </div>
}
