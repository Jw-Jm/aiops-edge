import React, { useEffect, useState } from 'react'
import { Tabs, Input, Button, Space, Table, Tag, Empty } from 'antd'
import { nl2sqlTranslate, getMcpTools, listSkills } from '../../api/client'
import { PageHeader, Breadcrumb } from '../../components/ui/PageKit'
import { useUIStore } from '../../store/uiStore'

const AiTools: React.FC = () => {
  const [q, setQ] = useState('')
  const [sql, setSql] = useState('')
  const [sqlErr, setSqlErr] = useState('')
  const [skills, setSkills] = useState<any[]>([])
  const [mcp, setMcp] = useState<any[]>([])
  const currentClusterId = useUIStore((s) => s.currentClusterId)
  const clusters = useUIStore((s) => s.clusters)
  const scopeLabel = currentClusterId === 'all'
    ? '全部集群'
    : (clusters.find((c) => String(c.id) === currentClusterId)?.name || currentClusterId)

  useEffect(() => {
    listSkills().then((r) => setSkills(r.data?.skills || r.data || [])).catch(() => {})
    getMcpTools().then((r) => setMcp(Array.isArray(r.data) ? r.data : r.data?.tools || [])).catch(() => {})
  }, [])

  const translate = () => {
    setSqlErr(''); setSql('')
    nl2sqlTranslate({ question: q }).then((r) => {
      const d = r.data
      if (d?.error) setSqlErr(d.error)
      else setSql(d?.sql || JSON.stringify(d))
    }).catch((e) => setSqlErr(e?.response?.data?.error || '翻译失败'))
  }

  return (
    <div>
      <Breadcrumb items={[{ t: '智能运维' }, { t: 'AI 工具' }]} />
      <PageHeader title="AI 工具" desc="NL2SQL · MCP 工具 · 技能目录"
        actions={<Tag color="blue" style={{ marginRight: 0 }}>执行范围：{scopeLabel}</Tag>} />

      <Tabs
        items={[
          {
            key: 'nl2sql', label: 'NL2SQL 查询',
            children: (
              <div className="card" style={{ padding: 16 }}>
                <Space wrap style={{ width: '100%' }}>
                  <Input value={q} onChange={(e) => setQ(e.target.value)} onPressEnter={translate} placeholder="自然语言描述查询，如：近 24 小时各服务调用量" style={{ width: 480 }} />
                  <Button type="primary" onClick={translate}>翻译 SQL</Button>
                </Space>
                {sqlErr && <div style={{ marginTop: 10, color: 'var(--danger)', fontSize: 12 }}>⚠ {sqlErr}</div>}
                {sql && <pre style={{ marginTop: 12, background: 'var(--surface-2)', borderRadius: 8, padding: 12, fontSize: 12, whiteSpace: 'pre-wrap' }}>{sql}</pre>}
              </div>
            ),
          },
          {
            key: 'mcp', label: 'MCP 工具',
            children: (
              <div className="card" style={{ padding: 0 }}>
                <Table rowKey="name" dataSource={mcp} size="small" pagination={false}
                  locale={{ emptyText: <Empty /> }}
                  columns={[{ title: '名称', dataIndex: 'name', key: 'name', render: (v: string, r: any) => <span>{v}{r.cls ? <Tag style={{ marginLeft: 6 }}>{r.cls}</Tag> : null}</span> },
                    { title: '描述', dataIndex: 'description', key: 'description', render: (v: string) => <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>{v || '-'}</span> }]} />
              </div>
            ),
          },
          {
            key: 'skills', label: '技能目录',
            children: (
              <div className="card" style={{ padding: 0 }}>
                <Table rowKey="key" dataSource={skills} size="small" pagination={false}
                  locale={{ emptyText: <Empty /> }}
                  columns={[{ title: '技能', dataIndex: 'name', key: 'name', render: (_: any, r: any) => r.name || r.key },
                    { title: '描述', dataIndex: 'description', key: 'description', render: (v: string) => <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>{v || '-'}</span> }]} />
              </div>
            ),
          },
        ]}
      />
    </div>
  )
}

export default AiTools
