import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { Alert, Button, Descriptions, Drawer, Empty, Modal, Space, Table, Tabs, Tag, Timeline, Typography, message } from 'antd'
import { Link } from 'react-router-dom'
import { decideAction, getAction, listActions, type ActionProjection } from '../../api/client'
import { canDecideAction, toActionViewModel } from './actionModel'
import { useAuthStore } from '../../store/authStore'
import { useScopeStore } from '../../store/scopeStore'

const actionTabs = [
  { key: 'proposed', label: '待审批' },
  { key: 'approved', label: '待执行' },
  { key: 'executing', label: '执行中' },
  { key: 'verification', label: '待验证' },
  { key: 'ended', label: '已完成/失败' },
]

const lifecycle = (action: ActionProjection) => [
  { label: '提出', value: action.status || '未提供' },
  { label: '预检', value: action.preflight_status || '未提供' },
  { label: '审批', value: action.status === 'approved' ? 'approved' : action.status === 'rejected' ? 'rejected' : 'pending' },
  { label: '执行', value: action.execution_status || '未提供' },
  { label: '验证', value: action.verification_status || '未提供' },
  { label: '回滚', value: action.rollback_summary || '未提供' },
]

function snapshotFrom(action: ActionProjection, key: 'before_snapshot' | 'after_snapshot'): unknown {
  const direct = action[key]
  if (direct != null) return direct
  const result = action.result
  if (result && typeof result === 'object') {
    return result[key] ?? (key === 'before_snapshot' ? result.before : result.after)
  }
  return null
}

function formatDetail(value: unknown): string {
  if (value == null || value === '') return '未提供'
  return typeof value === 'object' ? JSON.stringify(value) : String(value)
}

const ActionCenter: React.FC = () => {
  const role = useAuthStore((state) => state.role)
  const activeClusterId = useScopeStore((state) => state.authScope?.activeClusterId ?? '')
  const [actions, setActions] = useState<ActionProjection[]>([])
  const [selected, setSelected] = useState<ActionProjection | null>(null)
  const [pendingDecision, setPendingDecision] = useState<'approved' | 'rejected' | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [deciding, setDeciding] = useState(false)

  const load = useCallback(() => {
    if (!activeClusterId) {
      setActions([]); setLoading(false); setError('')
      return
    }
    setLoading(true); setError('')
    listActions({ limit: 100 }).then((response) => setActions(response.data?.actions ?? []))
      .catch((e) => setError(e?.response?.data?.error || e?.message || '动作加载失败'))
      .finally(() => setLoading(false))
  }, [activeClusterId])
  useEffect(() => { load() }, [load])

  const columns = useMemo(() => [
    { title: '操作 / 目标', key: 'target', render: (_: unknown, row: ActionProjection) => <div><Typography.Text strong>{row.operation || row.action_type}</Typography.Text><div style={{ fontSize: 12, color: 'var(--text-secondary)' }}>{row.namespace ? `${row.namespace}/` : ''}{row.target_name}</div></div> },
    { title: '来源 Run', dataIndex: 'run_id', render: (value: string) => <Link to={`/investigation/${value}`}>{value || '—'}</Link> },
    { title: '风险', key: 'risk', render: (_: unknown, row: ActionProjection) => <Tag color="gold">{toActionViewModel(row).risk.label}</Tag> },
    { title: '影响范围', key: 'impact', render: (_: unknown, row: ActionProjection) => toActionViewModel(row).impact },
    { title: '审批', dataIndex: 'status', render: (value: string) => <Tag color={value === 'approved' ? 'green' : value === 'rejected' ? 'red' : 'gold'}>{value || '未提供'}</Tag> },
    { title: '执行', dataIndex: 'execution_status', render: (value: string) => value || '未提供' },
    { title: '验证', key: 'verification', render: (_: unknown, row: ActionProjection) => toActionViewModel(row).verificationStatus },
    { title: '创建时间', dataIndex: 'created_at', render: (value: string) => value ? value.slice(0, 19).replace('T', ' ') : '未提供' },
    { title: '操作', key: 'actions', render: (_: unknown, row: ActionProjection) => <Button type="link" size="small" onClick={() => setSelected(row)}>查看详情</Button> },
  ], [])

  const filteredActions = (key: string) => key === 'verification'
    ? actions.filter((action) => ['pending_verification', 'awaiting_verification'].includes(action.execution_status) || action.verification_status === 'pending')
    : key === 'ended'
      ? actions.filter((action) => ['rejected', 'failed', 'succeeded', 'completed'].includes(action.status) || ['failed', 'succeeded', 'completed'].includes(action.execution_status))
      : key === 'executing'
        ? actions.filter((action) => ['executing', 'running'].includes(action.execution_status))
        : actions.filter((action) => action.status === key)

  const decide = async (decision: 'approved' | 'rejected') => {
    if (!selected) return
    setDeciding(true)
    try {
      await decideAction(selected.action_id, { decision, reason: decision === 'approved' ? '用户批准' : '用户拒绝', action_version: selected.action_version, idempotency_key: crypto.randomUUID() })
      message.success(decision === 'approved' ? '动作已批准' : '动作已拒绝')
      setSelected(null); load()
    } catch (e: any) {
      message.error(e?.response?.status === 409 || e?.response?.status === 412 ? '动作版本已变化，请刷新后重试' : e?.response?.data?.error || '动作决策失败')
    } finally { setDeciding(false) }
  }
  const refreshDetail = async () => { if (!selected) return; try { setSelected((await getAction(selected.action_id)).data) } catch { /* 保留当前快照 */ } }

  return <div data-testid="action-center">
    {error && <Alert type="error" showIcon role="alert" message={error} action={<Button size="small" onClick={load}>重试</Button>} style={{ marginBottom: 12 }} />}
    <Tabs defaultActiveKey="proposed" items={actionTabs.map((tab) => ({ key: tab.key, label: `${tab.label} (${filteredActions(tab.key).length})`, children: <Table rowKey="action_id" loading={loading} columns={columns} dataSource={filteredActions(tab.key)} pagination={{ pageSize: 20 }} locale={{ emptyText: <Empty description="暂无动作" /> }} onRow={(row) => ({ onClick: () => setSelected(row) })} /> }))} destroyOnHidden />
    <Drawer open={!!selected} width={620} title="动作详情" onClose={() => setSelected(null)} extra={<Button onClick={refreshDetail}>刷新</Button>}>
      {selected && <>
        <Space direction="vertical" size={4} style={{ width: '100%' }}><Typography.Title level={5} style={{ margin: 0 }}>{selected.operation} · {selected.target_name}</Typography.Title><Typography.Text type="secondary">来源 Run：<Link to={`/investigation/${selected.run_id}`}>{selected.run_id}</Link></Typography.Text></Space>
        <Descriptions size="small" column={1} style={{ marginTop: 16 }} items={[
          { key: 'uid', label: '目标 UID', children: selected.target_uid || '未提供' },
          { key: 'rv', label: 'ResourceVersion', children: selected.resource_version || '未提供' },
          { key: 'preflight', label: '预检', children: selected.preflight_status || '未提供' },
          { key: 'hash', label: 'Action Hash / Schema', children: `${selected.action_hash || '未提供'} · ${selected.hash_schema_version || '未提供'}` },
          { key: 'policy', label: 'Policy / 规范化参数', children: `${selected.policy_version || '未提供'} · ${selected.params ? JSON.stringify(selected.params) : '未提供'}` },
          { key: 'before', label: '执行前快照', children: formatDetail(snapshotFrom(selected, 'before_snapshot')) },
          { key: 'after', label: '执行后快照', children: formatDetail(snapshotFrom(selected, 'after_snapshot')) },
          { key: 'people', label: '创建人 / 审批人 / 时间', children: `${selected.created_by || '未提供'} / ${selected.approved_by || '未提供'} / ${selected.approved_at || selected.created_at || '未提供'}` },
          { key: 'rollback', label: '回滚策略', children: selected.rollback_summary || '未提供' },
          { key: 'verify', label: '验证条件', children: selected.verification_status || '未提供' },
        ]} />
        <Timeline style={{ marginTop: 24 }} items={lifecycle(selected).map((step) => ({ children: <Space><Typography.Text strong>{step.label}</Typography.Text><Tag>{step.value}</Tag></Space> }))} />
        {selected.status === 'proposed' && canDecideAction(role) && <Space><Button type="primary" loading={deciding} onClick={() => setPendingDecision('approved')}>批准执行</Button><Button danger loading={deciding} onClick={() => setPendingDecision('rejected')}>拒绝</Button></Space>}
        <div style={{ marginTop: 20, color: 'var(--text-secondary)', fontSize: 12 }}>没有服务端字段时显示“未提供”，不使用前端推断。</div>
      </>}
    </Drawer>
    <Modal open={!!pendingDecision} title={pendingDecision === 'approved' ? '确认批准动作' : '确认拒绝动作'} okText="确认" cancelText="取消" confirmLoading={deciding} onCancel={() => setPendingDecision(null)} onOk={() => { const decision = pendingDecision; setPendingDecision(null); if (decision) void decide(decision) }}><p>该决策将写入审计记录，并使用当前动作版本提交。请确认目标与 ResourceVersion 未发生变化。</p></Modal>
  </div>
}

export default ActionCenter
