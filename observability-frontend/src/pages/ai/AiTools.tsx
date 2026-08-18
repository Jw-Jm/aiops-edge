import React, { useEffect, useState } from 'react'
import { Alert, Tabs, Input, Button, Space, Table, Tag, Empty, Spin, Select, Popconfirm, Typography, Modal, message } from 'antd'
import api, { nl2sqlTranslate, nl2sqlExecute, getMcpTools, listSkills, executeSkill, callMcpTool } from '../../api/client'
import {
  listInstalledPacks, uninstallMarketplacePack,
  type InstalledPack, type InstallResult, type SourceType,
} from '../../api/marketplace'
import { PageHeader, Breadcrumb } from '../../components/ui/PageKit'
import AppIcon from '../../components/AppIcons'
import { useUIStore } from '../../store/uiStore'

// B12 修复：MCP 参数按工具输入 schema 声明类型转换（number/boolean），
// 避免全部以字符串发送导致后端类型校验失败。
function coerceMcpArgs(args: Record<string, any>, schema: Record<string, any> | undefined): Record<string, any> {
  const out: Record<string, any> = {}
  for (const [k, raw] of Object.entries(args)) {
    const type = schema?.[k]?.type || 'string'
    const v = String(raw ?? '')
    if (type === 'number' || type === 'integer') {
      const n = Number(v)
      out[k] = isNaN(n) ? v : n
    } else if (type === 'boolean') {
      if (v === 'true') out[k] = true
      else if (v === 'false') out[k] = false
      else out[k] = v
    } else {
      out[k] = v
    }
  }
  return out
}

const AiTools: React.FC = () => {
  const [q, setQ] = useState('')
  const [sql, setSql] = useState('')
  const [sqlId, setSqlId] = useState('')
  const [sqlErr, setSqlErr] = useState('')
  const [sqlNotice, setSqlNotice] = useState('')
  const [execLoading, setExecLoading] = useState(false)
  const [result, setResult] = useState<{ columns?: string[]; rows?: any[]; count?: number } | null>(null)
  const [skills, setSkills] = useState<any[]>([])
  const [mcp, setMcp] = useState<any[]>([])
  const [activeTool, setActiveTool] = useState<any>(null)
  const [mcpArgs, setMcpArgs] = useState<Record<string, any>>({})
  const [mcpResult, setMcpResult] = useState('')
  const [mcpLoading, setMcpLoading] = useState(false)
  // B4: 技能执行结果弹窗
  const [skillResult, setSkillResult] = useState<{ key: string; name: string; output: string } | null>(null)
  const [skillExecKey, setSkillExecKey] = useState<string | null>(null)
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
    setSqlErr(''); setSqlNotice(''); setSql(''); setSqlId(''); setResult(null)
    nl2sqlTranslate({ question: q }).then((r) => {
      const d = r.data
      if (d?.error) setSqlErr(d.error)
      // B12 修复：响应中无 sql 字段时给出明确错误提示，不再把整个响应体当 SQL 展示
      else if (d?.sql) { setSql(d.sql); setSqlId(d?.id || ''); setSqlNotice(d?.read_only_notice || '') }
      else setSqlErr('翻译失败：响应中未包含 SQL 字段，请检查后端 NL2SQL 服务')
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
                {sqlNotice && <Alert style={{ marginTop: 10 }} type="warning" showIcon message={sqlNotice} />}
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
                          const r = await callMcpTool(activeTool.name, coerceMcpArgs(mcpArgs, activeTool.parameters))
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
                    { title: '描述', dataIndex: 'description', key: 'description', render: (v: string) => <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>{v || '-'}</span> },
                    {
                      title: '操作', key: 'act', width: 90,
                      render: (_: unknown, r: any) => {
                        const key = r.key || r.name
                        return (
                          <Button size="small" type="primary" ghost loading={skillExecKey === key}
                            onClick={async () => {
                              setSkillExecKey(key)
                              try {
                                const res = await executeSkill(key, {})
                                const out = typeof res.data === 'string' ? res.data : JSON.stringify(res.data, null, 2)
                                setSkillResult({ key, name: r.name || key, output: out })
                              } catch (e: any) {
                                setSkillResult({ key, name: r.name || key, output: `执行失败: ${e?.response?.data?.error || e?.response?.data?.detail || e?.message || e}` })
                              } finally { setSkillExecKey(null) }
                            }}>执行</Button>
                        )
                      },
                    }]} />
              </div>
            ),
          },
          {
            key: 'market', label: '市场',
            children: <MarketplaceTab />,
          },
        ]}
      />

      {/* B4: 技能执行结果弹窗 */}
      <Modal title={skillResult ? `技能执行结果 · ${skillResult.name}` : '技能执行结果'}
        open={!!skillResult} onCancel={() => setSkillResult(null)} footer={null} width={640} destroyOnClose>
        {skillResult && (
          <pre style={{ maxHeight: 420, overflow: 'auto', fontSize: 12, background: 'var(--bg-soft)', padding: 12, borderRadius: 6, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
            {skillResult.output}
          </pre>
        )}
      </Modal>
    </div>
  )
}

// ===== Skill 市场 Tab：安装 pack + 已安装列表（Task D5）=====
const MarketplaceTab: React.FC = () => {
  const [source, setSource] = useState('')
  const [sourceType, setSourceType] = useState<SourceType>('local')
  const [installing, setInstalling] = useState(false)
  const [installed, setInstalled] = useState<InstalledPack[]>([])
  const [listLoading, setListLoading] = useState(true)
  const [result, setResult] = useState<InstallResult | null>(null)

  const loadInstalled = () => {
    setListLoading(true)
    listInstalledPacks()
      .then((r) => {
        const d = r.data || {}
        setInstalled(Array.isArray(d) ? d : d.installed || [])
      })
      .catch(() => setInstalled([]))
      .finally(() => setListLoading(false))
  }
  useEffect(() => { loadInstalled() }, [])

  const onInstall = () => {
    if (!source.trim()) { message.warning('请填写安装来源'); return }
    setInstalling(true); setResult(null)
    // B12 修复：sourceType 参与实际安装请求（后端当前仅消费 source，source_type 为前向兼容字段）
    api.post('/ai/marketplace/install', { source: source.trim(), source_type: sourceType })
      .then((r) => {
        setResult(r.data)
        message.success('安装成功')
        setSource('')
        loadInstalled()
      })
      .catch((e) => message.error(e?.response?.data?.detail || e?.response?.data?.error || '安装失败'))
      .finally(() => setInstalling(false))
  }

  const sigTag = (s?: string) =>
    s === 'verified' ? <Tag color="green">verified</Tag>
      : s === 'failed' ? <Tag color="red">failed</Tag>
        : <Tag>unsigned</Tag>

  const fmtTime = (v?: string) => {
    if (!v) return '-'
    const n = Number(v)
    const d = new Date(!isNaN(n) && String(v).length <= 11 ? n * 1000 : v)
    return isNaN(d.getTime())
      ? String(v)
      : d.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
  }

  const cols = [
    { title: 'pack_id', dataIndex: 'pack_id', key: 'pack_id', render: (v: string) => <span style={{ fontWeight: 600, fontFamily: 'monospace' }}>{v || '-'}</span> },
    { title: '来源', dataIndex: 'source', key: 'source', render: (v: string) => <span style={{ fontSize: 12, color: 'var(--text-muted)', wordBreak: 'break-all' }}>{v || '-'}</span> },
    { title: '签名状态', dataIndex: 'signature_state', key: 'signature_state', width: 110, render: (v: string) => sigTag(v) },
    { title: '安装时间', dataIndex: 'installed_at', key: 'installed_at', width: 150, render: (v: string) => <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>{fmtTime(v)}</span> },
    {
      title: '操作', key: 'act', width: 90,
      render: (_: unknown, r: InstalledPack) => (
        <Popconfirm title={`确认卸载 ${r.pack_id}？卸载后对应 skill 将立即失效`} onConfirm={() => {
          uninstallMarketplacePack(r.pack_id)
            .then(() => { message.success('已卸载'); setResult(null); loadInstalled() })
            .catch(() => message.error('卸载失败'))
        }}>
          <Button size="small" type="link" danger>卸载</Button>
        </Popconfirm>
      ),
    },
  ]

  return (
    <>
      <div className="card" style={{ padding: 16, marginBottom: 16 }}>
        <div style={{ fontWeight: 600, marginBottom: 10 }}>安装 Skill Pack</div>
        <Space wrap style={{ width: '100%' }}>
          <Select value={sourceType} onChange={(v) => setSourceType(v as SourceType)} style={{ width: 116 }}
            options={[
              { value: 'local', label: '本地目录' },
              { value: 'tarball', label: 'Tarball' },
              { value: 'git', label: 'Git URL' },
            ]} />
          <Input
            value={source}
            onChange={(e) => setSource(e.target.value)}
            onPressEnter={onInstall}
            allowClear
            placeholder={sourceType === 'git' ? '如 https://github.com/org/skill-pack.git' : '如 /data/skills/packs/example-pack 或 /tmp/example.tgz'}
            style={{ width: 480 }}
          />
          <Button type="primary" loading={installing} onClick={onInstall}>安装</Button>
        </Space>
        {result && (
          <div style={{ marginTop: 12, fontSize: 13, background: 'var(--bg-soft)', borderRadius: 8, padding: 12 }}>
            <Space wrap>
              <span>已安装 <b style={{ fontFamily: 'monospace' }}>{result.pack_id}</b></span>
              {sigTag(result.signature_state)}
            </Space>
            {result.skills && result.skills.length > 0 && (
              <div style={{ marginTop: 8, fontSize: 12 }}>
                包含技能：
                {result.skills.map((s) => <Tag key={s} style={{ marginLeft: 6 }}>{s}</Tag>)}
              </div>
            )}
          </div>
        )}
      </div>
      <div className="card" style={{ padding: 0 }}>
        <div style={{ padding: '12px 16px', borderBottom: '1px solid var(--border-soft)', fontWeight: 600 }}>
          已安装 Pack（{installed.length}）
        </div>
        <Table rowKey="pack_id" loading={listLoading} dataSource={installed} columns={cols} size="small" pagination={false}
          locale={{ emptyText: <Empty description={<span style={{ fontSize: 12, color: 'var(--text-muted)' }}>暂无已安装 pack</span>} /> }} />
      </div>
    </>
  )
}

export default AiTools
