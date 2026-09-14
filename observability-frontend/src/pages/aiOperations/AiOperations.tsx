import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Alert, Button, Descriptions, Input, Select, Tag, Timeline, Tooltip } from 'antd'
import { useSearchParams } from 'react-router-dom'
import {
  chatWithAI,
  createRun,
  getRun,
  listActions,
  listRunEvidences,
  listRuns,
  streamRunEvents,
  type ActionProjection,
  type RunEvidence,
  type RunEvent,
  type RunSummary,
} from '../../api/client'
import BoundedDataRegion from '../../components/display/BoundedDataRegion'
import DataState from '../../components/display/DataState'
import { Empty, PageHeader, PaneCard, StatusBadge, type StatusTone } from '../../components/ui/PageKit'
import { useScopeStore } from '../../store/scopeStore'
import { formatTimeRange } from '../../features/scope/types'
import { resolveResource, type ResolvedResource } from '../../api/resourceResolve'
import {
  ACTION_STAGE_LABELS,
  CONCLUSION_DESCRIPTIONS,
  CONCLUSION_LABELS,
  allowsActionDraft,
  deriveConclusion,
  projectActionStage,
  type ActionStage,
  type ConclusionLevel,
} from './conclusion'

const LEVEL_TONE: Record<ConclusionLevel, StatusTone> = {
  Unknown: 'muted',
  Candidate: 'warn',
  Supported: 'info',
  Confirmed: 'ok',
}

const STAGE_TONE: Partial<Record<ActionStage, StatusTone>> = {
  Draft: 'muted',
  Preflight: 'info',
  AwaitingConfirmation: 'warn',
  Running: 'info',
  Succeeded: 'ok',
  Failed: 'crit',
  Verified: 'ok',
  RolledBack: 'warn',
}

function formatTime(value?: string | null): string {
  if (!value) return '未提供'
  const parsed = Date.parse(value)
  if (!Number.isFinite(parsed)) return value
  return new Date(parsed).toLocaleString('zh-CN', { hour12: false, timeZoneName: 'short' })
}

function relativeTime(value?: string | null): string {
  if (!value) return '无时间'
  const parsed = Date.parse(value)
  if (!Number.isFinite(parsed)) return value
  const diff = Date.now() - parsed
  if (diff < 60_000) return '刚刚'
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)} 分钟前`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)} 小时前`
  return `${Math.floor(diff / 86_400_000)} 天前`
}

function isTerminal(run: RunSummary): boolean {
  const status = (run.status ?? '').toLowerCase()
  return ['success', 'partial', 'failed', 'regressed', 'cancelled', 'completed', 'done'].includes(status)
}

const AiOperations: React.FC = () => {
  const [searchParams, setSearchParams] = useSearchParams()
  const activeClusterId = useScopeStore((s) => s.authScope?.activeClusterId ?? '')
  const tenantId = useScopeStore((s) => s.authScope?.tenantId ?? '')
  const timeRange = useScopeStore((s) => s.active.timeRange)

  const [runs, setRuns] = useState<RunSummary[]>([])
  const [runsState, setRunsState] = useState<'loading' | 'ready' | 'empty' | 'error' | 'forbidden'>('loading')
  const [runsError, setRunsError] = useState<unknown>()
  const [selectedRunId, setSelectedRunId] = useState<string>(searchParams.get('runId') ?? '')
  const [run, setRun] = useState<RunSummary | null>(null)
  const [evidences, setEvidences] = useState<RunEvidence[]>([])
  const [actions, setActions] = useState<ActionProjection[]>([])
  const [liveEvents, setLiveEvents] = useState<RunEvent[]>([])
  const [question, setQuestion] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [answer, setAnswer] = useState<string>('')
  const [answering, setAnswering] = useState(false)
  const [locate, setLocate] = useState({ type: searchParams.get('resourceType') ?? '', namespace: searchParams.get('resourceNamespace') ?? '', name: searchParams.get('resourceName') ?? '' })
  const [located, setLocated] = useState<ResolvedResource | null>(null)
  const [locateState, setLocateState] = useState<'idle' | 'loading' | 'ready' | 'error' | 'forbidden'>('idle')
  const [locateError, setLocateError] = useState<string>('')
  const streamAbort = useRef<AbortController | null>(null)

  const loadRuns = useCallback(() => {
    setRunsState('loading')
    listRuns({ limit: 50 })
      .then((res) => {
        const list = res.data?.runs ?? []
        setRuns(list)
        setRunsState(list.length === 0 ? 'empty' : 'ready')
        if (!selectedRunId && list.length > 0) setSelectedRunId(list[0].run_id)
      })
      .catch((error) => {
        setRunsError(error)
        setRunsState(error?.response?.status === 403 ? 'forbidden' : 'error')
      })
  }, [selectedRunId])

  useEffect(() => {
    loadRuns()
  }, [loadRuns])

  const loadDetail = useCallback(
    (runId: string) => {
      if (!runId) {
        setRun(null)
        setEvidences([])
        setActions([])
        return
      }
      getRun(runId)
        .then((res) => setRun(res.data?.run ?? null))
        .catch(() => setRun(null))
      if (tenantId && activeClusterId) {
        listRunEvidences(runId, { tenant_id: tenantId, cluster_id: activeClusterId })
          .then((res) => setEvidences(res.data?.evidences ?? []))
          .catch(() => setEvidences([]))
      }
      listActions({ limit: 50 })
        .then((res) => setActions((res.data?.actions ?? []).filter((a) => !a.run_id || a.run_id === runId)))
        .catch(() => setActions([]))
    },
    [activeClusterId, tenantId],
  )

  useEffect(() => {
    loadDetail(selectedRunId)
    if (selectedRunId) setSearchParams({ runId: selectedRunId }, { replace: true })
  }, [selectedRunId, loadDetail, setSearchParams])

  // 实时进度：SSE 增量，断线由 streamRunEvents 内部按 Last-Event-ID 续传
  useEffect(() => {
    if (!selectedRunId) return
    const controller = new AbortController()
    streamAbort.current?.abort()
    streamAbort.current = controller
    setLiveEvents([])
    streamRunEvents(selectedRunId, (event) => setLiveEvents((prev) => [...prev, event].slice(-50)), controller.signal, {
      maxReconnects: 3,
    }).catch(() => undefined)
    return () => controller.abort()
  }, [selectedRunId])

  const conclusion = useMemo(() => {
    if (!run) return null
    const quality = `${run.partial ? 'partial' : ''}${run.stale ? ' stale' : ''}`.trim()
    return deriveConclusion({
      rootCause: run.root_cause,
      confidence: run.confidence,
      evidenceCount: run.evidence_count ?? evidences.length,
      hasUnavailableSource: Boolean(quality.includes('partial')),
      partial: Boolean(run.partial),
      stale: Boolean(run.stale),
      hasUnresolvedContradiction: false,
      hasDirectEvidence: (run.evidence_count ?? evidences.length) > 0,
      terminal: isTerminal(run),
    })
  }, [run, evidences.length])

  const handleSubmit = async () => {
    const text = question.trim()
    if (!text) return
    setSubmitting(true)
    setSubmitError(null)
    try {
      const res = await createRun({
        intent: text,
        cluster_id: activeClusterId || undefined,
        target_type: searchParams.get('resourceType') ?? undefined,
        target_resource_id: searchParams.get('resourceUid') ?? undefined,
      })
      const runId = res.data?.run_id
      if (!runId) throw new Error('服务端未返回 run_id')
      setQuestion('')
      setSelectedRunId(runId)
      loadRuns()
    } catch (error: any) {
      setSubmitError(error?.response?.data?.message || error?.message || '创建运维任务失败')
    } finally {
      setSubmitting(false)
    }
  }

  const handleAsk = async () => {
    const text = question.trim()
    if (!text) return
    setAnswering(true)
    setAnswer('')
    try {
      const res = await chatWithAI({ message: text, cluster_id: activeClusterId || undefined, run_id: selectedRunId || undefined })
      const data: any = res.data
      setAnswer(typeof data === 'string' ? data : data?.answer ?? data?.content ?? JSON.stringify(data))
    } catch (error: any) {
      setAnswer('')
      setSubmitError(error?.response?.data?.message || error?.message || 'AI 应答失败')
    } finally {
      setAnswering(false)
    }
  }

  // 资源定位：对象链接必须使用 canonical UID；UID 只能由服务端权威解析产出
  const handleLocate = () => {
    if (!activeClusterId || !locate.type || !locate.name) return
    setLocateState('loading')
    setLocateError('')
    setLocated(null)
    resolveResource({ clusterId: activeClusterId, resourceType: locate.type, namespace: locate.namespace, name: locate.name })
      .then((res) => {
        setLocated(res)
        setLocateState('ready')
      })
      .catch((error) => {
        setLocateState(error?.response?.status === 403 ? 'forbidden' : 'error')
        setLocateError(error?.response?.data?.error || error?.message || '定位失败')
      })
  }

  const frozenWindow = formatTimeRange(timeRange)

  return (
    <div className="ai-operations-page" data-testid="ai-operations-page">
      <PageHeader
        title="AI 智能运维"
        desc="范围冻结 → 证据 → 候选原因 → 反证 → 结论等级 → 动作草稿 → 预检 → 显式确认 → 执行 → 恢复验证 → 报告"
        actions={<Button onClick={loadRuns}>刷新任务</Button>}
      />

      <div className="ai-ops-context" data-testid="frozen-context">
        <Descriptions size="small" column={{ xs: 1, md: 2, lg: 4 }} bordered>
          <Descriptions.Item label="冻结范围">
            {activeClusterId ? <code>{activeClusterId}</code> : <Tag color="warning">未选择集群</Tag>}
          </Descriptions.Item>
          <Descriptions.Item label="租户">{tenantId ? <code>{tenantId}</code> : '未提供'}</Descriptions.Item>
          <Descriptions.Item label="时间窗">{frozenWindow}</Descriptions.Item>
          <Descriptions.Item label="任务 / Run">
            {selectedRunId ? <code>{selectedRunId}</code> : '尚未创建任务'}
          </Descriptions.Item>
        </Descriptions>
      </div>

      <div className="ai-ops-grid">
        {/* 左栏：任务与阶段 */}
        <div className="ai-ops-col">
          <PaneCard title="运维任务" action={<span className="muted-sm">{runs.length} 个</span>}>
            <BoundedDataRegion state={runsState} error={runsError} onRetry={loadRuns}>
              {runs.length === 0 ? (
                <Empty text="还没有运维任务" hint="在下方输入问题并发起调查后，任务会出现在这里" />
              ) : (
                <ul className="ai-ops-run-list">
                  {runs.map((item) => (
                    <li key={item.run_id}>
                      <button
                        type="button"
                        className={item.run_id === selectedRunId ? 'is-active' : ''}
                        onClick={() => setSelectedRunId(item.run_id)}
                      >
                        <strong>{item.intent || item.target_resource_id || item.run_id}</strong>
                        <span>
                          {item.status ?? 'unknown'} · {relativeTime(item.created_at ?? undefined)}
                        </span>
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </BoundedDataRegion>
          </PaneCard>

          <PaneCard title="任务阶段">
            {!selectedRunId ? (
              <Empty text="未选择任务" />
            ) : (
              <Timeline
                items={[
                  { children: `范围冻结 · ${formatTime(run?.created_at ?? undefined)}` },
                  {
                    children: `证据采集 · 已入库 ${run?.evidence_count ?? evidences.length} 条`,
                  },
                  { children: `候选原因 · ${run?.hypotheses?.length ?? 0} 个` },
                  { children: `结论 · ${conclusion ? CONCLUSION_LABELS[conclusion.level] : '未形成'}` },
                  {
                    children: `动作 · ${(run?.actions ?? actions).length} 项`,
                  },
                  {
                    children: `恢复验证 · ${run?.latest_verification?.status ?? '尚未验证'}`,
                    color: run?.latest_verification ? 'green' : 'gray',
                  },
                ]}
              />
            )}
          </PaneCard>
        </div>

        {/* 中栏：提问与证据 */}
        <div className="ai-ops-col ai-ops-col--main">
          <PaneCard title="问题与上下文">
            <Input.TextArea
              value={question}
              onChange={(e) => setQuestion(e.target.value)}
              placeholder="描述现象或提出问题，例如：checkout 服务 5 分钟内错误率上升的原因是什么？"
              autoSize={{ minRows: 3, maxRows: 6 }}
              aria-label="运维问题输入"
            />
            {submitError && <Alert type="error" showIcon message={submitError} style={{ marginTop: 8 }} />}
            <div className="ai-ops-actions">
              <Button type="primary" loading={submitting} disabled={!question.trim() || !activeClusterId} onClick={handleSubmit}>
                发起受控调查
              </Button>
              <Tooltip title="仅生成解释，不创建调查运行，不产生任何基础设施副作用">
                <Button loading={answering} disabled={!question.trim()} onClick={handleAsk}>
                  仅咨询（只读）
                </Button>
              </Tooltip>
              {!activeClusterId && <span className="muted-sm">需先在顶栏选择集群</span>}
            </div>
          </PaneCard>

          <PaneCard title="资源定位（canonical UID）" action={<span className="muted-sm">由服务端权威解析，不在前端拼接</span>}>
            <div className="ai-ops-locate">
              <Input value={locate.type} onChange={(e) => setLocate((s) => ({ ...s, type: e.target.value }))} placeholder="类型，如 pod / deployment" aria-label="资源类型" />
              <Input value={locate.namespace} onChange={(e) => setLocate((s) => ({ ...s, namespace: e.target.value }))} placeholder="命名空间（集群级对象可留空）" aria-label="命名空间" />
              <Input value={locate.name} onChange={(e) => setLocate((s) => ({ ...s, name: e.target.value }))} placeholder="对象名" aria-label="对象名" />
              <Button loading={locateState === 'loading'} disabled={!activeClusterId || !locate.type || !locate.name} onClick={handleLocate}>
                解析 canonical UID
              </Button>
            </div>
            {locateState === 'forbidden' && <Alert type="error" showIcon message="无权定位该对象" style={{ marginTop: 8 }} />}
            {locateState === 'error' && <Alert type="error" showIcon message="定位失败" description={locateError} style={{ marginTop: 8 }} />}
            {locateState === 'ready' && located && (
              <div className="ai-ops-locate__result" style={{ marginTop: 8 }}>
                <span className="muted-sm">canonical UID</span>
                <code data-testid="canonical-uid">{located.uid}</code>
                <span className="muted-sm">{located.resourceType} · {located.namespace || '集群级'} · {located.name}{located.health ? ` · 健康 ${located.health}` : ''}</span>
              </div>
            )}
          </PaneCard>

          {answer && (
            <PaneCard title="只读咨询应答">
              <pre className="ai-ops-answer">{answer}</pre>
            </PaneCard>
          )}

          <PaneCard
            title="实时进度与证据"
            action={<span className="muted-sm">{liveEvents.length > 0 ? `${liveEvents.length} 条增量事件` : '无增量事件'}</span>}
          >
            {liveEvents.length > 0 && (
              <ul className="ai-ops-events" aria-label="任务实时事件">
                {liveEvents.slice(-10).map((event, index) => (
                  <li key={`${event.sequence ?? index}-${event.event_type ?? 'evt'}`}>
                    <code>#{event.sequence ?? index + 1}</code> {event.event_type ?? 'event'}
                  </li>
                ))}
              </ul>
            )}
            {evidences.length === 0 ? (
              <DataState kind="empty" title="暂无可打开证据" description="任务尚未采集到证据，或证据尚未通过校验入库" />
            ) : (
              <table className="data-table" aria-label="运行证据">
                <thead>
                  <tr>
                    <th scope="col">evidence_id</th>
                    <th scope="col">类型</th>
                    <th scope="col">来源</th>
                    <th scope="col">观察时间</th>
                    <th scope="col">质量</th>
                    <th scope="col">事实摘要</th>
                  </tr>
                </thead>
                <tbody>
                  {evidences.map((item) => {
                    const id = item.evidence_id ?? item.id
                    const type = item.evidence_type ?? item.type
                    const source = item.source_ref ?? item.source
                    const summary = item.summary ?? item.fact
                    return (
                      <tr key={id}>
                        <td><code>{id}</code></td>
                        <td>{type}</td>
                        <td>{source}</td>
                        <td>{formatTime(item.observed_at ?? item.collected_at)}</td>
                        <td>{item.quality ?? item.source_reliability ?? '未提供'}</td>
                        <td className="cell-wrap">{summary}</td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            )}
          </PaneCard>
        </div>

        {/* 右栏：结论、候选原因与动作 */}
        <div className="ai-ops-col">
          <PaneCard title="结论等级">
            {!conclusion ? (
              <Empty text="尚未选择任务" />
            ) : (
              <div className="ai-ops-conclusion">
                <StatusBadge text={CONCLUSION_LABELS[conclusion.level]} tone={LEVEL_TONE[conclusion.level]} />
                <p>{CONCLUSION_DESCRIPTIONS[conclusion.level]}</p>
                <dl>
                  <dt>根因</dt>
                  <dd>{run?.root_cause || '未定位'}</dd>
                  <dt>置信度</dt>
                  <dd>{typeof run?.confidence === 'number' ? run.confidence.toFixed(2) : '服务端未提供'}</dd>
                </dl>
                {conclusion.downgradeReasons.length > 0 && (
                  <Alert
                    type="warning"
                    showIcon
                    message="结论被降级，原因如下"
                    description={<ul>{conclusion.downgradeReasons.map((r) => <li key={r}>{r}</li>)}</ul>}
                  />
                )}
                {conclusion.missing.length > 0 && (
                  <div className="ai-ops-missing">
                    <strong>还缺什么</strong>
                    <ul>{conclusion.missing.map((m) => <li key={m}>{m}</li>)}</ul>
                  </div>
                )}
              </div>
            )}
          </PaneCard>

          <PaneCard title="候选原因与反证" action={<span className="muted-sm">{run?.hypotheses?.length ?? 0} 个候选</span>}>
            {(run?.hypotheses ?? []).length === 0 ? (
              <Empty text="尚无候选原因" hint="候选原因由调查过程生成，必须附支持证据与反证" />
            ) : (
              <ul className="ai-ops-hypotheses">
                {(run?.hypotheses ?? []).map((h) => (
                  <li key={h.hypothesis_id}>
                    <strong>{h.content}</strong>
                    <span>
                      置信度 {typeof h.confidence === 'number' ? h.confidence.toFixed(2) : '未提供'} ·{' '}
                      {h.confirmed_by_evidence ? '有直接证据' : '缺直接证据'}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </PaneCard>

          <PaneCard title="动作草稿与执行" action={<span className="muted-sm">{(run?.actions ?? actions).length} 项</span>}>
            {(() => {
              const list = (run?.actions ?? actions) as Array<Record<string, any>>
              if (list.length === 0) {
                return <Empty text="暂无动作" hint={conclusion && allowsActionDraft(conclusion.level) ? '可基于当前结论生成动作草稿' : '结论等级不足以支撑动作建议'} />
              }
              return (
                <ul className="ai-ops-actions-list">
                  {list.map((action) => {
                    const stage = projectActionStage(action)
                    return (
                      <li key={action.action_id}>
                        <div>
                          <strong>{action.action_type ?? action.operation ?? '动作'}</strong>
                          <span>
                            {action.target_name ?? action.target_uid ?? '未提供目标'} · 风险{' '}
                            {action.authoritative_risk ?? action.risk_level ?? '未评估'}
                          </span>
                        </div>
                        <StatusBadge text={ACTION_STAGE_LABELS[stage]} tone={STAGE_TONE[stage] ?? 'muted'} />
                      </li>
                    )
                  })}
                </ul>
              )
            })()}
            <Alert
              type="info"
              showIcon
              style={{ marginTop: 8 }}
              message="AI 不会自动批准或执行"
              description="所有执行必须经过服务端预检、对象版本复核与人工显式确认；确认入口位于来源任务的处置阶段。"
            />
          </PaneCard>

          <PaneCard title="AI 运维报告">
            <Button
              disabled={!run || !isTerminal(run)}
              onClick={() => window.open(`/reports?type=ai-operations&runId=${encodeURIComponent(selectedRunId)}`, '_self')}
            >
              由本任务生成报告
            </Button>
            {run && !isTerminal(run) && <p className="muted-sm">任务仍在进行中；报告仅从已完成或明确终止的任务生成。</p>}
          </PaneCard>
        </div>
      </div>
    </div>
  )
}

export default AiOperations
