import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { Alert, Button, Drawer, Input, Radio, Segmented, Select, Table, Tag } from 'antd'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { useNavigate, useSearchParams } from 'react-router-dom'
import {
  INSPECTION_TEMPLATES,
  downloadReport,
  generateAIOperationsReport,
  generateInspectionReport,
  listReports,
  reportBucket,
  type GeneratedInspectionReport,
  type InspectionTemplateId,
  type ReportRow,
} from '../../api/reports'
import BoundedDataRegion from '../../components/display/BoundedDataRegion'
import DataState from '../../components/display/DataState'
import { Empty, PageHeader, PaneCard, StatusBadge, type StatusTone } from '../../components/ui/PageKit'
import { useScopeStore } from '../../store/scopeStore'
import { addKnowledgeCase } from '../../api/client'
import { resourceDomainOf, resourceLocation, resourceTypeLabel } from '../../features/resources/resourceDomain'
import type { PlatformResourceRef } from '../../features/resources/types'

const VERDICT_TONE: Record<string, StatusTone> = {
  healthy: 'ok',
  partial: 'warn',
  stale: 'warn',
  not_connected: 'muted',
  failed: 'crit',
  unknown: 'muted',
}

const VERDICT_LABEL: Record<string, string> = {
  healthy: '正常',
  partial: '部分异常',
  stale: '数据陈旧',
  not_connected: '来源未接入',
  failed: '失败',
  unknown: '未知',
}

function formatTime(value?: string): string {
  if (!value) return '未提供'
  const parsed = Date.parse(value)
  return Number.isFinite(parsed) ? new Date(parsed).toLocaleString('zh-CN', { hour12: false, timeZoneName: 'short' }) : value
}

/** 报告对象必须使用类型化资源身份，不能把所有对象称为"服务"。 */
export function reportSubject(row: ReportRow, clusterId: string): string {
  if (row.resourceType && row.resourceUid) {
    const domain = resourceDomainOf(row.resourceType as never)
    if (domain) {
      const ref: PlatformResourceRef = {
        clusterId,
        uid: row.resourceUid,
        type: row.resourceType as never,
        domain,
        name: row.resourceName || row.resourceUid,
        ...(domain === 'kubernetes' && row.resourceNamespace ? { namespace: row.resourceNamespace } : {}),
      }
      return `${resourceTypeLabel(row.resourceType as never)} · ${resourceLocation(ref)}`
    }
  }
  return row.serviceName ? `服务 · ${row.serviceName}` : '集群范围'
}

const Reports: React.FC = () => {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const clusterId = useScopeStore((s) => s.authScope?.activeClusterId ?? '')
  // 深链支持：/reports?type=ai-operations&runId=...（AI 运维任务"由本任务生成报告"入口）
  const deepLinkRunId = searchParams.get('runId') ?? ''
  const deepLinkType = searchParams.get('type') ?? ''
  const [bucket, setBucket] = useState<'inspection' | 'ai'>(
    deepLinkType === 'ai-operations' ? 'ai' : 'inspection',
  )
  const [rows, setRows] = useState<ReportRow[]>([])
  const [state, setState] = useState<'loading' | 'ready' | 'empty' | 'error' | 'forbidden'>('loading')
  const [error, setError] = useState<unknown>()
  const [template, setTemplate] = useState<InspectionTemplateId>('standard')
  const [windowHours, setWindowHours] = useState(24)
  const [generating, setGenerating] = useState(false)
  const [generated, setGenerated] = useState<GeneratedInspectionReport | null>(null)
  const [generateError, setGenerateError] = useState<string>('')
  const [aiRunId, setAiRunId] = useState(deepLinkRunId)
  const [aiGenerating, setAiGenerating] = useState(false)
  const [aiResult, setAiResult] = useState<{ verdict: string; evidenceCount: number; summary: string; id: number } | null>(null)
  const [aiError, setAiError] = useState<string>('')
  const [preview, setPreview] = useState<ReportRow | null>(null)
  const [downloadingId, setDownloadingId] = useState<number | null>(null)
  const [downloadError, setDownloadError] = useState<string>('')

  const load = useCallback(() => {
    if (!clusterId) {
      setRows([])
      setState('empty')
      return () => undefined
    }
    const controller = new AbortController()
    setState('loading')
    listReports({ clusterId }, controller.signal)
      .then((list) => {
        setRows(list)
        setState(list.length === 0 ? 'empty' : 'ready')
      })
      .catch((reason) => {
        setError(reason)
        setState(reason?.response?.status === 403 ? 'forbidden' : 'error')
      })
    return () => controller.abort()
  }, [clusterId])

  useEffect(() => load(), [load])

  const filtered = useMemo(() => rows.filter((r) => reportBucket(r.reportType) === bucket), [rows, bucket])

  const handleGenerate = () => {
    if (!clusterId) return
    setGenerating(true)
    setGenerateError('')
    setGenerated(null)
    const to = new Date()
    const from = new Date(to.getTime() - windowHours * 3600 * 1000)
    generateInspectionReport({
      clusterId,
      template,
      from: from.toISOString(),
      to: to.toISOString(),
    })
      .then((report) => {
        setGenerated(report)
        load()
      })
      .catch((err) => {
        setGenerateError(err?.response?.data?.detail || err?.response?.data?.error || err?.message || '生成失败')
      })
      .finally(() => setGenerating(false))
  }

  const handleGenerateAI = () => {
    if (!clusterId || !aiRunId.trim()) return
    setAiGenerating(true)
    setAiError('')
    setAiResult(null)
    generateAIOperationsReport({ clusterId, runId: aiRunId.trim() })
      .then((report) => {
        setAiResult(report)
        load()
      })
      .catch((err) => {
        setAiError(err?.response?.data?.detail || err?.response?.data?.error || err?.message || '生成失败')
      })
      .finally(() => setAiGenerating(false))
  }

  const handleDownload = (row: ReportRow) => {
    setDownloadingId(row.id)
    setDownloadError('')
    downloadReport(clusterId, row.id)
      .then((blob) => {
        const url = URL.createObjectURL(blob)
        const a = document.createElement('a')
        a.href = url
        a.download = `${row.reportType || 'report'}-${row.id}.md`
        document.body.appendChild(a)
        a.click()
        a.remove()
        URL.revokeObjectURL(url)
      })
      .catch((err) => setDownloadError(err?.response?.data?.error || err?.message || '下载失败'))
      .finally(() => setDownloadingId(null))
  }

  return (
    <div className="reports-page" data-testid="reports-page">
      <PageHeader
        title="报告"
        desc="仅两类主入口：巡检报告与 AI 运维报告；页面、导出与 API 使用同一聚合与单位"
        actions={<Button onClick={load}>刷新</Button>}
      />

      <Segmented
        value={bucket}
        onChange={(v) => setBucket(v as 'inspection' | 'ai')}
        options={[
          { label: '巡检报告', value: 'inspection' },
          { label: 'AI 运维报告', value: 'ai' },
        ]}
        aria-label="报告类型"
      />

      {!clusterId && (
        <Alert
          type="warning"
          showIcon
          style={{ marginTop: 12 }}
          message="未选择集群作用域"
          description="报告按租户与集群隔离；请在顶栏选择集群后再查看或生成报告。"
        />
      )}

      {bucket === 'inspection' && (
        <PaneCard title="生成巡检报告" style={{ marginTop: 12 }}>
          <div className="report-generate">
            <Select
              value={template}
              onChange={(v) => setTemplate(v as InspectionTemplateId)}
              options={INSPECTION_TEMPLATES.map((t) => ({ value: t.id, label: `${t.label} · ${t.desc}` }))}
              style={{ minWidth: 320 }}
              aria-label="巡检模板"
            />
            <Radio.Group value={windowHours} onChange={(e) => setWindowHours(e.target.value)} aria-label="时间窗">
              <Radio.Button value={24}>最近 24 小时</Radio.Button>
              <Radio.Button value={168}>最近 7 天</Radio.Button>
            </Radio.Group>
            <Button type="primary" loading={generating} disabled={!clusterId} onClick={handleGenerate}>
              立即生成
            </Button>
          </div>
          {generateError && <Alert type="error" showIcon style={{ marginTop: 8 }} message="生成失败" description={generateError} />}
          {generated && (
            <Alert
              type={generated.verdict === 'healthy' ? 'success' : 'warning'}
              showIcon
              style={{ marginTop: 8 }}
              message={`报告已生成，整体状态 ${VERDICT_LABEL[generated.verdict] ?? generated.verdict}`}
              description={`报告 ID ${generated.id} · 查询合同 ${generated.document.query_contract} · 章节 ${generated.document.sections.length} 个`}
              action={<Button size="small" onClick={() => setGenerated(null)}>收起</Button>}
            />
          )}
          {generated && generated.document.warnings.length > 0 && (
            <Alert
              type="warning"
              showIcon
              style={{ marginTop: 8 }}
              message="本次巡检存在未接入来源"
              description={<ul>{generated.document.warnings.map((w) => <li key={w}>{w}</li>)}</ul>}
            />
          )}
          <p className="muted-sm" style={{ marginTop: 8 }}>
            周期计划、生成进度、取消与失败重试尚未实现（后端缺口）；当前为同步生成，未实现项不会被伪装为可用。
          </p>
        </PaneCard>
      )}

      {bucket === 'ai' && (
        <PaneCard title="从任务生成 AI 运维报告" style={{ marginTop: 12 }}>
          <div className="report-generate">
            <Input
              value={aiRunId}
              onChange={(e) => setAiRunId(e.target.value)}
              placeholder="输入已完成的调查任务 Run ID（如 1c62feca-1dfb-48a2-…）"
              aria-label="调查任务 Run ID"
              style={{ maxWidth: 420 }}
              allowClear
            />
            <Button type="primary" onClick={handleGenerateAI} loading={aiGenerating} disabled={!aiRunId.trim() || !clusterId}>
              由任务生成报告
            </Button>
            <Button onClick={() => navigate('/ai-operations?sourcePage=/reports')}>前往 AI 智能运维</Button>
          </div>
          {aiError && (
            <Alert
              type="error"
              showIcon
              style={{ marginTop: 12 }}
              message="生成失败"
              description={aiError === 'RUN_NOT_TERMINAL' ? '该任务尚未完成或终止；仅已完成/明确的终态任务可生成报告。' : aiError}
            />
          )}
          {aiResult && (
            <Alert
              type="success"
              showIcon
              style={{ marginTop: 12 }}
              message={`已生成 AI 运维报告 #${aiResult.id} · 结论等级 ${aiResult.verdict} · 证据 ${aiResult.evidenceCount} 条`}
              description={aiResult.summary}
            />
          )}
          {!clusterId && (
            <Alert type="warning" showIcon style={{ marginTop: 12 }} message="请先在顶栏选择集群作用域" />
          )}
        </PaneCard>
      )}

      <PaneCard title={bucket === 'inspection' ? '巡检报告' : 'AI 运维报告'} style={{ marginTop: 12 }} action={<span className="muted-sm">{filtered.length} 份</span>}>
        <BoundedDataRegion state={state} error={error} onRetry={load}>
          {filtered.length === 0 ? (
            <Empty
              text={bucket === 'inspection' ? '当前集群暂无巡检报告' : '当前集群暂无 AI 运维报告'}
              hint={bucket === 'inspection' ? '使用上方"立即生成"创建第一份巡检报告' : '从已完成的 AI 智能运维任务生成报告'}
            />
          ) : (
            <Table
              size="small"
              rowKey="id"
              pagination={{ pageSize: 10 }}
              dataSource={filtered}
              columns={[
                { title: '报告', key: 'type', width: 200, render: (_: unknown, r: ReportRow) => <span>{r.reportType || '未提供'}<br /><small className="muted-sm">{r.taskId}</small></span> },
                {
                  title: '对象',
                  key: 'subject',
                  width: 200,
                  render: (_: unknown, r: ReportRow) => <span>{reportSubject(r, clusterId)}</span>,
                },
                { title: '整体状态', key: 'verdict', width: 120, render: (_: unknown, r: ReportRow) => <StatusBadge text={VERDICT_LABEL[r.verdict] ?? (r.verdict || '未提供')} tone={VERDICT_TONE[r.verdict] ?? 'muted'} /> },
                { title: '摘要', dataIndex: 'summary', key: 'summary', render: (v: string) => <span className="cell-wrap">{v || '未提供'}</span> },
                { title: '生成时间', dataIndex: 'createdAt', key: 'createdAt', width: 200, render: formatTime },
                {
                  title: '操作',
                  key: 'actions',
                  width: 160,
                  render: (_: unknown, r: ReportRow) => (
                    <div className="report-actions">
                      <Button type="link" size="small" onClick={() => setPreview(r)}>查看</Button>
                      <Button type="link" size="small" loading={downloadingId === r.id} onClick={() => handleDownload(r)}>下载</Button>
                    </div>
                  ),
                },
              ]}
            />
          )}
        </BoundedDataRegion>
        {downloadError && <Alert type="error" showIcon style={{ marginTop: 8 }} message="下载失败" description={downloadError} />}
      </PaneCard>

      <Drawer
        open={Boolean(preview)}
        onClose={() => setPreview(null)}
        width={720}
        title={preview ? `${preview.reportType} · ${preview.taskId}` : '报告'}
      >
        {preview && (
          <>
            <div className="report-preview-meta">
              <Tag>{VERDICT_LABEL[preview.verdict] ?? preview.verdict}</Tag>
              <span className="muted-sm">生成时间 {formatTime(preview.createdAt)}</span>
              {preview.riskScore !== null && <span className="muted-sm">风险分 {preview.riskScore}</span>}
            </div>
            {preview.content ? (
              <div className="report-markdown"><ReactMarkdown remarkPlugins={[remarkGfm]}>{preview.content}</ReactMarkdown></div>
            ) : (
              <DataState kind="empty" title="该报告没有正文内容" description="正文缺失时明确表达，不使用摘要冒充正文" />
            )}
            {/* AI 运维沉淀必须先生成草稿并经审阅发布，不能自动写入生产知识库（§6.6） */}
            <div className="report-preview-actions">
              <Button
                onClick={() => {
                  void addKnowledgeCase({
                    title: preview.summary || preview.reportType,
                    content: preview.content,
                    category: 'ai_operation',
                    status: 'pending_review',
                    sourceRunId: preview.sourceRunId || preview.taskId,
                    clusterId,
                  })
                }}
              >
                加入运维知识（待审核）
              </Button>
              <span className="muted-sm">沉淀先进入草稿状态，需人工审阅后才发布到知识库</span>
            </div>
          </>
        )}
      </Drawer>
    </div>
  )
}

export default Reports
