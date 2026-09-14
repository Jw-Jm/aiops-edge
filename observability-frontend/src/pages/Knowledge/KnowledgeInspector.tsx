import React, { useMemo, useState } from 'react'
import { Alert, Button, Descriptions, Empty, Input, Space, Tag, Tooltip, Typography, message } from 'antd'
import type { KnowledgeItem, KnowledgeVersion } from '../../api/knowledge'

export interface KnowledgeSourceHealth {
  source: string
  scope?: string
  documentCount?: number
  chunkCount?: number
  indexVersion?: string
  lastAttemptedAt?: string
  lastSuccessAt?: string
  lastError?: string
  quality?: string
}

export interface KnowledgeInspectorProps {
  item?: KnowledgeItem
  version?: KnowledgeVersion
  indexAvailable: boolean
  /** 审阅与禁用是服务端能力，前端只暴露入口；权限由服务端校验 */
  canReview?: boolean
  onReview?: (decision: 'approve' | 'reject', reason: string) => Promise<void>
  onDisable?: () => Promise<void>
  /** 删除验证：禁用后检索必须不再命中 */
  disableVerification?: { state: 'idle' | 'running' | 'passed' | 'failed'; detail: string }
  onVerifyRemoval?: () => Promise<void>
  sourceHealth?: KnowledgeSourceHealth[]
  /** 当前渲染的段落号，用于段落级引用深链 */
  onCopyCitation?: (paragraphIndex: number) => void
}

function splitParagraphs(content?: string): string[] {
  if (!content) return []
  return content
    .split(/\n\s*\n/)
    .map((p) => p.trim())
    .filter(Boolean)
}

export default function KnowledgeInspector({
  item,
  version,
  indexAvailable,
  canReview = false,
  onReview,
  onDisable,
  disableVerification,
  onVerifyRemoval,
  sourceHealth = [],
  onCopyCitation,
}: KnowledgeInspectorProps) {
  const [rejectReason, setRejectReason] = useState('')
  const [busy, setBusy] = useState(false)

  const paragraphs = useMemo(() => splitParagraphs(version?.content), [version?.content])

  if (!item) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="选择一条知识查看正文与引用定位" />

  const runReview = async (decision: 'approve' | 'reject') => {
    if (!onReview) return
    if (decision === 'reject' && !rejectReason.trim()) {
      message.warning('拒绝必须给出原因')
      return
    }
    setBusy(true)
    try {
      await onReview(decision, rejectReason.trim())
      setRejectReason('')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="knowledge-inspector" data-testid="knowledge-inspector">
      <div className="knowledge-inspector__title">
        <div>
          <Typography.Title level={4}>{item.title}</Typography.Title>
          <Typography.Text type="secondary">{item.summary || '暂无摘要'}</Typography.Text>
        </div>
        <Tag>{item.status}</Tag>
      </div>

      <Descriptions
        size="small"
        column={2}
        style={{ marginTop: 18 }}
        items={[
          { key: 'type', label: '类型', children: item.knowledge_type },
          { key: 'scope', label: '范围', children: item.scope_type === 'platform_common' ? '平台通用' : item.cluster_id || '当前集群' },
          { key: 'version', label: '当前版本', children: item.current_version_id || version?.version_id || '未提供' },
          { key: 'freshness', label: '更新时间', children: item.updated_at || '未提供' },
          { key: 'source', label: '来源', children: version?.source_kind || item.source_kind || '人工新增' },
          { key: 'revision', label: '来源版本', children: version?.source_revision || item.source_revision || '未关联' },
        ]}
      />

      {/* 待审阅：approve/reject 是发布门禁，AI 沉淀不能自动进入生产知识库 */}
      {item.status === 'pending_review' && (
        <section className="knowledge-review" data-testid="knowledge-review">
          <strong>审阅</strong>
          <p className="muted-sm">批准后该版本进入已发布状态；拒绝必须给出原因并留痕。</p>
          <Input.TextArea
            value={rejectReason}
            onChange={(e) => setRejectReason(e.target.value)}
            placeholder="拒绝原因（拒绝时必填）"
            autoSize={{ minRows: 2, maxRows: 4 }}
            aria-label="拒绝原因"
          />
          <Space style={{ marginTop: 8 }}>
            <Button type="primary" loading={busy} disabled={!canReview} onClick={() => runReview('approve')}>批准并发布</Button>
            <Button danger loading={busy} disabled={!canReview || !rejectReason.trim()} onClick={() => runReview('reject')}>拒绝</Button>
            {!canReview && <span className="muted-sm">当前账户没有审阅权限（由服务端校验）</span>}
          </Space>
        </section>
      )}

      <section className="knowledge-inspector__content">
        <div className="section-heading">
          <strong>正文与引用定位</strong>
          <small>{version?.source_revision || item.source_revision || '未关联 revision'}</small>
        </div>
        {paragraphs.length === 0 ? (
          <Typography.Paragraph>当前版本暂无正文。</Typography.Paragraph>
        ) : (
          <ol className="knowledge-paragraphs">
            {paragraphs.map((paragraph, index) => (
              <li key={index} className="knowledge-paragraph">
                <Typography.Paragraph>{paragraph}</Typography.Paragraph>
                <Tooltip title={`复制第 ${index + 1} 段的引用深链（含版本与段落号）`}>
                  <Button
                    size="small"
                    type="link"
                    onClick={() => {
                      const citation = `${window.location.origin}/knowledge?knowledgeId=${encodeURIComponent(item.knowledge_id)}&version=${encodeURIComponent(version?.version_id || item.current_version_id || '')}&para=${index + 1}`
                      void navigator.clipboard?.writeText(citation).catch(() => undefined)
                      message.success(`已复制第 ${index + 1} 段引用`)
                      onCopyCitation?.(index + 1)
                    }}
                  >
                    复制第 {index + 1} 段引用
                  </Button>
                </Tooltip>
              </li>
            ))}
          </ol>
        )}
      </section>

      {/* 来源健康：owner/scope/计数/索引版本/最近尝试与成功/错误 */}
      <section className="knowledge-source-health" data-testid="knowledge-source-health">
        <div className="section-heading"><strong>来源健康</strong><small>{indexAvailable ? '索引可用' : '索引不可用'}</small></div>
        {sourceHealth.length === 0 ? (
          <p className="muted-sm">服务端未返回来源健康数据；这不是"无来源"，而是来源健康接口未提供。</p>
        ) : (
          <ul>
            {sourceHealth.map((health) => (
              <li key={health.source}>
                <strong>{health.source}</strong>
                <span>
                  {health.scope ? `范围 ${health.scope} · ` : ''}
                  {health.documentCount !== undefined ? `文档 ${health.documentCount} · ` : ''}
                  {health.chunkCount !== undefined ? `分块 ${health.chunkCount} · ` : ''}
                  {health.indexVersion ? `索引 ${health.indexVersion} · ` : ''}
                  {health.lastSuccessAt ? `最近成功 ${health.lastSuccessAt}` : '无成功记录'}
                  {health.lastError ? ` · 错误：${health.lastError}` : ''}
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>

      {/* 禁用 + 删除验证：禁用后检索必须不再命中 */}
      {onDisable && (
        <section className="knowledge-disable" data-testid="knowledge-disable">
          <strong>删除与验证</strong>
          <p className="muted-sm">禁用后知识不再进入检索结果；必须验证索引、缓存与向量库均不再命中。</p>
          <Space wrap>
            <Button danger disabled={item.status === 'disabled'} onClick={() => void onDisable()}>禁用该知识</Button>
            <Button disabled={item.status !== 'disabled'} loading={disableVerification?.state === 'running'} onClick={() => void onVerifyRemoval?.()}>验证检索不再命中</Button>
          </Space>
          {disableVerification && disableVerification.state !== 'idle' && (
            <Alert
              style={{ marginTop: 8 }}
              type={disableVerification.state === 'passed' ? 'success' : disableVerification.state === 'failed' ? 'error' : 'info'}
              showIcon
              message={disableVerification.state === 'passed' ? '删除验证通过' : disableVerification.state === 'failed' ? '删除验证未通过' : '正在验证'}
              description={disableVerification.detail}
            />
          )}
        </section>
      )}
    </div>
  )
}
