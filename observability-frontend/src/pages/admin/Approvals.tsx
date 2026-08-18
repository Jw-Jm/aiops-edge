import React, { useEffect, useState, useCallback } from 'react'
import { Alert, Button, Drawer, Input, Modal, Segmented, Space, Table, Tag, message } from 'antd'
import { listApprovalTasks, approveTask, rejectTask } from '../../api/client'
import { PageHeader, Breadcrumb, Empty, StatusBadge, type StatusTone } from '../../components/ui/PageKit'
import { useAuthStore } from '../../store/authStore'
import { fmtTime } from '../../lib/format'
import AppIcon from '../../components/AppIcons'

// =====================================================================
//  审批中心：所有涉及环境操作变更（执行命令 / K8s 动作 / 恢复方案 / AI 处置建议）
//  均需人工审核。waiting 状态任务带 plan/script/risk_score/risk_reason，是核心数据源。
//  批准/驳回均二次确认 + 显式展示"将执行的命令"，操作可追溯。
// =====================================================================

// 深色代码块：突出"将执行的环境命令"，与平台亮色主题形成强对比，传达安全敏感语义
const codeBlockStyle: React.CSSProperties = {
  background: '#0f172a',
  color: '#e2e8f0',
  fontFamily: 'var(--font-mono)',
  fontSize: 12.5,
  lineHeight: 1.6,
  padding: '12px 14px',
  borderRadius: 8,
  whiteSpace: 'pre-wrap',
  wordBreak: 'break-all',
  overflow: 'auto',
  maxHeight: 320,
  border: '1px solid #1e293b',
  margin: 0,
}

// B12: 与后端约定单一风险分格式（0-1），删除原先对 0~1 / 1~5 / 0~100 三格式的猜测。
// 0-1 小数 → 1-5 星。
function riskStars(score: any): { stars: number; color: string; level: string; tagColor: string } | null {
  if (score == null || score === '') return null
  const n = Number(score)
  if (isNaN(n) || n < 0) return null
  const clamped = Math.max(0, Math.min(1, n))
  const stars = Math.max(1, Math.min(5, Math.round(clamped * 5)))
  const color = stars >= 4 ? 'var(--danger)' : stars >= 3 ? 'var(--warning)' : 'var(--success)'
  const level = stars >= 5 ? '极高风险' : stars >= 4 ? '高风险' : stars >= 3 ? '中风险' : '低风险'
  const tagColor = stars >= 4 ? 'red' : stars >= 3 ? 'orange' : 'green'
  return { stars, color, level, tagColor }
}

const SOURCE_COLOR: Record<string, string> = {
  ai_chat: 'blue', ai: 'blue',
  recovery: 'purple',
  flow: 'cyan', workflow: 'cyan',
  k8s: 'geekblue',
}
function sourceTag(s?: string) {
  const label = s || '未知'
  const color = SOURCE_COLOR[(s || '').toLowerCase()] || 'default'
  return <Tag color={color} style={{ marginRight: 0 }}>{label}</Tag>
}

const STATUS_MAP: Record<string, { tone: StatusTone; text: string }> = {
  waiting: { tone: 'warn', text: '待审批' },
  approved: { tone: 'ok', text: '已批准' },
  rejected: { tone: 'muted', text: '已驳回' },
  queued: { tone: 'info', text: '排队中' },
  diagnosing: { tone: 'info', text: '诊断中' },
  done: { tone: 'ok', text: '已完成' },
  failed: { tone: 'crit', text: '失败' },
}
function statusBadge(s: string) {
  const m = STATUS_MAP[s] || { tone: 'muted' as StatusTone, text: s || '—' }
  return <StatusBadge text={m.text} tone={m.tone} />
}

type Tab = 'waiting' | 'approved' | 'rejected' | 'all'

const Approvals: React.FC = () => {
  const role = useAuthStore((s) => s.role)
  const isApprover = role === 'approver' || role === 'admin'

  const [tab, setTab] = useState<Tab>('waiting')
  const [rows, setRows] = useState<any[]>([])
  const [loading, setLoading] = useState(false)
  const [detail, setDetail] = useState<any | null>(null)
  const [approveTarget, setApproveTarget] = useState<any | null>(null)
  const [rejectTarget, setRejectTarget] = useState<any | null>(null)
  const [rejectReason, setRejectReason] = useState('')
  const [acting, setActing] = useState(false)

  const load = useCallback(() => {
    setLoading(true)
    const params = tab === 'all' ? {} : { status: tab }
    listApprovalTasks(params)
      .then((r) => {
        const list = (r.data as any)?.tasks || []
        setRows(Array.isArray(list) ? list : [])
      })
      .catch(() => { setRows([]); message.error('加载审批任务失败') })
      .finally(() => setLoading(false))
  }, [tab])

  useEffect(() => { load() }, [load])

  const doApprove = async () => {
    if (!approveTarget) return
    setActing(true)
    try {
      await approveTask(approveTarget.id)
      message.success('已批准，环境操作将执行')
      setApproveTarget(null)
      load()
    } catch (e: any) {
      message.error(e?.response?.data?.error || e?.response?.data?.detail || '批准失败')
    } finally {
      setActing(false)
    }
  }

  const doReject = async () => {
    if (!rejectTarget) return
    setActing(true)
    try {
      await rejectTask(rejectTarget.id, rejectReason.trim() || undefined)
      message.success('已驳回，环境操作不会执行')
      setRejectTarget(null)
      setRejectReason('')
      load()
    } catch (e: any) {
      message.error(e?.response?.data?.error || e?.response?.data?.detail || '驳回失败')
    } finally {
      setActing(false)
    }
  }

  const columns = [
    {
      title: '来源', dataIndex: 'source', key: 'source', width: 110,
      render: (v: string) => sourceTag(v),
    },
    {
      title: '服务', dataIndex: 'service', key: 'service', width: 150,
      render: (v: string) => v ? <span style={{ fontFamily: 'var(--font-mono)', fontSize: 12 }}>{v}</span> : '—',
    },
    {
      title: '风险等级', key: 'risk', width: 150,
      render: (_: unknown, r: any) => {
        const rv = riskStars(r.risk_score)
        if (!rv) return <span style={{ color: 'var(--text-muted)', fontSize: 12 }}>未评估</span>
        return (
          <Space size={6}>
            <span style={{ color: rv.color, letterSpacing: 1, fontSize: 13 }}>
              {'★'.repeat(rv.stars)}{'☆'.repeat(5 - rv.stars)}
            </span>
            <Tag color={rv.tagColor} style={{ marginRight: 0 }}>{rv.level}</Tag>
          </Space>
        )
      },
    },
    {
      title: '方案摘要', dataIndex: 'plan', key: 'plan',
      render: (v: string) => (
        <span title={v} style={{
          display: 'inline-block', maxWidth: 320, whiteSpace: 'nowrap',
          overflow: 'hidden', textOverflow: 'ellipsis', verticalAlign: 'middle',
          color: v ? 'var(--text-secondary)' : 'var(--text-muted)', fontSize: 12,
        }}>
          {v ? (v.length > 60 ? v.slice(0, 60) + '…' : v) : '—'}
        </span>
      ),
    },
    {
      title: '创建时间', dataIndex: 'created_at', key: 'created_at', width: 130,
      render: (v: string) => <span style={{ fontSize: 12, color: 'var(--text-secondary)' }}>{fmtTime(v)}</span>,
    },
    {
      title: '操作', key: 'act', width: 210,
      render: (_: unknown, r: any) => (
        <Space wrap size={4} onClick={(e) => e.stopPropagation()}>
          <Button size="small" onClick={() => setDetail(r)}>查看详情</Button>
          {r.status === 'waiting' && isApprover && (
            <>
              <Button size="small" danger onClick={() => { setRejectReason(''); setRejectTarget(r) }}>驳回</Button>
              <Button size="small" type="primary"
                style={{ background: 'var(--success)', borderColor: 'var(--success)' }}
                onClick={() => setApproveTarget(r)}>批准</Button>
            </>
          )}
          {r.status === 'waiting' && !isApprover && (
            <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>需审批人权限</span>
          )}
        </Space>
      ),
    },
  ]

  // 详情 Drawer 内容
  const renderDetail = () => {
    if (!detail) return null
    const rv = riskStars(detail.risk_score)
    return (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        <Space wrap size={16}>
          {statusBadge(detail.status)}
          <span style={{ fontSize: 12, color: 'var(--text-secondary)' }}>来源：{detail.source || '—'}</span>
          <span style={{ fontSize: 12, color: 'var(--text-secondary)' }}>服务：{detail.service || '—'}</span>
          <span style={{ fontSize: 12, color: 'var(--text-secondary)' }}>创建：{fmtTime(detail.created_at)}</span>
        </Space>

        <div>
          <div style={{ fontSize: 12, color: 'var(--text-muted)', marginBottom: 6 }}>风险等级</div>
          {rv ? (
            <Space size={8}>
              <span style={{ color: rv.color, letterSpacing: 1, fontSize: 15 }}>{'★'.repeat(rv.stars)}{'☆'.repeat(5 - rv.stars)}</span>
              <Tag color={rv.tagColor} style={{ marginRight: 0 }}>{rv.level}</Tag>
            </Space>
          ) : <span style={{ color: 'var(--text-muted)' }}>未评估</span>}
          {detail.risk_reason && (
            <div style={{ marginTop: 6, fontSize: 12, color: 'var(--warning)', whiteSpace: 'pre-wrap' }}>{detail.risk_reason}</div>
          )}
        </div>

        {detail.plan && (
          <div>
            <div style={{ fontSize: 12, color: 'var(--text-muted)', marginBottom: 6 }}>处置方案</div>
            <div style={{ whiteSpace: 'pre-wrap', fontSize: 13, lineHeight: 1.7, color: 'var(--text)' }}>{detail.plan}</div>
          </div>
        )}

        <div>
          <div style={{ fontSize: 12, color: 'var(--danger)', marginBottom: 6, fontWeight: 600 }}>⚠ 将执行的环境命令</div>
          <pre style={codeBlockStyle}>{detail.script || '（无命令）'}</pre>
        </div>

        {detail.diagnosis && (
          <div>
            <div style={{ fontSize: 12, color: 'var(--text-muted)', marginBottom: 6 }}>诊断上下文</div>
            <div style={{ whiteSpace: 'pre-wrap', fontSize: 12, lineHeight: 1.7, color: 'var(--text-secondary)', background: 'var(--surface-2)', padding: 12, borderRadius: 8, border: '1px solid var(--border)' }}>{detail.diagnosis}</div>
          </div>
        )}
      </div>
    )
  }

  return (
    <div>
      <Breadcrumb items={[{ t: '系统管理' }, { t: '审批中心' }]} />
      <PageHeader title="审批中心"
        desc="所有涉及环境操作变更（执行命令、K8s 动作、恢复方案、AI 处置建议）均需人工审核批准后方可执行，操作留痕可追溯"
        actions={
          <Space>
            <Button icon={<AppIcon name="approvals" />} onClick={load} loading={loading}>刷新</Button>
          </Space>
        } />

      <div className="card" style={{ padding: 16, marginBottom: 16 }}>
        <Segmented value={tab} onChange={(v) => setTab(v as Tab)}
          options={[
            { label: '待审批', value: 'waiting' },
            { label: '已批准', value: 'approved' },
            { label: '已驳回', value: 'rejected' },
            { label: '全部', value: 'all' },
          ]} />
      </div>

      <div className="card" style={{ padding: 0 }}>
        <Table rowKey={(r) => r.id || r.task_id} loading={loading} columns={columns} dataSource={rows} size="middle"
          pagination={{ pageSize: 10, showSizeChanger: false }}
          onRow={(r) => ({ onClick: () => setDetail(r), style: { cursor: 'pointer' } })}
          locale={{ emptyText: <Empty text={tab === 'waiting' ? '当前无待审批任务' : '暂无审批记录'} hint="涉及环境操作的变更会在此汇总，需审批人确认后执行" /> }} />
      </div>

      {/* 详情 Drawer */}
      <Drawer title="审批任务详情" open={!!detail} onClose={() => setDetail(null)} width={720}
        styles={{ body: { padding: 20 } }}
        extra={detail?.status === 'waiting' && isApprover ? (
          <Space>
            <Button danger onClick={() => { setRejectReason(''); setRejectTarget(detail); setDetail(null) }}>驳回</Button>
            <Button type="primary" style={{ background: 'var(--success)', borderColor: 'var(--success)' }}
              onClick={() => { setApproveTarget(detail); setDetail(null) }}>批准</Button>
          </Space>
        ) : undefined}>
        {renderDetail()}
      </Drawer>

      {/* 批准二次确认：显式展示将执行的命令 + 风险说明 */}
      <Modal title="批准执行确认" open={!!approveTarget} onCancel={() => setApproveTarget(null)}
        okText="确认批准执行" cancelText="取消" width={640} destroyOnClose
        okButtonProps={{ loading: acting, style: { background: 'var(--success)', borderColor: 'var(--success)' } }}
        onOk={doApprove}>
        <Alert type="warning" showIcon style={{ marginBottom: 14 }}
          message="批准后将立即在目标环境执行以下命令，此操作不可撤销"
          description="请确认你已获得授权，并已核对命令内容与影响范围。" />
        {approveTarget?.service && (
          <div style={{ fontSize: 12, marginBottom: 6 }}>
            <span style={{ color: 'var(--text-muted)' }}>目标服务：</span>
            <span style={{ fontFamily: 'var(--font-mono)' }}>{approveTarget.service}</span>
          </div>
        )}
        <div style={{ fontSize: 12, color: 'var(--danger)', marginBottom: 6, fontWeight: 600 }}>将执行以下命令</div>
        <pre style={codeBlockStyle}>{approveTarget?.script || '（无命令）'}</pre>
        {(() => { const rv = riskStars(approveTarget?.risk_score); return rv ? (
          <div style={{ marginTop: 12, fontSize: 12 }}>
            <span style={{ color: rv.color, marginRight: 8 }}>{'★'.repeat(rv.stars)}{'☆'.repeat(5 - rv.stars)} {rv.level}</span>
            {approveTarget?.risk_reason && <span style={{ color: 'var(--text-secondary)' }}>{approveTarget.risk_reason}</span>}
          </div>
        ) : null })()}
      </Modal>

      {/* 驳回二次确认 + 可选原因（写入审计） */}
      <Modal title="驳回确认" open={!!rejectTarget}
        onCancel={() => { setRejectTarget(null); setRejectReason('') }}
        okText="确认驳回" cancelText="取消" destroyOnClose
        okButtonProps={{ danger: true, loading: acting }}
        onOk={doReject}>
        <Alert type="info" showIcon style={{ marginBottom: 14 }}
          message="驳回后该环境操作将不会执行，记录将留痕。" />
        <div style={{ fontSize: 12, color: 'var(--text-muted)', marginBottom: 6 }}>将驳回的命令</div>
        <pre style={codeBlockStyle}>{rejectTarget?.script || '（无命令）'}</pre>
        <div style={{ marginTop: 14, fontSize: 12, color: 'var(--text-muted)', marginBottom: 6 }}>驳回原因（可选，写入审计）</div>
        <Input.TextArea rows={3} value={rejectReason} onChange={(e) => setRejectReason(e.target.value)}
          placeholder="如：命令风险过高 / 已通过其他方式处理 / 误告警…"
          maxLength={500} showCount />
      </Modal>
    </div>
  )
}

export default Approvals