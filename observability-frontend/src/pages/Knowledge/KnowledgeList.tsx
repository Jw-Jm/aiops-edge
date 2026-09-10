import React from 'react'
import { Empty, List, Tag } from 'antd'
import type { KnowledgeItem } from '../../api/knowledge'

const typeLabel: Record<string, string> = { incident: '故障案例', document: '运维文档', playbook: '内置 Playbook' }

export default function KnowledgeList({ items, selectedId, loading, onSelect }: { items: KnowledgeItem[]; selectedId?: string; loading?: boolean; onSelect: (item: KnowledgeItem) => void }) {
  return <div className="knowledge-list" data-testid="knowledge-list">
    <div className="knowledge-list__heading"><span>知识条目</span><small>{items.length} 条</small></div>
    <List
      loading={loading}
      dataSource={items}
      locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无知识条目" /> }}
      renderItem={(item) => <List.Item className={`knowledge-list__item${item.knowledge_id === selectedId ? ' is-selected' : ''}`} onClick={() => onSelect(item)}>
        <div className="knowledge-list__item-main"><strong>{item.title}</strong><span>{item.summary || '暂无摘要'}</span><small>{typeLabel[item.knowledge_type] || item.knowledge_type} · {item.status}</small></div>
        {item.scope_type === 'platform_common' && <Tag color="blue">平台通用</Tag>}
      </List.Item>}
    />
  </div>
}

export { typeLabel }
