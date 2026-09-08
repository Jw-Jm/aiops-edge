import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { Alert, Button, Drawer, Empty, Space, Table, Tag, Timeline, Typography, message } from 'antd'
import { Link } from 'react-router-dom'
import { decideAction, getAction, listActions, type ActionProjection } from '../../api/client'
import { canDecideAction, toActionViewModel } from './actionModel'
import { useAuthStore } from '../../store/authStore'

const lifecycle = (action: ActionProjection) => [
  { label: '提出', value: action.status || '未提供' },
  { label: '预检', value: action.preflight_status || '未提供' },
  { label: '审批', value: action.status === 'approved' ? 'approved' : action.status === 'rejected' ? 'rejected' : 'pending' },
  { label: '执行', value: action.execution_status || '未提供' },
  { label: '验证', value: '未提供' },
  { label: '回滚', value: '未提供' },
]

const ActionCenter: React.FC = () => {
  const role = useAuthStore((state) => state.role)
  const [actions, setActions] = useState<ActionProjection[]>([])
  const [selected, setSelected] = useState<ActionProjection | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [deciding, setDeciding] = useState(false)
  const load = useCallback(() => { setLoading(true); setError(''); listActions({ limit: 100 }).then((response) => setActions(response.data?.actions ?? [])).catch((e) => setError(e?.response?.data?.error || e?.message || '动作加载失败')).finally(() => setLoading(false)) }, [])
  useEffect(() => { load() }, [load])
  const columns = useMemo(() => [
    { title: '操作 / 目标', key: 'target', render: (_: unknown, row: ActionProjection) => <div><Typography.Text strong>{row.operation || row.action_type}</Typography.Text><div style={{ fontSize: 12, color: 'var(--text-secondary)' }}>{row.namespace ? `${row.namespace}/` : ''}{row.target_name}</div></div> },
    { title: '来源 Run', dataIndex: 'run_id', render: (value: string) => <Link to={`/investigation/${value}`}>{value || '—'}</Link> },
    { title: '风险', key: 'risk', render: (_: unknown, row: ActionProjection) => { const vm = toActionViewModel(row as ActionProjection & { risk_level?: string; risk_score?: number }); return <Tag color={row.status === 'approved' ? 'orange' : 'blue'}>{vm.risk.label}</Tag> } },
    { title: '影响范围', key: 'impact', render: (_: unknown, row: ActionProjection) => toActionViewModel(row as ActionProjection & { impact_summary?: string }).impact },
    { title: '审批', dataIndex: 'status', render: (value: string) => <Tag color={value === 'approved' ? 'green' : value === 'rejected' ? 'red' : 'gold'}>{value || '未提供'}</Tag> },
    { title: '执行', dataIndex: 'execution_status', render: (value: string) => value || '未提供' },
    { title: '验证', key: 'verification', render: (_: unknown, row: ActionProjection) => toActionViewModel(row as ActionProjection & { verification_status?: string }).verificationStatus },
    { title: '操作', key: 'actions', render: (_: unknown, row: ActionProjection) => <Button type="link" size="small" onClick={() => setSelected(row)}>查看详情</Button> },
  ], [])
  const decide = async (decision: 'approved' | 'rejected') => {
    if (!selected) return
    setDeciding(true)
    try { await decideAction(selected.action_id, { decision, reason: decision === 'approved' ? '用户批准' : '用户拒绝', action_version: selected.action_version, idempotency_key: crypto.randomUUID() }); message.success(decision === 'approved' ? '动作已批准' : '动作已拒绝'); setSelected(null); load() }
    catch (e: any) { message.error(e?.response?.status === 409 || e?.response?.status === 412 ? '动作版本已变化，请刷新后重试' : e?.response?.data?.error || '动作决策失败') }
    finally { setDeciding(false) }
  }
  const refreshDetail = async () => { if (!selected) return; try { const response = await getAction(selected.action_id); setSelected(response.data) } catch { /* list data remains visible */ } }
  return <div data-testid="action-center">{error && <Alert type="error" showIcon message={error} action={<Button size="small" onClick={load}>重试</Button>} style={{ marginBottom: 12 }} />}<Table rowKey="action_id" loading={loading} columns={columns} dataSource={actions} pagination={{ pageSize: 20 }} locale={{ emptyText: <Empty description="暂无动作" /> }} onRow={(row) => ({ onClick: () => setSelected(row) })} /><Drawer open={!!selected} width={600} title="动作详情" onClose={() => setSelected(null)} extra={<Button onClick={refreshDetail}>刷新</Button>}>{selected && <><Space direction="vertical" size={4} style={{ width: '100%' }}><Typography.Title level={5} style={{ margin: 0 }}>{selected.operation} · {selected.target_name}</Typography.Title><Typography.Text type="secondary">来源 Run：<Link to={`/investigation/${selected.run_id}`}>{selected.run_id}</Link></Typography.Text><Typography.Text>动作版本：{selected.action_version} · 预检：{selected.preflight_status || '未提供'}</Typography.Text></Space><Timeline style={{ marginTop: 24 }} items={lifecycle(selected).map((step) => ({ children: <Space><Typography.Text strong>{step.label}</Typography.Text><Tag>{step.value}</Tag></Space> }))} />{selected.status === 'proposed' && <Space><Button type="primary" loading={deciding} disabled={!canDecideAction(role)} onClick={() => void decide('approved')}>批准执行</Button><Button danger loading={deciding} disabled={!canDecideAction(role)} onClick={() => void decide('rejected')}>拒绝</Button></Space>}<div style={{ marginTop: 20, color: 'var(--text-secondary)', fontSize: 12 }}>风险、影响范围、验证和回滚字段仅在后端提供时展示，不使用前端推断。</div></>}</Drawer></div>
}

export default ActionCenter
