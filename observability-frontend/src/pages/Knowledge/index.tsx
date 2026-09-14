import React, { useEffect, useMemo, useState } from 'react'
import { Alert, Button, Drawer, Input, Space, Tabs, message } from 'antd'
import { useLocation, useNavigate, useParams } from 'react-router-dom'
import { createKnowledge, disableKnowledge, getKnowledge, getKnowledgeIndexStatus, listKnowledge, reviewKnowledge, searchKnowledge, submitKnowledge, type KnowledgeInput, type KnowledgeItem, type KnowledgeVersion } from '../../api/knowledge'
import KnowledgeEditor from './KnowledgeEditor'
import KnowledgeInspector, { type KnowledgeSourceHealth } from './KnowledgeInspector'
import KnowledgeList from './KnowledgeList'
import { PageHeader, PaneCard } from '../../components/ui/PageKit'
import { useScopeStore } from '../../store/scopeStore'

const defaultDraft = (clusterId: string): KnowledgeInput => ({ scope_type: 'cluster', cluster_id: clusterId, knowledge_type: 'incident', title: '', summary: '', content: '', source_kind: 'manual', status: 'draft' })

export default function Knowledge() {
  // 回归（真实环境验证发现的 S1 缺陷 D18）：/knowledge 顶级路由没有 clusterId
  // 路径参数，useParams 恒为 undefined → 页面永远"当前集群：未选择"且列表请求
  // 不发起。集群级 canonical 路由（/clusters/:clusterUid/knowledge）仍以路径
  // 参数为准；顶级入口回退到服务端确认的活动集群作用域。
  const pathClusterId = useParams<{ clusterId: string }>().clusterId
  const activeClusterId = useScopeStore((s) => s.authScope?.activeClusterId ?? '')
  const clusterId = pathClusterId || activeClusterId
  const navigate = useNavigate()
  const location = useLocation()
  const isNewRoute = location.pathname.endsWith('/knowledge/new')
  const capabilities = useScopeStore((state) => state.capabilities)
  const canWrite = capabilities.includes('knowledge.write')
  const [items, setItems] = useState<KnowledgeItem[]>([])
  const [selected, setSelected] = useState<KnowledgeItem>()
  const [version, setVersion] = useState<KnowledgeVersion>()
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [indexAvailable, setIndexAvailable] = useState(true)
  const [activeType, setActiveType] = useState('incident')
  const [searchText, setSearchText] = useState('')
  const [searching, setSearching] = useState(false)
  const [searchResults, setSearchResults] = useState<Array<Record<string, unknown>>>([])
  const [draft, setDraft] = useState<KnowledgeInput>(() => defaultDraft(clusterId))
  const [saving, setSaving] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [listOpen, setListOpen] = useState(false)
  const [inspectorOpen, setInspectorOpen] = useState(false)
  const [sourceHealth, setSourceHealth] = useState<KnowledgeSourceHealth[]>([])
  const [disableVerification, setDisableVerification] = useState<{ state: 'idle' | 'running' | 'passed' | 'failed'; detail: string }>({ state: 'idle', detail: '' })

  useEffect(() => {
    setDraft(defaultDraft(clusterId))
    if (!clusterId) return
    setLoading(true); setError('')
    void Promise.all([listKnowledge(clusterId), getKnowledgeIndexStatus(clusterId)]).then(([list, index]) => {
      setItems(list.items || [])
      setIndexAvailable(list.meta?.index_available !== false && index.meta?.index_available !== false)
      setSourceHealth((index.items || []) as unknown as KnowledgeSourceHealth[])
      setSelected((current) => isNewRoute ? undefined : current || list.items?.[0])
    }).catch((reason) => setError(reason?.response?.data?.error || reason?.message || '知识列表加载失败')).finally(() => setLoading(false))
  }, [clusterId, isNewRoute])

  useEffect(() => {
    if (!clusterId || !selected?.knowledge_id) return
    let active = true
    setVersion(undefined)
    void getKnowledge(clusterId, selected.knowledge_id).then((result) => {
      if (!active) return
      setSelected(result.data)
      setVersion(result.version)
    }).catch(() => {
      if (active) setError('知识正文加载失败')
    })
    return () => { active = false }
  }, [clusterId, selected?.knowledge_id])

  const visibleItems = useMemo(() => items.filter((item) => item.knowledge_type === activeType), [activeType, items])

  const selectItem = (item: KnowledgeItem) => {
    setSelected(item); setInspectorOpen(true)
  }

  const runSearch = async () => {
    if (!searchText.trim()) return
    setSearching(true)
    try {
      const result = await searchKnowledge(clusterId, { query: searchText, topK: 8 })
      setSearchResults(result.items || []); setIndexAvailable(result.meta?.index_available !== false)
    } catch {
      setSearchResults([]); setIndexAvailable(false)
    } finally { setSearching(false) }
  }

  const saveDraft = async () => {
    if (!clusterId) return
    setSaving(true)
    try {
      const result = await createKnowledge(clusterId, { ...draft, title: draft.title.trim() || '未命名运维知识', status: 'draft' })
      if (result.data) {
        const saved = { ...result.data, status: result.data.status || 'draft' }
        setItems((current) => [saved, ...current])
        setSelected(saved)
      }
      message.success('草稿已保存')
    } catch (reason: any) {
      message.error(reason?.response?.data?.error || '草稿保存失败')
    } finally { setSaving(false) }
  }

  const submitDraft = async () => {
    if (!clusterId || !selected?.knowledge_id || selected.status !== 'draft') return
    setSubmitting(true)
    try {
      await submitKnowledge(clusterId, selected.knowledge_id)
      setSelected((current) => current ? { ...current, status: 'pending_review' } : current)
      setItems((current) => current.map((item) => item.knowledge_id === selected.knowledge_id ? { ...item, status: 'pending_review' } : item))
      message.success('已提交审核')
    } catch (reason: any) {
      message.error(reason?.response?.data?.error || '提交审核失败')
    } finally { setSubmitting(false) }
  }

  // 审阅是发布门禁：批准进入已发布，拒绝必须留痕
  const handleReview = async (decision: 'approve' | 'reject', reason: string) => {
    if (!clusterId || !selected?.knowledge_id) return
    await reviewKnowledge(clusterId, selected.knowledge_id, decision, reason)
    const nextStatus = decision === 'approve' ? 'published' : 'draft'
    setSelected((current) => (current ? { ...current, status: nextStatus } : current))
    setItems((current) => current.map((item) => (item.knowledge_id === selected.knowledge_id ? { ...item, status: nextStatus } : item)))
    message.success(decision === 'approve' ? '已批准并发布' : '已拒绝并退回草稿')
  }

  const handleDisable = async () => {
    if (!clusterId || !selected?.knowledge_id) return
    await disableKnowledge(clusterId, selected.knowledge_id)
    setSelected((current) => (current ? { ...current, status: 'disabled' } : current))
    setItems((current) => current.map((item) => (item.knowledge_id === selected.knowledge_id ? { ...item, status: 'disabled' } : item)))
    message.success('已禁用；请执行删除验证')
  }

  // 删除验证：禁用后用原文标题检索，索引/向量库必须不再命中（§6.6）
  const handleVerifyRemoval = async () => {
    if (!clusterId || !selected) return
    setDisableVerification({ state: 'running', detail: '正在用原文标题与关键词检索索引与向量库' })
    try {
      const [byTitle, byKeyword] = await Promise.all([
        searchKnowledge(clusterId, { query: selected.title, topK: 8 }),
        searchKnowledge(clusterId, { query: selected.summary || selected.knowledge_id, topK: 8 }),
      ])
      const hit = [...byTitle.items, ...byKeyword.items].find((hit) => String(hit.knowledge_id ?? '') === selected.knowledge_id)
      if (hit) {
        setDisableVerification({ state: 'failed', detail: `检索仍能命中该知识（来源 ${String(hit.source ?? hit.knowledge_id ?? '未知')}）；索引或向量库尚未同步，删除验证未通过。` })
        return
      }
      setDisableVerification({ state: 'passed', detail: '标题与关键词检索均未命中该知识；索引与向量库已同步。' })
    } catch (reason: any) {
      setDisableVerification({ state: 'failed', detail: reason?.response?.data?.error || '检索不可用，无法完成删除验证；这不能被当作通过。' })
    }
  }

  const knowledgeEmptyLabels: Record<string, string> = { incident: '故障案例', document: '运维文档', playbook: '内置 Playbook' }
  // 空态必须类型化并提供权限允许的主动作；不渲染整块无信息留白。
  const knowledgeEmptyPanel = (
    <div className="knowledge-empty-state">
      <strong>当前类型暂无{knowledgeEmptyLabels[activeType] || '知识'}</strong>
      <p>当前集群还没有{knowledgeEmptyLabels[activeType] || '知识'}，可先新增草稿或切换类型查看。</p>
      {canWrite && <Button type="primary" onClick={() => { setDraft(defaultDraft(clusterId)); navigate(clusterId ? `/clusters/${encodeURIComponent(clusterId)}/knowledge/new` : '/clusters') }}>新增知识</Button>}
    </div>
  )
  const listPanel = visibleItems.length === 0 && !loading && !error
    ? knowledgeEmptyPanel
    : <KnowledgeList items={visibleItems} selectedId={selected?.knowledge_id} loading={loading} onSelect={selectItem} />
  const inspectorPanel = (
    <KnowledgeInspector
      item={selected}
      version={version}
      indexAvailable={indexAvailable}
      canReview={canWrite}
      onReview={handleReview}
      onDisable={handleDisable}
      disableVerification={disableVerification}
      onVerifyRemoval={handleVerifyRemoval}
      sourceHealth={sourceHealth}
    />
  )

  return <div className="knowledge-workspace" data-testid="knowledge-workspace">
    <PageHeader title="运维知识" desc={`当前集群：${clusterId || '未选择'}`} actions={<Space><Button onClick={() => setListOpen(true)} className="knowledge-mobile-button">知识列表</Button><Button onClick={() => setInspectorOpen(true)} className="knowledge-mobile-button">查看正文</Button>{canWrite && <Button type="primary" onClick={() => setDraft(defaultDraft(clusterId))}>新增知识</Button>}</Space>} />
    <PaneCard title="检索与索引" className="knowledge-search-card">
      <div className="knowledge-search-row"><Input.Search aria-label="知识检索" placeholder="检索故障案例、运维文档和 Playbook" value={searchText} loading={searching} onChange={(event) => setSearchText(event.target.value)} onSearch={runSearch} enterButton="检索" />{!indexAvailable && <Alert type="warning" showIcon message="语义检索暂不可用，正文仍可浏览" />}</div>
      {searchResults.length > 0 && <div className="knowledge-search-results">{searchResults.map((result, index) => <button type="button" key={`${String(result.knowledge_id || result.document_id || index)}`} onClick={() => setSelected(items.find((item) => item.knowledge_id === result.knowledge_id) || selected)}>{String(result.title || result.document_id || '检索结果')}</button>)}</div>}
    </PaneCard>
    {error && <Alert type="error" showIcon message={error} style={{ marginTop: 16 }} />}
    <Tabs className="knowledge-tabs" activeKey={activeType} onChange={setActiveType} items={[{ key: 'incident', label: '故障案例' }, { key: 'document', label: '运维文档' }, { key: 'playbook', label: '内置 Playbook' }]} />
    <div className="knowledge-workspace__columns">
      <PaneCard className="knowledge-workspace__list-pane">{listPanel}</PaneCard>
      <PaneCard className="knowledge-workspace__inspector-pane">{inspectorPanel}</PaneCard>
      <PaneCard className="knowledge-workspace__editor-pane"><KnowledgeEditor value={draft} readOnly={!canWrite || activeType === 'playbook'} allowPlatformCommon={canWrite} currentStatus={selected?.status} saving={saving} submitting={submitting} onChange={setDraft} onSave={saveDraft} onSubmit={submitDraft} /></PaneCard>
    </div>
    <Drawer open={listOpen} title="知识列表" onClose={() => setListOpen(false)}>{listOpen ? listPanel : null}</Drawer>
    <Drawer open={inspectorOpen} title="知识正文" onClose={() => setInspectorOpen(false)}>{inspectorOpen ? inspectorPanel : null}</Drawer>
  </div>
}
