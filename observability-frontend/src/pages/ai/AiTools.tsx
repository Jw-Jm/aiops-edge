import React, { useEffect, useState } from 'react'
import { Tabs, Input, Button, Space, Table, Tag, Empty, Spin, Typography, message } from 'antd'
import { nl2sqlTranslate, nl2sqlExecute, getMcpTools, listSkills, callMcpTool } from '../../api/client'
import { PageHeader, Breadcrumb } from '../../components/ui/PageKit'
import AppIcon from '../../components/AppIcons'
import { useUIStore } from '../../store/uiStore'

const AiTools: React.FC = () => {
  const [q, setQ] = useState('')
  const [sql, setSql] = useState('')
  const [sqlId, setSqlId] = useState('')
  const [sqlErr, setSqlErr] = useState('')
  const [execLoading, setExecLoading] = useState(false)
  const [result, setResult] = useState<{ columns?: string[]; rows?: any[]; count?: number } | null>(null)
  const [skills, setSkills] = useState<any[]>([])
  const [mcp, setMcp] = useState<any[]>([])
  const [activeTool, setActiveTool] = useState<any>(null)
  const [mcpArgs, setMcpArgs] = useState<Record<string, any>>({})
  const [mcpResult, setMcpResult] = useState('')
  const [mcpLoading, setMcpLoading] = useState(false)
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
    setSqlErr(''); setSql(''); setSqlId(''); setResult(null)
    nl2sqlTranslate({ question: q }).then((r) => {
      const d = r.data
      if (d?.error) setSqlErr(d.error)
      else { setSql(d?.sql || JSON.stringify(d)); setSqlId(d?.id || '') }
    }).catch((e) => setSqlErr(e?.response?.data?.error || '翻译失败'))
  }

  // 修复 4.2：翻译出 SQL 后可一键执行并展示结果表格（安全护栏已由后端保证：仅 SELECT + 表白名单 + LIMIT）
  const run = async () => {
    if (!sqlId) { message.warning('请先翻译生成 SQL'); return }
    setExecLoading(true); setResult(null)
    try {
      const r = await nl2sqlExecute(sqlId)
      setResult(r.data)
    } catch (e: any) {
      message.error(e?.response?.data?.detail || e?.response?.data?.error || '执行失败')
    } finally {
      setExecLoading(false)
    }
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
                {sql && (
                  <>
                    <pre style={{ marginTop: 12, background: 'var(--surface-2)', borderRadius: 8, padding: 12, fontSize: 12, whiteSpace: 'pre-wrap', position: 'relative' }}>
                      {sql}
                    </pre>
                    <Space style={{ marginTop: 8 }}>
                      <Button type="primary" size="small" loading={execLoading} onClick={run} icon={<AppIcon name="send" />}>
                        执行查询
                      </Button>
                      <Button size="small" onClick={() => { navigator.clipboard?.writeText(sql); message.success('已复制 SQL') }}>
                        复制 SQL
                      </Button>
                    </Space>
                    {result && (
                      <div style={{ marginTop: 12 }}>
                        <div style={{ marginBottom: 8, fontSize: 12, color: 'var(--text-secondary)' }}>
                          查询结果 · 共 <b>{result.count ?? result.rows?.length ?? 0}</b> 行
                        </div>
                        <Table
                          size="small"
                          rowKey={(_, i) => String(i)}
                          dataSource={(result.rows || []).map((r, i) => ({ __i: i, ...r }))}
                          columns={(result.columns || []).map((c) => ({ title: c, dataIndex: c, key: c, render: (v: any) => <span style={{ fontVariantNumeric: 'tabular-nums' }}>{String(v ?? '')}</span> }))}
                          pagination={{ pageSize: 10, showSizeChanger: false }}
                        />
                      </div>
                    )}
                  </>
                )}
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
                    { title: '描述', dataIndex: 'description', key: 'description', render: (v: string) => <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>{v || '-'}</span> },
                    { title: '操作', key: 'act', render: (_: unknown, r: any) => <Button size="small" onClick={() => { setActiveTool(r); setMcpArgs({}); setMcpResult('') }}>调用</Button> }]} />
                {activeTool && (
                  <div style={{ padding: 12, borderTop: '1px solid var(--border-soft)' }}>
                    <div style={{ fontWeight: 600, marginBottom: 8 }}>调用 {activeTool.name}</div>
                    {activeTool.parameters && Object.keys(activeTool.parameters).length > 0 ? (
                      Object.entries(activeTool.parameters).map(([k, v]) => (
                        <Input key={k} placeholder={`${k} (${(v as any)?.type || 'string'})`}
                          value={mcpArgs[k] || ''}
                          onChange={(e) => setMcpArgs((p) => ({ ...p, [k]: e.target.value }))}
                          style={{ marginBottom: 8, maxWidth: 320 }} />
                      ))
                    ) : null}
                    <Space>
                      <Button type="primary" loading={mcpLoading} onClick={async () => {
                        setMcpLoading(true)
                        try {
                          const r = await callMcpTool(activeTool.name, mcpArgs)
                          setMcpResult(typeof r.data === 'string' ? r.data : JSON.stringify(r.data, null, 2))
                        } catch (e: any) {
                          setMcpResult(`调用失败: ${e?.response?.data?.error || e?.response?.data?.detail || e?.message || e}`)
                        } finally { setMcpLoading(false) }
                      }}>执行</Button>
                      <Button onClick={() => { setActiveTool(null); setMcpResult(''); setMcpArgs({}) }}>关闭</Button>
                    </Space>
                    {mcpResult && (
                      <pre style={{ marginTop: 10, maxHeight: 260, overflow: 'auto', fontSize: 12, background: 'var(--bg-soft)', padding: 10, borderRadius: 6 }}>{mcpResult}</pre>
                    )}
                  </div>
                )}
              </div>
            ),
          },
          {
            key: 'skills', label: '技能目录',
            children: (
              <div className="card" style={{ padding: 0 }}>
                <Table rowKey="key" dataSource={skills} size="small" pagination={false}
                  locale={{ emptyText: <Empty description={<span style={{ fontSize: 12, color: 'var(--text-muted)' }}>暂无技能<br />可复用诊断/巡检能力将通过 AI 对话自动调度</span>} /> }}
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
