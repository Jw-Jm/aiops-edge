import React, { useEffect, useMemo, useState } from 'react'
import {
  Button, Col, Input, InputNumber, Modal, Row, Select, Segmented, Space, Table, Tag, Typography,
} from 'antd'
import {
  createK8sActionProposal, executeAiAction,
  listK8sNamespaces, listK8sPods, listK8sDeployments, listK8sNodes,
  K8S_ACTION_KINDS,
  type K8sActionName, type K8sActionProjection, type K8sActionExecuteResult,
} from '../../api/k8s'
import { PageHeader, Breadcrumb, Empty } from '../../components/ui/PageKit'
import { useUIStore } from '../../store/uiStore'

const { Text } = Typography

// B12: kind → 可用动作，由 k8s.ts 的 K8S_ACTION_KINDS（单一来源）反查派生，
// 不再在页面内硬编码一份 ACTION_BY_KIND，避免与后端动作清单漂移。
const ACTION_BY_KIND: Record<string, string[]> = Object.entries(K8S_ACTION_KINDS).reduce((acc, [action, kinds]) => {
  kinds.forEach((k) => { (acc[k] ||= []).push(action) })
  return acc
}, {} as Record<string, string[]>)

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
  // ── Canonical Action proposal / execute ──
  const [proposalKey, setProposalKey] = useState('')
  const [actionRecord, setActionRecord] = useState<K8sActionProjection | null>(null)
  const [preflightLoading, setPreflightLoading] = useState(false)
  const [execResult, setExecResult] = useState<K8sActionExecuteResult | null>(null)
  const [execLoading, setExecLoading] = useState(false)
  const [execError, setExecError] = useState('')
  const [confirmVisible, setConfirmVisible] = useState(false)

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
      // B12: 列表错误不再仅空列表时显示——即使有数据也展示后端 error（如部分命名空间失败）
      if (d?.error) setListErr(String(d.error))
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
  const resetPreflight = () => {
    setProposalKey('')
    setActionRecord(null)
    setExecResult(null)
    setExecError('')
    setConfirmVisible(false)
  }

  const buildExtra = (): Record<string, unknown> => {
    const extra: Record<string, unknown> = {}
    for (const f of actionParams(action)) {
      const v: number | null = f === 'replicas' ? replicas : f === 'grace_period_seconds' ? gracePeriod : drainTimeout
      if (v != null) extra[f] = v
    }
    return extra
  }

  const doPreflight = async () => {
    if (!name || currentClusterId === 'all') return
    setExecResult(null); setExecError(''); setActionRecord(null)
    setPreflightLoading(true)
    try {
      const key = proposalKey || `ui-k8s-${Date.now()}-${Math.random().toString(36).slice(2)}`
      setProposalKey(key)
      const r = await createK8sActionProposal({
        idempotency_key: key,
        cluster_id: currentClusterId,
        resource_type: kind,
        namespace,
        target_name: name,
        operation: action as K8sActionName,
        params: buildExtra(),
      })
      setActionRecord(r.data)
    } catch (e: any) {
      setExecError(errText(e))
      setActionRecord(null)
    } finally {
      setPreflightLoading(false)
    }
  }

  const doExecute = async () => {
    if (!actionRecord?.action_id) return
    setExecError(''); setExecResult(null)
    setExecLoading(true)
    try {
      const r = await executeAiAction(actionRecord.action_id)
      setExecResult(r.data)
      if (r.data?.status === 'rejected') {
        setExecError(r.data.message || '执行器已拒绝，未发生真实变更')
      }
    } catch (e: any) {
      const st = e?.response?.status
      if (st === 422) setExecError('Canonical Action 尚未审批通过，未发生真实变更')
      else if (st === 409) setExecError('Action 状态或资源版本已变化，请重新查看审批中心')
      else if (st === 403) setExecError(e?.response?.data?.message || e?.response?.data?.error || '无执行权限，未发生真实变更')
      else setExecError(errText(e))
      setExecResult(null)
    } finally {
      setExecLoading(false)
    }
  }

  const requestExecute = () => {
    if (!actionRecord?.action_id || actionRecord.status !== 'approved') return
    setConfirmVisible(true)
  }

  const pickRow = (r: any) => {
    const pickedKind = r.kind || (resView === 'deployments' ? 'deployment' : resView === 'pods' ? 'pod' : 'node')
    setKind(pickedKind)
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

            <div style={{ padding: '10px 12px', marginBottom: 12, borderRadius: 8, background: 'var(--surface-2)', border: '1px solid var(--border)' }}>
              <div style={{ fontWeight: 600, fontSize: 13, marginBottom: 4 }}>Canonical Action 审批边界</div>
              <div style={{ color: 'var(--text-muted)', fontSize: 12 }}>
                预检会创建不可变 Action 并进入审批中心；页面不再接受旧 approval_task_id。
                当前执行器保持禁用，未审批或被拒绝的动作不会改变环境。
              </div>
            </div>

            <Space wrap>
              <Button type="primary" onClick={doPreflight} loading={preflightLoading} disabled={!name || currentClusterId === 'all'}>
                ① 预检并提交审批
              </Button>
              <Button danger type="primary" onClick={requestExecute} loading={execLoading}
                disabled={!actionRecord?.action_id || actionRecord.status !== 'approved'}>
                ② 执行{actionRecord?.status === 'approved' ? '' : '（需 Canonical Action 审批）'}
              </Button>
              <Button size="small" onClick={resetPreflight}>清空</Button>
            </Space>

            {currentClusterId === 'all' && (
              <div style={{ marginTop: 12, color: 'var(--warning)', fontSize: 12 }}>
                请先在全局导航选择一个具体集群，再创建 K8s Canonical Action。
              </div>
            )}

            {actionRecord && (
              <div style={{ marginTop: 14, padding: '10px 12px', borderRadius: 8, background: 'var(--surface-2)', border: '1px solid var(--border)' }}>
                <div style={{ fontWeight: 600, fontSize: 12, marginBottom: 8, color: 'var(--text-secondary)' }}>
                  Action 已创建 · {actionRecord.status === 'proposed' ? '待审批' : actionRecord.status}
                </div>
                <div style={{ display: 'grid', gridTemplateColumns: 'auto 1fr', gap: '5px 10px', fontSize: 12 }}>
                  <Text type="secondary">Action ID</Text><Text code>{actionRecord.action_id}</Text>
                  <Text type="secondary">Hash</Text><Text code>{actionRecord.action_hash}</Text>
                  <Text type="secondary">目标 UID</Text><Text code>{actionRecord.target_uid}</Text>
                  <Text type="secondary">ResourceVersion</Text><Text code>{actionRecord.resource_version}</Text>
                  <Text type="secondary">执行状态</Text><Tag color={actionRecord.execution_status === 'rejected' ? 'red' : 'blue'}>{actionRecord.execution_status}</Tag>
                </div>
              </div>
            )}

            {execError && (
              <div style={{ marginTop: 12, padding: '10px 12px', borderRadius: 8, background: 'var(--danger-soft)', color: 'var(--danger)', fontSize: 12 }}>
                ⚠ {execError}
              </div>
            )}

            {execResult && (
              <div style={{ marginTop: 14, padding: '10px 12px', borderRadius: 8, background: execResult.status === 'rejected' ? 'var(--danger-soft)' : 'var(--success-soft)', border: '1px solid rgba(22,163,74,.2)' }}>
                <div style={{ fontWeight: 600, fontSize: 12, marginBottom: 6, color: execResult.status === 'rejected' ? 'var(--danger)' : 'var(--success)' }}>执行结果：{execResult.status}</div>
                <pre style={{ fontFamily: 'var(--font-mono)', fontSize: 12, whiteSpace: 'pre-wrap', margin: 0, color: 'var(--text)' }}>
                  {execResult.message || '(无附加消息)'}
                </pre>
              </div>
            )}
          </div>
        </Col>
      </Row>

      <Modal
        title="确认执行 K8s 操作"
        open={confirmVisible}
        onCancel={() => setConfirmVisible(false)}
        onOk={() => { setConfirmVisible(false); void doExecute() }}
        okText="确认执行"
        cancelText="取消"
        okButtonProps={{ danger: true, loading: execLoading }}
        destroyOnClose
      >
        <div style={{ marginBottom: 10, color: 'var(--danger)', fontWeight: 600 }}>
          预检已通过，确认要在 {namespace || '集群'} 上执行“{ACTION_LABEL[action] || action}”吗？
        </div>
        <div style={{ fontSize: 12, color: 'var(--text-muted)', marginBottom: 6 }}>
          目标：{kind}/{name}{namespace ? ` · ${namespace}` : ''}
        </div>
        <pre style={{ fontFamily: 'var(--font-mono)', fontSize: 12, whiteSpace: 'pre-wrap', background: 'var(--surface-2)', padding: 10, borderRadius: 6 }}>
          {JSON.stringify({ action_id: actionRecord?.action_id, action_hash: actionRecord?.action_hash, params: actionRecord?.params }, null, 2)}
        </pre>
        <div style={{ marginTop: 8, fontSize: 12, color: 'var(--warning)' }}>该动作已通过 Canonical Action 审批，确认后将进入执行器；执行器当前配置为 disabled。</div>
      </Modal>
    </div>
  )
}

export default K8sActions
