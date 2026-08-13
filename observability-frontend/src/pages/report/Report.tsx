import React, { useEffect, useState } from 'react'
import { Table, Button, message, Tag, Drawer, Space } from 'antd'
import ReactMarkdown from 'react-markdown'
import { listReports } from '../../api/client'
import api from '../../api/client'
import { PageHeader, Breadcrumb, Empty } from '../../components/ui/PageKit'

interface Report { id?: string; task_id?: string; service_name?: string; report_type?: string; verdict?: string; risk_score?: number; summary?: string; created_at?: string; title?: string; status?: string; cluster_id?: string }

const Report: React.FC = () => {
  const [data, setData] = useState<Report[]>([])
  const [loading, setLoading] = useState(true)
  const [preview, setPreview] = useState<Report | null>(null) // 2.18 预览

  useEffect(() => {
    const load = () => {
      listReports({ limit: 100 }).then((r) => {
        // /ops/reports/history 返回 { history: [{task_id, service_name, report_type, verdict, risk_score, summary, created_at}] }
        const d = Array.isArray(r.data) ? r.data : r.data?.history || r.data?.reports || r.data?.data || []
        setData(d)
      }).catch(() => setData([])).finally(() => setLoading(false))
    }
    load()
    // Issue7: 30s 轮询刷新，使 AI 对话新生成的巡检/诊断报告自动出现在报告中心，无需手动刷新
    const timer = setInterval(load, 30000)
    return () => clearInterval(timer)
  }, [])

  const taskIdOf = (r: any) => r.task_id || r.id || ''
  const reportTypeName = (rt?: string) =>
    rt === 'report' ? '诊断报告' : rt === 'inspection' ? '巡检报告' : (rt || '报告')
  // 2.18 命名：类型 + 服务 + 短时间
  const reportTitle = (r: any) => {
    const rt = reportTypeName(r.report_type)
    const svc = r.service_name ? ` ${r.service_name}` : ''
    // 2.18 用时间作后缀（去掉 task_id 随机字符串），如"诊断报告 order-svc 07-21 14:03"
    const t = r.created_at ? ` ${(r.created_at || '').slice(5, 16).replace('T', ' ')}` : ''
    return `${rt}${svc}${t}`
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

  const cols = [
    { title: '报告', dataIndex: 'task_id', key: 'task_id', render: (_: any, r: any) => <span style={{ fontFamily: 'var(--font-mono)', fontSize: 12 }}>{reportTitle(r)}</span> },
    { title: '集群', dataIndex: 'cluster_id', key: 'cluster_id', width: 130, render: (v: string) => v && v !== 'default' ? <Tag color="blue">{v}</Tag> : <span style={{ color: 'var(--text-muted)' }}>主集群</span> },
    { title: '时间', dataIndex: 'created_at', key: 'created_at', render: (v: string) => <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>{v ? v.slice(0, 19).replace('T', ' ') : '-'}</span> },
    { title: '操作', key: 'op', width: 140, render: (_: any, r: Report) => (
        <Space size={0}>
          <Button size="small" type="link" onClick={() => setPreview(r)}>预览</Button>
          <Button size="small" type="link" onClick={() => download(r)}>下载</Button>
        </Space>
      ) },
  ]

  return (
    <div>
      <Breadcrumb items={[{ t: '报告' }, { t: '报告中心' }]} />
      <PageHeader title="报告中心" desc="诊断报告 / 巡检报告的生成与下载" />
      <div className="card" style={{ padding: 0 }}>
        <Table rowKey="task_id" loading={loading} columns={cols} dataSource={data} size="middle"
          pagination={{ pageSize: 20 }} locale={{ emptyText: <Empty text="暂无报告" /> }} />
      </div>

      {/* 2.18 预览：summary 全文 + verdict + 元信息 */}
      <Drawer width={560} open={!!preview} onClose={() => setPreview(null)} title="报告预览"
        styles={{ body: { padding: 16, background: 'var(--surface-1)' } }}>
        {preview && (
          <div>
            <div style={{ fontSize: 16, fontWeight: 700, marginBottom: 4 }}>{reportTitle(preview)}</div>
            <Space style={{ marginBottom: 16, flexWrap: 'wrap' }}>
              <Tag color="blue">{reportTypeName(preview.report_type)}</Tag>
              {preview.service_name && <Tag>服务：{preview.service_name}</Tag>}
              {preview.cluster_id && preview.cluster_id !== 'default' && <Tag>集群：{preview.cluster_id}</Tag>}
              {preview.verdict && <Tag color={preview.verdict === 'safe' || preview.verdict === 'pass' ? 'green' : 'orange'}>{String(preview.verdict)}</Tag>}
              {preview.created_at && <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>{preview.created_at.slice(0, 19).replace('T', ' ')}</span>}
            </Space>
            {/* 修复 5.1：markdown 渲染摘要，保留标题/列表/粗体等结构 */}
            <div className="markdown-body" style={{ fontSize: 13, lineHeight: 1.8, color: 'var(--text)' }}>
              <ReactMarkdown>{preview.summary || '暂无摘要'}</ReactMarkdown>
            </div>
          </div>
        )}
      </Drawer>
    </div>
  )
}

export default Report
