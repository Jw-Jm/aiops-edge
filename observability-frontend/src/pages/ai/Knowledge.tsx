import React, { useEffect, useState } from 'react'
import { Table, Button, Modal, Form, Input, Select, Tag, Space, message, Popconfirm, Tabs } from 'antd'
import { listKnowledge, addKnowledge, deleteKnowledge, getRagStats, reloadRagKnowledge, KnowledgeItem, RagStats } from '../../api/client'
import { PageHeader, Breadcrumb, Empty } from '../../components/ui/PageKit'

const Knowledge: React.FC = () => {
  const [data, setData] = useState<KnowledgeItem[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [open, setOpen] = useState(false)
  const [q, setQ] = useState('')
  const [type, setType] = useState('all')  // all | case | knowledge（Tab 筛选）
  const [rag, setRag] = useState<RagStats | null>(null)
  const [reloadLoading, setReloadLoading] = useState(false)
  const [form] = Form.useForm()

  const load = (page = 1, size = 50) => {
    setLoading(true)
    listKnowledge({ page, size, type, ...(q ? { q } : {}) })
      .then((r) => {
        const d = r.data
        setData(Array.isArray(d) ? d : d?.items || [])
        setTotal(d?.total ?? (Array.isArray(d) ? d.length : 0))
      })
      .catch(() => { setData([]); setTotal(0) })
      .finally(() => setLoading(false))
  }
  useEffect(() => { load() }, [type])

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
      title: '类型', dataIndex: 'type', key: 'type', width: 90,
      render: (v: string) => (v === 'knowledge' ? <Tag color="blue">知识文档</Tag> : <Tag color="green">故障案例</Tag>),
    },
    { title: '标题', dataIndex: 'title', key: 'title', width: 200, render: (v: string) => <span style={{ fontWeight: 500 }}>{v}</span> },
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
      title: '更新时间', dataIndex: 'updated_at', key: 'updated_at', width: 160,
      render: (v: string) => <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>{(v || '').replace('T', ' ').slice(0, 16)}</span>,
    },
    {
      title: '操作', key: 'act', width: 90,
      render: (_: unknown, r: KnowledgeItem) => (
        <Popconfirm title="确认删除该知识条目？" onConfirm={() => {
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
      <PageHeader title="知识库" desc="运维知识条目与 RAG 故障案例库，供 AI 诊断检索与团队沉淀"
        actions={
          <Space>
            <Button onClick={() => { setQ(''); load() }}>刷新</Button>
            <Button type="primary" onClick={() => { form.resetFields(); setOpen(true) }}>新增知识</Button>
          </Space>
        } />

      {/* 统计卡：知识条目 + RAG 案例库 */}
      <div style={{ display: 'flex', gap: 16, marginBottom: 16, flexWrap: 'wrap' }}>
        <div className="card" style={{ flex: 1, minWidth: 220, marginBottom: 0, padding: 16 }}>
          <div style={{ fontSize: 13, color: 'var(--text-muted)' }}>知识文档</div>
          <div style={{ fontSize: 30, fontWeight: 700, marginTop: 4 }}>{total}</div>
        </div>
        <div className="card" style={{ flex: 1, minWidth: 220, marginBottom: 0, padding: 16 }}>
          <div style={{ fontSize: 13, color: 'var(--text-muted)' }}>故障案例（AI 检索）</div>
          <div style={{ fontSize: 30, fontWeight: 700, marginTop: 4 }}>{rag?.cases ?? 0}</div>
        </div>
        <div className="card" style={{ flex: 1, minWidth: 220, marginBottom: 0, padding: 16 }}>
          <div style={{ fontSize: 13, color: 'var(--text-muted)' }}>知识条目</div>
          <div style={{ fontSize: 30, fontWeight: 700, marginTop: 4 }}>{total}</div>
        </div>
        <div className="card" style={{ flex: 1, minWidth: 220, marginBottom: 0, padding: 16 }}>
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

      {/* 搜索 + 类型筛选 + 列表 */}
      <div className="card" style={{ padding: 0 }}>
        <div style={{ padding: '8px 12px 0' }}>
          <Tabs
            activeKey={type}
            onChange={(k) => { setType(k); setQ('') }}
            items={[
              { key: 'all', label: `全部 (${rag?.total ?? 0})` },
              { key: 'case', label: `故障案例 (${rag?.cases ?? 0})` },
              { key: 'knowledge', label: `知识文档 (${rag?.knowledge ?? 0})` },
            ]}
            size="small"
          />
        </div>
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
          locale={{ emptyText: <Empty text={'暂无知识条目，点击右上角「新增知识」录入，或「导入内置案例」'} /> }} />
      </div>

      <Modal title="新增知识" open={open} onOk={submit} onCancel={() => setOpen(false)} destroyOnClose width={620}>
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

export default Knowledge
