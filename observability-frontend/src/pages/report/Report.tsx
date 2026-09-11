import React, { useEffect, useState } from 'react'
import { Alert, Table, Button, message, Tag, Drawer, Space } from 'antd'
import { BookOutlined } from '@ant-design/icons'
import ReactMarkdown from 'react-markdown'
import { useNavigate } from 'react-router-dom'
import { listReports, addKnowledgeCase } from '../../api/client'
import api from '../../api/client'
import { useScopeStore } from '../../store/scopeStore'
import { resourceDomainOf, resourceLocation, resourceTypeLabel } from '../../features/resources/resourceDomain'
import type { PlatformResourceRef } from '../../features/resources/types'

interface Report { id?: string; task_id?: string; service_name?: string; report_type?: string; verdict?: string; risk_score?: number; summary?: string; created_at?: string; title?: string; status?: string; cluster_id?: string; resource_uid?: string; resource_type?: string; resource_name?: string; namespace?: string; source_run_id?: string; impact_count?: number; duration_ms?: number; root_cause?: string; action_status?: string; verification_status?: string; evidence_count?: number }

function reportResource(report: Report): PlatformResourceRef | undefined {
  const domain = resourceDomainOf((report.resource_type || '') as never)
  if (!report.resource_uid || !domain || report.resource_type === 'k8s_cluster') return undefined
  return { clusterId: report.cluster_id || '', uid: report.resource_uid, type: report.resource_type as never, domain, name: report.resource_name || report.service_name || report.resource_uid, ...(domain === 'kubernetes' && report.namespace ? { namespace: report.namespace } : {}) }
}

export function reportSubject(report: Report): string {
  const resource = reportResource(report)
  if (resource) return `${resourceTypeLabel(resource.type)} · ${resourceLocation(resource)}`
  return report.service_name ? `服务 · ${report.service_name}` : '集群范围'
}

const Report: React.FC = () => {
  const activeClusterId = useScopeStore((s) => s.authScope?.activeClusterId ?? '')
  const navigate = useNavigate()
  const [data, setData] = useState<Report[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [reloadToken, setReloadToken] = useState(0)
  const [preview, setPreview] = useState<Report | null>(null) // 2.18 预览

  useEffect(() => {
    if (!activeClusterId) {
      setData([])
      setLoading(false)
      setError('')
      return
    }
    const load = () => {
      setError('')
      setLoading(true)
      listReports({ limit: 100, cluster_id: activeClusterId }).then((r) => {
        const d = Array.isArray(r.data) ? r.data : r.data?.reports || []
        setData(d)
      }).catch((e: any) => {
        setData([])
        setError(e?.response?.data?.error || e?.message || '报告数据加载失败')
      }).finally(() => setLoading(false))
    }
    load()
    // Issue7: 30s 轮询刷新，使 AI 对话新生成的巡检/诊断报告自动出现在报告中心，无需手动刷新
    // 切换服务端 active scope 后重建 effect，报告列表不会跨作用域复用。
    // B12: Tab 隐藏时暂停轮询（visibilitychange），避免后台空转请求
    let timer: ReturnType<typeof setInterval> | null = null
    const start = () => { if (!timer) timer = setInterval(load, 30000) }
    const stop = () => { if (timer) { clearInterval(timer); timer = null } }
    const onVis = () => { document.visibilityState === 'visible' ? start() : stop() }
    document.addEventListener('visibilitychange', onVis)
    start()
    return () => { document.removeEventListener('visibilitychange', onVis); stop() }
  }, [activeClusterId, reloadToken])

  const taskIdOf = (r: any) => r.task_id || r.id || ''
  const reportTypeName = (rt?: string) =>
    rt === 'report' ? '诊断报告' : rt === 'inspection' ? '巡检报告' : (rt || '报告')
  // 2.18 命名：类型 + typed resource + 短时间
  const reportTitle = (r: any) => {
    const rt = reportTypeName(r.report_type)
    const subject = reportSubject(r)
    // 2.18 用时间作后缀（去掉 task_id 随机字符串），如"诊断报告 order-svc 07-21 14:03"
    const t = r.created_at ? ` ${(r.created_at || '').slice(5, 16).replace('T', ' ')}` : ''
    return `${rt} · ${subject}${t}`
  }

  const download = (r: Report) => {
    const taskId = taskIdOf(r)
    if (!taskId) { message.warning('缺少报告 ID'); return }
    api.get(`/ops/reports/${taskId}/download`, { responseType: 'blob' })
      .then((res) => {
        const url = URL.createObjectURL(res.data as any)
        const a = document.createElement('a')
        a.href = url
        // 修复 5.10：文件名只取"类型+服务+短时间"，不拼接随机 task_id
        a.download = `${reportTitle(r).replace(/[/\\:*?"<>|]/g, '_')}.md`
        a.click()
        URL.revokeObjectURL(url)
      })
      .catch(() => message.warning('该报告无下载文件（仅元数据）'))
  }

  // 需求：报告一键加入知识库（POST /ai/knowledge/case，质量审查失败返回 400）
  const [caseLoading, setCaseLoading] = useState<Record<string, boolean>>({})
  const [caseAdded, setCaseAdded] = useState<Record<string, boolean>>({})
  const addToKnowledge = (r: Report) => {
    const taskId = taskIdOf(r)
    if (!taskId || caseLoading[taskId]) return
    if (caseAdded[taskId]) { message.info('该报告已加入知识库'); return }
    setCaseLoading((p) => ({ ...p, [taskId]: true }))
    addKnowledgeCase({ report_id: taskId, status: 'pending_review', sourceRunId: r.source_run_id || taskId, source_run_id: r.source_run_id || taskId })
      .then((res: any) => {
        const d = res.data || {}
        if (d.inserted === false) {
          message.warning('已存在相似案例')
        } else {
          message.success(`已加入知识库 (案例 ${d.case_id || taskId})`)
        }
        setCaseAdded((p) => ({ ...p, [taskId]: true }))
      })
      .catch((e: any) => {
        const err = e?.response?.data?.error || e?.response?.data?.detail || e?.message || '加入失败'
        message.error(`质量审查未通过：${err}`)
      })
      .finally(() => setCaseLoading((p) => ({ ...p, [taskId]: false })))
  }

  const cols = [
    { title: '报告', dataIndex: 'task_id', key: 'task_id', render: (_: any, r: any) => <span style={{ fontFamily: 'var(--font-mono)', fontSize: 12 }}>{reportTitle(r)}</span> },
    { title: '资源事件', key: 'subject', render: (_: any, r: Report) => <span>{reportSubject(r)}</span> },
    { title: '集群', dataIndex: 'cluster_id', key: 'cluster_id', width: 130, render: (v: string) => v && v !== 'default' ? <Tag color="blue">{v}</Tag> : <span style={{ color: 'var(--text-muted)' }}>主集群</span> },
    { title: '影响', key: 'impact', width: 90, render: (_: any, r: Report) => r.impact_count == null ? <span style={{ color: 'var(--text-muted)' }}>未提供</span> : `${r.impact_count} 个资源` },
    { title: '持续', key: 'duration', width: 90, render: (_: any, r: Report) => r.duration_ms == null ? <span style={{ color: 'var(--text-muted)' }}>未提供</span> : `${Math.round(r.duration_ms / 60000)} 分钟` },
    { title: '来源 Run', key: 'run', width: 150, render: (_: any, r: Report) => r.source_run_id ? <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11 }}>{r.source_run_id}</span> : <span style={{ color: 'var(--text-muted)' }}>未提供</span> },
    { title: '时间', dataIndex: 'created_at', key: 'created_at', render: (v: string) => <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>{v ? v.slice(0, 19).replace('T', ' ') : '-'}</span> },
    { title: '操作', key: 'op', width: 220, render: (_: any, r: Report) => {
        const taskId = taskIdOf(r)
        return (
          <Space size={0}>
            <Button size="small" type="link" onClick={() => setPreview(r)}>预览</Button>
            <Button size="small" type="link" onClick={() => download(r)}>下载</Button>
            <Button size="small" type="link" icon={<BookOutlined />}
              loading={!!caseLoading[taskId]}
              disabled={!!caseAdded[taskId]}
              onClick={() => addToKnowledge(r)}>{caseAdded[taskId] ? '已进入审核队列' : '加入运维知识（待审核）'}</Button>
          </Space>
        )
      } },
  ]

  return (
    <div>
      {error && <Alert type="error" showIcon role="alert" message="报告数据读取失败" description={error} action={<Button size="small" onClick={() => setReloadToken((value) => value + 1)}>重试</Button>} style={{ marginBottom: 12 }} />}
      {/* 错误态与空态互斥：读取失败时不渲染空表；空数据使用自然高度卡片并提供下一步。 */}
      {error ? null : loading ? (
        <div className="card" style={{ padding: 0 }}><Table rowKey={taskIdOf} loading columns={cols} dataSource={[]} size="middle" pagination={false} /></div>
      ) : data.length === 0 ? (
        <div className="card report-empty-state">
          <strong>当前集群暂无报告</strong>
          <p>报告来自已完成的调查与巡检。当前集群还没有可展示的报告。</p>
          <Button type="primary" onClick={() => navigate(activeClusterId ? `/clusters/${encodeURIComponent(activeClusterId)}/investigations` : '/clusters')}>前往调查</Button>
        </div>
      ) : (
        <div className="card" style={{ padding: 0 }}>
          <Table rowKey={taskIdOf} columns={cols} dataSource={data} size="middle"
            pagination={{ pageSize: 20 }} />
        </div>
      )}

      {/* 2.18 预览：summary 全文 + verdict + 元信息 */}
      <Drawer width={560} open={!!preview} onClose={() => setPreview(null)} title="报告预览"
        styles={{ body: { padding: 16, background: 'var(--surface-1)' } }}>
        {preview && (
          <div>
            <div style={{ fontSize: 16, fontWeight: 700, marginBottom: 4 }}>{reportTitle(preview)}</div>
            <Space style={{ marginBottom: 16, flexWrap: 'wrap' }}>
              <Tag color="blue">{reportTypeName(preview.report_type)}</Tag>
              <Tag>{reportSubject(preview)}</Tag>
              {preview.cluster_id && preview.cluster_id !== 'default' && <Tag>集群：{preview.cluster_id}</Tag>}
              {preview.source_run_id && <Tag>来源 Run：{preview.source_run_id}</Tag>}
              {preview.verdict && <Tag color={preview.verdict === 'safe' || preview.verdict === 'pass' ? 'green' : 'orange'}>{String(preview.verdict)}</Tag>}
              {preview.created_at && <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>{preview.created_at.slice(0, 19).replace('T', ' ')}</span>}
            </Space>
            {/* 修复 5.1：markdown 渲染摘要，保留标题/列表/粗体等结构 */}
            <div className="report-template" style={{ fontSize: 13, lineHeight: 1.8, color: 'var(--text)' }}>
              <section><h4>结论摘要</h4><ReactMarkdown>{preview.summary || '未提供'}</ReactMarkdown></section>
              <section><h4>影响范围</h4><p>{preview.impact_count == null ? '未提供' : `${preview.impact_count} 个资源`}{preview.duration_ms == null ? '' : ` · 持续 ${Math.round(preview.duration_ms / 60000)} 分钟`}</p></section>
              <section><h4>根因与证据</h4><p>{preview.root_cause || preview.verdict || '未提供；请从关联调查 Run 查看原始证据。'}</p></section>
              <section><h4>处置建议</h4><p>{preview.action_status || '未提供'}</p></section>
              <section><h4>验证结果</h4><p>{preview.verification_status || '未提供'}</p></section>
              <section><h4>审计信息</h4><p>{preview.created_at ? `生成时间：${preview.created_at.slice(0, 19).replace('T', ' ')}` : '未提供'}</p></section>
            </div>
          </div>
        )}
      </Drawer>
    </div>
  )
}

export default Report
