import React, { useEffect, useState } from 'react'
import { Tabs, Input, Button, Space, Table, Tag, message, Empty, Modal, Form, Drawer } from 'antd'
import { nl2sqlTranslate, getMcpTools, callMcpTool, listSkills } from '../../api/client'
import { PageHeader, Breadcrumb } from '../../components/ui/PageKit'
import { useUIStore } from '../../store/uiStore'

const AiTools: React.FC = () => {
  const [q, setQ] = useState('')
  const [sql, setSql] = useState('')
  const [sqlErr, setSqlErr] = useState('')
  const [skills, setSkills] = useState<any[]>([])
  const [mcp, setMcp] = useState<any[]>([])
  const [form] = Form.useForm()
  // MCP 调用状态：当前工具 + 参数弹窗 + 结果展示
  const [callTool, setCallTool] = useState<any>(null)
  const [callArgs, setCallArgs] = useState<Record<string, any>>({})
  const [result, setResult] = useState<any>(null)
  const [calling, setCalling] = useState(false)
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

  // P1-2 修复：MCP 调用弹参数表单 → 真实调用 → 展示结果
  const invokeMcp = (tool: any) => {
    const schema = tool.inputSchema || tool.parameters || {}
    // 兼容两种 schema：标准 JSON Schema {properties:{...}} 或扁平映射 {name:type}
    const props = schema.properties || schema
    const keys = Object.keys(props).filter((k) => k !== 'type' && k !== 'required' && k !== 'properties')
    if (keys.length === 0) {
      // 无参数工具直接调用
      doInvoke(tool.name, {})
      return
    }
    // 有参数：弹表单，用参数名做字段
    setCallTool(tool)
    setCallArgs({})
    form.resetFields()
    setResult(null)
  }
  const doInvoke = async (name: string, args: Record<string, any>) => {
    setCalling(true)
    try {
      const r = await callMcpTool(name, args)
      setResult(r.data)
      setCallTool(null)
      setCallArgs({})
      message.success('调用成功')
    } catch (e: any) {
      message.error(e?.response?.data?.error || e?.response?.data?.message || '调用失败')
      setCalling(false)
    }
  }
  const confirmCall = async () => {
    const v = await form.validateFields().catch(() => null)
    if (!v) return
    doInvoke(callTool.name, v)
  }
  // 结果美化：JSON 字符串化
  const pretty = (data: any) => {
    if (data == null) return '无返回'
    try { return JSON.stringify(data, null, 2) } catch { return String(data) }
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
                    { title: '描述', dataIndex: 'description', key: 'description', render: (v: string) => <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>{v || '-'}</span> },
                    { title: '操作', key: 'op', width: 90, render: (_: any, r: any) => <Button size="small" type="primary" ghost loading={calling} onClick={() => invokeMcp(r)}>调用</Button> }]} />
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
                    { title: '描述', dataIndex: 'description', key: 'description', render: (v: string) => <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>{v || '-'}</span> },
                    { title: '类型', dataIndex: 'category', key: 'category', render: (v: string) => v && <Tag>{v}</Tag> }]} />
              </div>
            ),
          },
        ]}
      />

      {/* 参数表单弹窗 */}
      <Modal open={!!callTool} title={'调用 ' + (callTool?.name || '')} onOk={confirmCall} onCancel={() => setCallTool(null)} confirmLoading={calling} okText="执行" cancelText="取消">
        {callTool && (() => {
          const schema = callTool.inputSchema || callTool.parameters || {}
          const props = schema.properties || schema
          const keys = Object.keys(props).filter((k) => k !== 'type' && k !== 'required' && k !== 'properties')
          if (keys.length === 0) return <div style={{ color: 'var(--text-muted)' }}>该工具无需参数，点击"执行"直接调用</div>
          return <Form form={form} layout="vertical">
            {keys.map((k) => <Form.Item key={k} name={k} label={k} rules={props[k]?.required ? [{ required: true }] : []}>
              <Input placeholder={(props[k]?.description) || ''} />
            </Form.Item>)}
          </Form>
        })()}
      </Modal>

      {/* 调用结果 Drawer */}
      <Drawer open={!!result} onClose={() => setResult(null)} title="调用结果" width={560} styles={{ body: { padding: 16, background: 'var(--surface-1)' } }}>
        <pre style={{ whiteSpace: 'pre-wrap', fontSize: 12, lineHeight: 1.7, margin: 0 }}>{result != null ? pretty(result) : '无返回'}</pre>
      </Drawer>
    </div>
  )
}

export default AiTools
