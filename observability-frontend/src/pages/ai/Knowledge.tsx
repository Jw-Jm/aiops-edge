import React, { useEffect, useState } from 'react'
import { Table, Button, Modal, Form, Input, Select, Tag, Space, Tabs, Menu, Spin, message, Popconfirm } from 'antd'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { listKnowledge, addKnowledge, deleteKnowledge, getRagStats, reloadRagKnowledge, KnowledgeItem, RagStats } from '../../api/client'
import { getPlaybooks, getPlaybookContent, PlaybookEntry, PlaybookDoc } from '../../api/playbooks'
import { PageHeader, Breadcrumb, Empty } from '../../components/ui/PageKit'

const Knowledge: React.FC = () => {
  const [data, setData] = useState<KnowledgeItem[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [open, setOpen] = useState(false)
  const [q, setQ] = useState('')
  // 知识库统一为单一类型「故障案例」，列表固定只请求 type=case
  const [rag, setRag] = useState<RagStats | null>(null)
  const [reloadLoading, setReloadLoading] = useState(false)
  const [form] = Form.useForm()

  const load = (page = 1, size = 50) => {
    setLoading(true)
    listKnowledge({ page, size, type: 'case', ...(q ? { q } : {}) })
      .then((r) => {
        const d = r.data
        setData(Array.isArray(d) ? d : d?.items || [])
        setTotal(d?.total ?? (Array.isArray(d) ? d.length : 0))
      })
      .catch(() => { setData([]); setTotal(0) })
      .finally(() => setLoading(false))
  }
  useEffect(() => { load() }, [])

  const loadRag = () => {
    getRagStats().then((r) => setRag(r.data)).catch(() => setRag(null))
  }
  useEffect(() => { loadRag() }, [])

  const submit = async () => {
    const v = await form.validateFields()
    addKnowledge({
      title: v.title,
      content: v.content,
      source: v.source || 'manual',
      tags: v.tags || '',
    })
      .then(() => { message.success('已新增'); setOpen(false); form.resetFields(); load(); })
      .catch((e) => message.error(e?.response?.data?.error || '新增失败'))
  }

  const onReload = () => {
    setReloadLoading(true)
    reloadRagKnowledge()
      .then((r) => {
        const d = r.data || {}
        message.success(`已导入内置案例：新增 ${d.added ?? 0} 条（去重 ${d.dup ?? 0}）`)
        loadRag()
      })
      .catch(() => message.error('导入失败'))
      .finally(() => setReloadLoading(false))
  }

  const cols = [
    { title: 'ID', dataIndex: 'id', key: 'id', width: 64 },
    {
      title: '标题', dataIndex: 'title', key: 'title', width: 200,
      render: (v: string, r: KnowledgeItem) => <span style={{ fontWeight: 500 }}>{v || (r.content || '').split('\n')[0].slice(0, 80) || r.id}</span>,
    },
    {
      title: '标签', dataIndex: 'tags', key: 'tags', width: 170,
      render: (v: string) => v ? v.split(',').filter(Boolean).map((t) => <Tag key={t} style={{ marginBottom: 2 }}>{t.trim()}</Tag>) : '-',
    },
    { title: '来源', dataIndex: 'source', key: 'source', width: 90, render: (v: string) => (v === 'manual' ? <Tag color="blue">手动录入</Tag> : <Tag>{v}</Tag>) },
    {
      title: '内容/根因', key: 'content', render: (_: unknown, r: KnowledgeItem) => (
        <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>
          {(r.root_cause || r.content || '').slice(0, 110)}{((r.root_cause || r.content || '').length > 110) ? '…' : ''}
        </span>
      ),
    },
    { title: '方案', dataIndex: 'plan', key: 'plan', render: (v: string) => <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>{(v || '-').slice(0, 60)}</span> },
    {
      title: '验证状态', dataIndex: 'validated', key: 'validated', width: 100,
      render: (v: string) => (
        v === 'approved' || v === 'verified' || v === 'true'
          ? <Tag color="green">已验证</Tag>
          : v === 'pending'
            ? <Tag color="orange">待审核</Tag>
            : <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>-</span>
      ),
    },
    {
      title: '创建时间', dataIndex: 'created_at', key: 'created_at', width: 160,
      render: (v: string) => <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>
        {v ? new Date(Number(v) * 1000).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : '-'}
      </span>,
    },
    {
      title: '操作', key: 'act', width: 90,
      render: (_: unknown, r: KnowledgeItem) => (
        <Popconfirm title="确认删除该故障案例？" onConfirm={() => {
          deleteKnowledge(r.id).then(() => { message.success('已删除'); load() }).catch(() => message.error('删除失败'))
        }}>
          <Button size="small" type="link" danger>删除</Button>
        </Popconfirm>
      ),
    },
  ]

  return (
    <div>
      <Breadcrumb items={[{ t: '智能运维' }, { t: '知识库' }]} />
      <PageHeader title="知识库" desc="RAG 故障案例库 + 内置运维 Playbook，供 AI 诊断检索与团队沉淀"
        actions={
          <Space>
            <Button onClick={() => { setQ(''); load() }}>刷新</Button>
            <Button type="primary" onClick={() => { form.resetFields(); setOpen(true) }}>新增案例</Button>
          </Space>
        } />

      <Tabs
        items={[
          {
            key: 'cases', label: '故障案例',
            children: (
              <>
                {/* 统计卡：故障案例 + 统一总数（知识文档已归档，不再展示） */}
      <div style={{ display: 'flex', gap: 16, marginBottom: 16, flexWrap: 'wrap' }}>
        <div className="card" style={{ flex: 1, minWidth: 200, marginBottom: 0, padding: 16 }}>
          <div style={{ fontSize: 13, color: 'var(--text-muted)' }}>故障案例（AI 检索）</div>
          <div style={{ fontSize: 30, fontWeight: 700, marginTop: 4 }}>{rag?.cases ?? 0}</div>
        </div>
        <div className="card" style={{ flex: 1, minWidth: 200, marginBottom: 0, padding: 16 }}>
          <div style={{ fontSize: 13, color: 'var(--text-muted)' }}>统一知识库总数（ChromaDB）</div>
          <div style={{ fontSize: 30, fontWeight: 700, marginTop: 4 }}>{rag?.total ?? 0}</div>
        </div>
        <div className="card" style={{ flex: 2, minWidth: 320, marginBottom: 0, padding: 16, display: 'flex', alignItems: 'center', gap: 12, justifyContent: 'space-between' }}>
          <div>
            <div style={{ fontSize: 13, color: 'var(--text-muted)' }}>内置故障案例库</div>
            <div style={{ fontSize: 12, marginTop: 4 }}>从 data/knowledge_cases.json 导入典型案例（症状/根因/方案/结果），AI 诊断时自动检索相似案例</div>
          </div>
          <Button loading={reloadLoading} onClick={onReload}>导入内置案例</Button>
        </div>
      </div>

      {/* 搜索 + 案例列表 */}
      <div className="card" style={{ padding: 0 }}>
        <div style={{ padding: '0 12px 12px', display: 'flex', gap: 8 }}>
          <Input
            placeholder="搜索标题 / 内容 / 标签"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onPressEnter={() => load()}
            allowClear
            style={{ maxWidth: 320 }}
          />
          <Button onClick={() => load()}>搜索</Button>
        </div>
        <Table rowKey="id" loading={loading} columns={cols} dataSource={data} size="middle"
          pagination={{ total, pageSize: 50, showTotal: (t) => `共 ${t} 条` }}
          locale={{ emptyText: <Empty text={'暂无故障案例，点击右上角「新增案例」录入，或「导入内置案例」'} /> }} />
              </div>
              </>
            ),
          },
          {
            key: 'playbooks', label: '运维 Playbook',
            children: <PlaybookTab />,
          },
        ]}
      />

      <Modal title="新增故障案例" open={open} onOk={submit} onCancel={() => setOpen(false)} destroyOnClose width={620}>
        <Form form={form} layout="vertical" style={{ marginTop: 12 }}>
          <Form.Item name="title" label="标题" rules={[{ required: true }]}><Input placeholder="如：ClickHouse 连接池耗尽排查" /></Form.Item>
          <Form.Item name="content" label="内容" rules={[{ required: true }]}><Input.TextArea rows={5} placeholder="现象 / 根因 / 处置步骤" /></Form.Item>
          <Form.Item name="tags" label="标签（逗号分隔）"><Input placeholder="如：clickhouse,数据库,连接池" /></Form.Item>
          <Form.Item name="source" label="来源" initialValue="manual">
            <Select options={[{ value: 'manual', label: '手动录入' }, { value: 'incident', label: '故障复盘' }, { value: 'docs', label: '文档沉淀' }]} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}

// ===== 运维 Playbook Tab：分类浏览 + 向量检索 + 原文渲染（Task E4）=====
const PlaybookTab: React.FC = () => {
  const [all, setAll] = useState<PlaybookEntry[]>([])
  const [listLoading, setListLoading] = useState(true)
  const [cats, setCats] = useState<{ name: string; count: number }[]>([])
  const [activeCat, setActiveCat] = useState('')
  const [q, setQ] = useState('')
  const [searching, setSearching] = useState(false)
  const [hits, setHits] = useState<PlaybookEntry[] | null>(null)
  const [doc, setDoc] = useState<PlaybookDoc | null>(null)
  const [docLoading, setDocLoading] = useState(false)

  // 首屏拉全量，本地按 category 分组（diagnostics/alerts/concepts/reference）
  const loadAll = () => {
    setListLoading(true)
    getPlaybooks()
      .then((r) => {
        const d = r.data || {}
        const list = Array.isArray(d) ? d : d.playbooks || []
        setAll(list)
        const m = new Map<string, number>()
        list.forEach((p) => { const c = p.category || 'other'; m.set(c, (m.get(c) || 0) + 1) })
        setCats([...m.entries()].map(([name, count]) => ({ name, count })))
      })
      .catch(() => { setAll([]); setCats([]) })
      .finally(() => setListLoading(false))
  }
  useEffect(() => { loadAll() }, [])

  const visible: PlaybookEntry[] = hits !== null
    ? hits
    : (activeCat ? all.filter((p) => p.category === activeCat) : all)

  const onSearch = () => {
    if (!q.trim()) { setHits(null); return }
    setSearching(true)
    getPlaybooks({ q: q.trim() })
      .then((r) => {
        const d = r.data || {}
        setHits(Array.isArray(d) ? d : d.items || [])
      })
      .catch(() => setHits([]))
      .finally(() => setSearching(false))
  }

  const openDoc = (path: string) => {
    setDocLoading(true)
    getPlaybookContent(path)
      .then((r) => setDoc(r.data))
      .catch(() => { message.error('加载 Playbook 原文失败'); setDoc(null) })
      .finally(() => setDocLoading(false))
  }

  return (
    <div style={{ display: 'flex', gap: 16, alignItems: 'flex-start' }}>
      {/* 左：搜索 + 分类 + 列表 */}
      <div className="card" style={{ width: 320, flexShrink: 0, padding: 0, marginBottom: 0 }}>
        <div style={{ padding: 12, borderBottom: '1px solid var(--border-soft)' }}>
          <Space.Compact style={{ width: '100%' }}>
            <Input value={q} onChange={(e) => setQ(e.target.value)} onPressEnter={onSearch} allowClear placeholder="搜索 playbook…" />
            <Button type="primary" loading={searching} onClick={onSearch}>搜索</Button>
          </Space.Compact>
        </div>
        <Menu
          mode="inline"
          selectedKeys={[activeCat || '__all']}
          onClick={(e) => setActiveCat(e.key === '__all' ? '' : e.key)}
          style={{ borderInlineEnd: 'none' }}
          items={[
            { key: '__all', label: '全部' },
            ...cats.map((c) => ({ key: c.name, label: `${c.name}（${c.count}）` })),
          ]}
        />
        <div style={{ maxHeight: 'calc(100vh - 440px)', overflow: 'auto', borderTop: '1px solid var(--border-soft)' }}>
          {listLoading || searching ? (
            <div style={{ textAlign: 'center', padding: 24 }}><Spin size="small" /></div>
          ) : visible.length === 0 ? (
            <Empty text={hits !== null ? '未检索到相关内容' : '暂无 Playbook'} hint={hits !== null ? '尝试更换关键词' : ''} />
          ) : visible.map((p) => (
            <div key={p.path} onClick={() => openDoc(p.path)} role="button"
              style={{ padding: '10px 14px', cursor: 'pointer', borderBottom: '1px solid var(--border-soft)' }}
              onMouseEnter={(e) => (e.currentTarget.style.background = 'var(--surface-2)')}
              onMouseLeave={(e) => (e.currentTarget.style.background = 'transparent')}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 8 }}>
                <span style={{ fontSize: 13, fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{p.title || p.path}</span>
                {typeof p.score === 'number' && (
                  <Tag color={p.score >= 0.7 ? 'green' : p.score >= 0.6 ? 'orange' : 'default'} style={{ marginRight: 0, flexShrink: 0 }}>{p.score.toFixed(2)}</Tag>
                )}
              </div>
              {p.preview && (
                <div style={{ fontSize: 12, color: 'var(--text-muted)', marginTop: 4, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{p.preview}</div>
              )}
              <div style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 4, fontFamily: 'monospace' }}>{p.path}</div>
            </div>
          ))}
        </div>
      </div>

      {/* 右：原文详情（react-markdown 渲染） */}
      <div className="card" style={{ flex: 1, minWidth: 0, padding: 16, marginBottom: 0 }}>
        {docLoading ? (
          <div style={{ textAlign: 'center', padding: 48 }}><Spin /></div>
        ) : doc ? (
          <>
            <div style={{ fontSize: 16, fontWeight: 700, marginBottom: 12, borderBottom: '1px solid var(--border-soft)', paddingBottom: 10, fontFamily: 'monospace' }}>
              {doc.path}
            </div>
            <div style={{ fontSize: 13, lineHeight: 1.8, color: 'var(--text-secondary)' }}>
              <ReactMarkdown remarkPlugins={[remarkGfm]}>{doc.content}</ReactMarkdown>
            </div>
          </>
        ) : (
          <Empty text="选择左侧 Playbook 查看原文" hint="支持关键词搜索进行向量检索" />
        )}
      </div>
    </div>
  )
}

export default Knowledge
