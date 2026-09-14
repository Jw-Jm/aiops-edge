import React from 'react'
import { Button, Input, Select, Space, Typography } from 'antd'
import type { KnowledgeInput } from '../../api/knowledge'

export default function KnowledgeEditor({ value, readOnly, allowPlatformCommon, currentStatus, saving, submitting, onChange, onSave, onSubmit }: { value: KnowledgeInput; readOnly?: boolean; allowPlatformCommon?: boolean; currentStatus?: string; saving?: boolean; submitting?: boolean; onChange: (next: KnowledgeInput) => void; onSave: () => void; onSubmit?: () => void }) {
  return <div className="knowledge-editor" data-testid="knowledge-editor">
    <div className="section-heading"><div><Typography.Title level={5} style={{ margin: 0 }}>人工新增</Typography.Title><Typography.Text type="secondary">保存草稿与提交审核分开</Typography.Text></div></div>
    <Space direction="vertical" size={12} style={{ width: '100%' }}>
      <Input aria-label="知识标题" placeholder="知识标题" value={value.title} disabled={readOnly} onChange={(event) => onChange({ ...value, title: event.target.value })} />
      <Select aria-label="知识范围" value={value.scope_type} disabled={readOnly} options={[{ value: 'cluster', label: '当前集群' }, ...(allowPlatformCommon ? [{ value: 'platform_common', label: '平台通用（管理员）' }] : [])]} onChange={(scope_type) => onChange({ ...value, scope_type: scope_type as KnowledgeInput['scope_type'], cluster_id: scope_type === 'cluster' ? value.cluster_id : undefined })} />
      <Select aria-label="知识类型" value={value.knowledge_type} disabled={readOnly} options={[{ value: 'incident', label: '故障案例' }, { value: 'document', label: '运维文档' }, { value: 'playbook', label: '内置 Playbook' }]} onChange={(knowledge_type) => onChange({ ...value, knowledge_type })} />
      <Input.TextArea aria-label="知识摘要" placeholder="摘要（可选）" autoSize={{ minRows: 2, maxRows: 4 }} value={value.summary} disabled={readOnly} onChange={(event) => onChange({ ...value, summary: event.target.value })} />
      <Input.TextArea aria-label="知识正文" placeholder="输入可复核的运维正文" autoSize={{ minRows: 8, maxRows: 16 }} value={value.content} disabled={readOnly} onChange={(event) => onChange({ ...value, content: event.target.value })} />
      {!readOnly && <Space><Button type="primary" loading={saving} onClick={onSave}>保存草稿</Button>{currentStatus === 'draft' && onSubmit && <Button loading={submitting} onClick={onSubmit}>提交审核</Button>}</Space>}
    </Space>
  </div>
}
