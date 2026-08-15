import React, { useEffect, useMemo, useState } from 'react'
import {
  Button, Col, Input, InputNumber, Row, Select, Segmented, Space, Spin, Table, Tag, Typography,
} from 'antd'
import {
  k8sPreflight, k8sExecute,
  listK8sNamespaces, listK8sPods, listK8sDeployments, listK8sNodes,
  K8S_ACTION_KINDS, K8S_DESTRUCTIVE,
  type K8sPreflightResult, type K8sExecuteResult,
} from '../../api/k8s'
import { listApprovalTasks } from '../../api/client'
import { PageHeader, Breadcrumb, Empty } from '../../components/ui/PageKit'
import { useUIStore } from '../../store/uiStore'

const { Text } = Typography

// kind → 可用动作（与后端 ACTION_KINDS 一致，仅做前端过滤）
const ACTION_BY_KIND: Record<string, string[]> = {
  node: ['cordon', 'uncordon', 'drain'],
  pod: ['delete_pod', 'evict_pod'],
  deployment: ['rollout_restart', 'scale'],
  statefulset: ['rollout_restart', 'scale'],
  daemonset: ['rollout_restart'],
}

const ACTION_LABEL: Record<string, string> = {
  rollout_restart: '滚动重启',
  scale: '扩缩容',
  delete_pod: '删除 Pod',
  evict_pod: '驱逐 Pod',
  cordon: '节点隔离 (cordon)',
  uncordon: '节点恢复 (uncordon)',
  drain: '节点排空 (drain)',
}

const KIND_OPTIONS = ['pod', 'deployment', 'statefulset', 'daemonset', 'node']

function errText(e: any): string {
  return e?.response?.data?.error
    || e?.response?.data?.detail
    || e?.message
    || '请求失败'
}

// 动作参数 → 需要展示的表单字段
function actionParams(action: string): string[] {
  switch (action) {
    case 'scale': return ['replicas']
    case 'delete_pod':
    case 'evict_pod': return ['grace_period_seconds']
    case 'drain': return ['drain_timeout']
    default: return []
  }
}

interface ApprovalTaskOption { id: string; script: string; created_at?: string }

const K8sActions: React.FC = () => {
  const currentClusterId = useUIStore((s) => s.currentClusterId)
  const scopeLabel = useMemo(() => currentClusterId === 'all' ? '全部集群' : `集群 ${currentClusterId}`, [currentClusterId])

  // ── 资源区 ──
  const [namespaces, setNamespaces] = useState<string[]>([])
  const [nsFilter, setNsFilter] = useState<string>('all')
  const [resView, setResView] = useState<'pods' | 'deployments' | 'nodes'>('deployments')
  const [rows, setRows] = useState<any[]>([])
  const [listLoading, setListLoading] = useState(false)
  const [listErr, setListErr] = useState('')

  // ── 目标资源 + 动作 ──
  const [kind, setKind] = useState<string>('deployment')
  const [namespace, setNamespace] = useState('')
  const [name, setName] = useState('')
  const [action, setAction] = useState<string>('rollout_restart')
  const [replicas, setReplicas] = useState<number | null>(1)
  const [gracePeriod, setGracePeriod] = useState<number | null>(30)
  const [drainTimeout, setDrainTimeout] = useState<number | null>(300)
  const [approvalTaskId, setApprovalTaskId] = useState('')
  const [approvalOptions, setApprovalOptions] = useState<ApprovalTaskOption[]>([])
  const [approvalLoading, setApprovalLoading] = useState(false)

  // ── 预检 / 执行 ──
  const [preflight, setPreflight] = useState<K8sPreflightResult | null>(null)
  const [preflightLoading, setPreflightLoading] = useState(false)
  const [execResult, setExecResult] = useState<K8sExecuteResult | null>(null)
  const [execLoading, setExecLoading] = useState(false)
  const [execError, setExecError] = useState('')

  const destructive = K8S_DESTRUCTIVE.includes(action as any)

  // 资源列表加载
  const loadList = () => {
    setListLoading(true)
    setListErr('')
    const p: Promise<any> = resView === 'pods'
      ? listK8sPods(nsFilter)
      : resView === 'deployments'
        ? listK8sDeployments(nsFilter)
        : listK8sNodes()
    p.then((r) => {
      const d = r.data
      const list = d?.pods || d?.deployments || d?.nodes
      setRows(Array.isArray(list) ? list : [])
      if (d?.error && Array.isArray(list) && list.length === 0) setListErr(String(d.error))
    }).catch((e) => {
      setRows([])
      setListErr(e?.response?.data?.error || '列表加载失败')
    }).finally(() => setListLoading(false))
  }

  useEffect(() => { loadList() }, [resView, nsFilter, currentClusterId])

  // 命名空间列表
  useEffect(() => {
    listK8sNamespaces().then((r) => {
      const nss = r.data?.namespaces
      if (Array.isArray(nss)) setNamespaces(nss.map((n) => n.name).filter(Boolean))
    }).catch(() => {})
  }, [currentClusterId])

  // kind 切换时重置为第一个可用动作
  useEffect(() => {
    const acts = ACTION_BY_KIND[kind] || []
    setAction(acts[0] || '')
  }, [kind])

  // 资源参数变化 → 失效旧预检
  const resetPreflight = () => { setPreflight(null); setExecResult(null); setExecError('') }

  const buildExtra = (): Record<string, unknown> => {
    const extra: Record<string, unknown> = {}
    for (const f of actionParams(action)) {
      const v: number | null = f === 'replicas' ? replicas : f === 'grace_period_seconds' ? gracePeriod : drainTimeout
      if (v != null) extra[f] = v
    }
    return extra
  }

  const doPreflight = async () => {
    if (!name) return
    setExecResult(null); setExecError(''); setPreflight(null)
    setPreflightLoading(true)
    try {
      const r = await k8sPreflight({ action, kind, namespace, name, extra: buildExtra() })
      setPreflight(r.data)
      if (r.data?.ok === false) setExecError(r.data?.error || '预检失败')
    } catch (e: any) {
      setExecError(errText(e))
      setPreflight(null)
    } finally {
      setPreflightLoading(false)
    }
  }

  const doExecute = async () => {
    if (!preflight?.preflight_token) return
    setExecError(''); setExecResult(null)
    setExecLoading(true)
    try {
      const r = await k8sExecute({
        action, kind, namespace, name,
        extra: buildExtra(),
        preflight_token: preflight.preflight_token,
        expected_resource_version: preflight.resource_version || '',
        ...(destructive ? { approval_task_id: approvalTaskId } : {}),
      })
      setExecResult(r.data)
    } catch (e: any) {
      const st = e?.response?.status
      if (st === 400) setExecError('预检凭证无效或已过期，请重新预检后执行')
      else if (st === 409) setExecError('资源版本已变化，请重新预检后执行')
      else if (st === 403) setExecError(e?.response?.data?.error || '无审批权限或审批未通过')
      else setExecError(errText(e))
      setExecResult(null)
    } finally {
      setExecLoading(false)
    }
  }

  // 载入已批准审批单（供 destructive 动作选择 approval_task_id）
  const loadApprovedTasks = () => {
    setApprovalLoading(true)
    listApprovalTasks({ status: 'approved' })
      .then((r) => {
        const list = (r.data as any)?.tasks || []
        setApprovalOptions(Array.isArray(list) ? list.map((t: any) => ({
          id: t.id || t.task_id || '', script: t.script || '', created_at: t.created_at || '',
        })) : [])
      })
      .catch(() => setApprovalOptions([]))
      .finally(() => setApprovalLoading(false))
  }
  useEffect(() => { if (destructive) loadApprovedTasks() }, [destructive])

  const pickRow = (r: any) => {
    setKind(r.kind || 'pod')
    setNamespace(r.namespace || r.ns || '')
    setName(r.name || '')
    resetPreflight()
  }

  const resCols = resView === 'pods' ? [
    { title: '名称', dataIndex: 'name', key: 'name' },
    { title: '命名空间', dataIndex: 'namespace', key: 'namespace', width: 160 },
    { title: '状态', dataIndex: 'status', key: 'status', width: 110, render: (s?: string) => <Tag color={s === 'Running' ? 'green' : 'orange'}>{s || '-'}</Tag> },
    { title: '重启次数', dataIndex: 'restarts', key: 'restarts', width: 90, render: (v?: number) => v ?? '-' },
  ] : resView === 'deployments' ? [
    { title: '名称', dataIndex: 'name', key: 'name' },
    { title: '命名空间', dataIndex: 'namespace', key: 'namespace', width: 160 },
    { title: '副本', dataIndex: 'replicas', key: 'replicas', width: 90, render: (v?: number) => v ?? '-' },
    { title: '就绪', dataIndex: 'ready', key: 'ready', width: 90, render: (v?: number) => v ?? '-' },
  ] : [
    { title: '节点', dataIndex: 'name', key: 'name' },
    { title: '状态', dataIndex: 'status', key: 'status', width: 110, render: (s?: string) => <Tag color={s === 'Ready' ? 'green' : 'red'}>{s || '-'}</Tag> },
    { title: 'CPU', dataIndex: 'cpu', key: 'cpu', width: 110 },
    { title: '内存', dataIndex: 'memory', key: 'memory', width: 130 },
    { title: 'Kubelet', dataIndex: 'version', key: 'version', width: 140, render: (v?: string) => <span style={{ fontSize: 12 }}>{v || '-'}</span> },
  ]

  const paramFields = actionParams(action)

  return (
    <div>
      <Breadcrumb items={[{ t: '基础设施' }, { t: 'K8s 运维' }]} />
      <PageHeader title="K8s 运维" desc="结构化生命周期动作：预检 → 审批（危险动作）→ 执行，全程白名单校验 + 审计"
        actions={<Tag color="blue" style={{ marginRight: 0 }}>操作范围：{scopeLabel}</Tag>} />

      <Row gutter={16}>
        {/* 资源区 */}
        <Col xs={24} lg={13}>
          <div className="card" style={{ padding: 16 }}>
            <Space wrap style={{ width: '100%', marginBottom: 12 }}>
              <Segmented value={resView} onChange={(v) => setResView(v as any)}
                options={[{ label: 'Deployments', value: 'deployments' }, { label: 'Pods', value: 'pods' }, { label: '节点', value: 'nodes' }]} />
              {resView !== 'nodes' && (
                <Select value={nsFilter} onChange={setNsFilter} style={{ width: 200 }}
                  options={[{ value: 'all', label: '全部命名空间' }, ...namespaces.map((n) => ({ value: n, label: n }))]} />
              )}
              <Button size="small" onClick={loadList} loading={listLoading}>刷新</Button>
            </Space>
            {listErr && <div style={{ color: 'var(--danger)', fontSize: 12, marginBottom: 8 }}>⚠ {listErr}</div>}
            <Table rowKey={(r) => `${r.namespace || ''}-${r.name}`} loading={listLoading} columns={resCols} dataSource={rows}
              size="small" pagination={{ pageSize: 10, showSizeChanger: false }}
              onRow={(r) => ({ onClick: () => pickRow(r), style: { cursor: 'pointer' } })}
              locale={{ emptyText: <Empty text="暂无资源" hint="点击行可填入目标资源" /> }} />
            <div style={{ marginTop: 8, fontSize: 12, color: 'var(--text-muted)' }}>
              点击列表行填充下方目标资源；也可直接手动填写（预检会校验资源存在性）。
            </div>
          </div>
        </Col>

        {/* 操作区 */}
        <Col xs={24} lg={11}>
          <div className="card" style={{ padding: 16 }}>
            <div className="card__title" style={{ marginBottom: 12 }}>目标资源与动作</div>
            <Space wrap style={{ width: '100%', marginBottom: 4 }}>
              <Select value={kind} onChange={(v) => { setKind(v); resetPreflight() }} style={{ width: 130 }}
                options={KIND_OPTIONS.map((k) => ({ value: k, label: k }))} />
              {kind !== 'node' && (
                <Input value={namespace} onChange={(e) => { setNamespace(e.target.value); resetPreflight() }}
                  placeholder="命名空间" style={{ width: 130 }} />
              )}
              <Input value={name} onChange={(e) => { setName(e.target.value); resetPreflight() }}
                placeholder="资源名称" style={{ width: 200 }} />
            </Space>

            <Space wrap style={{ width: '100%', margin: '10px 0' }}>
              <Select value={action} onChange={(v) => { setAction(v); resetPreflight() }} style={{ width: 170 }}
                options={(ACTION_BY_KIND[kind] || []).map((a) => ({ value: a, label: ACTION_LABEL[a] }))} />
              {paramFields.includes('replicas') && (
                <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>副本数</span>
              )}
              {paramFields.includes('replicas') && (
                <InputNumber value={replicas} onChange={(v) => { setReplicas(v); resetPreflight() }} min={0} style={{ width: 100 }} />
              )}
              {paramFields.includes('grace_period_seconds') && (
                <>
                  <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>宽限秒数</span>
                  <InputNumber value={gracePeriod} onChange={(v) => { setGracePeriod(v); resetPreflight() }} min={0} style={{ width: 100 }} />
                </>
              )}
              {paramFields.includes('drain_timeout') && (
                <>
                  <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>超时秒数</span>
                  <InputNumber value={drainTimeout} onChange={(v) => { setDrainTimeout(v); resetPreflight() }} min={0} style={{ width: 100 }} />
                </>
              )}
            </Space>

            {destructive && (
              <div style={{ padding: '10px 12px', marginBottom: 12, borderRadius: 8, background: 'var(--danger-soft)', border: '1px solid rgba(220,38,38,.18)' }}>
                <div style={{ color: 'var(--danger)', fontWeight: 600, fontSize: 13, marginBottom: 8 }}>
                  该动作属于破坏性操作，必须关联已批准审批单才能执行
                </div>
                <Space wrap>
                  <Input value={approvalTaskId} onChange={(e) => setApprovalTaskId(e.target.value)}
                    placeholder="审批单 ID（approval_task_id）" style={{ width: 220 }} />
                  <Select value={approvalTaskId} onChange={setApprovalTaskId} placeholder="或从已批准审批单选择"
                    loading={approvalLoading} style={{ width: 260 }} allowClear showSearch
                    optionFilterProp="label"
                    options={approvalOptions.map((t) => ({ value: t.id, label: `${t.id} · ${(t.script || '').slice(0, 60)}` }))} />
                  <Button size="small" onClick={loadApprovedTasks} loading={approvalLoading}>刷新</Button>
                </Space>
              </div>
            )}

            <Space wrap>
              <Button type="primary" onClick={doPreflight} loading={preflightLoading} disabled={!name}>
                ① 预检
              </Button>
              <Button danger type="primary" onClick={doExecute} loading={execLoading}
                disabled={!preflight?.preflight_token || (destructive && !approvalTaskId)}>
                ② 执行{preflight?.preflight_token ? '' : '（需先预检）'}
              </Button>
              <Button size="small" onClick={resetPreflight}>清空</Button>
            </Space>

            {preflight && (
              <div style={{ marginTop: 14, padding: '10px 12px', borderRadius: 8, background: 'var(--surface-2)', border: '1px solid var(--border)' }}>
                <div style={{ fontWeight: 600, fontSize: 12, marginBottom: 6, color: 'var(--text-secondary)' }}>预检通过 · 命令预览</div>
                <pre style={{ fontFamily: 'var(--font-mono)', fontSize: 12, whiteSpace: 'pre-wrap', margin: 0 }}>{preflight.command}</pre>
                <div style={{ marginTop: 8, fontSize: 12 }}>
                  <Text type="secondary">resourceVersion：</Text>
                  <Text code>{preflight.resource_version}</Text>
                </div>
                {preflight.category && (
                  <Tag style={{ marginTop: 6 }} color="blue">{preflight.category}</Tag>
                )}
              </div>
            )}

            {execError && (
              <div style={{ marginTop: 12, padding: '10px 12px', borderRadius: 8, background: 'var(--danger-soft)', color: 'var(--danger)', fontSize: 12 }}>
                ⚠ {execError}
              </div>
            )}

            {execResult && (
              <div style={{ marginTop: 14, padding: '10px 12px', borderRadius: 8, background: 'var(--success-soft)', border: '1px solid rgba(22,163,74,.2)' }}>
                <div style={{ fontWeight: 600, fontSize: 12, marginBottom: 6, color: 'var(--success)' }}>执行结果</div>
                <pre style={{ fontFamily: 'var(--font-mono)', fontSize: 12, whiteSpace: 'pre-wrap', margin: 0, color: 'var(--text)' }}>
                  {execResult.output || '(无输出)'}
                </pre>
              </div>
            )}
          </div>
        </Col>
      </Row>
    </div>
  )
}

export default K8sActions
