import React, { useState } from 'react'
import { Card, Input, Button, Space, Typography, Table, message, Alert, Tag, Divider } from 'antd'
import { ThunderboltFilled, PlayCircleOutlined, ReloadOutlined, CheckCircleOutlined } from '@ant-design/icons'
import { nl2sqlTranslate, nl2sqlExecute } from '../../api/client'

const { Text, Paragraph } = Typography

interface TranslateResult {
  id: string; sql: string; explanation: string; pending: boolean; error?: string
}

const NL2SQL: React.FC = () => {
  const [question, setQuestion] = useState('')
  const [loading, setLoading] = useState(false)
  const [translating, setTranslating] = useState(false)
  const [result, setResult] = useState<TranslateResult | null>(null)
  const [cols, setCols] = useState<string[]>([])
  const [rows, setRows] = useState<Record<string, unknown>[]>([])
  const [executed, setExecuted] = useState(false)
  const [error, setError] = useState('')

  const handleTranslate = async () => {
    if (!question.trim()) { message.warning('请输入查询意图'); return }
    setTranslating(true); setError(''); setResult(null); setCols([]); setRows([]); setExecuted(false)
    try {
      const r = await nl2sqlTranslate({ question: question.trim() })
      const d = r.data as TranslateResult
      if (d.error) { setError(d.error) }
      setResult(d)
    } catch (e: any) {
      message.error(e?.response?.data?.detail || '翻译失败')
    } finally {
      setTranslating(false)
    }
  }

  const handleExecute = async () => {
    if (!result?.id) return
    setLoading(true); setError('')
    try {
      const r = await nl2sqlExecute(result.id)
      const d = r.data as { columns: string[]; rows: Record<string, unknown>[]; count: number; error?: string }
      if (d.error) { setError(d.error) }
      setCols(d.columns || [])
      setRows(d.rows || [])
      setExecuted(true)
      if (d.columns && d.columns.length) message.success(`查询完成，返回 ${d.rows?.length || 0} 行`)
    } catch (e: any) {
      message.error(e?.response?.data?.detail || '执行失败')
    } finally {
      setLoading(false)
    }
  }

  const columns = cols.map(c => ({
    title: c,
    dataIndex: c,
    key: c,
    ellipsis: true,
    render: (v: unknown) => String(v ?? ''),
  }))

  return (
    <div>
      <Card title="自然语言查询 ClickHouse" style={{ marginBottom: 16 }}>
        <Space.Compact style={{ width: '100%' }}>
          <Input
            size="large"
            placeholder="输入查询意图，如：近 24 小时各服务的调用量和错误率"
            value={question}
            onChange={e => setQuestion(e.target.value)}
            onPressEnter={handleTranslate}
            disabled={translating}
          />
          <Button size="large" type="primary" icon={<ThunderboltFilled />} onClick={handleTranslate} loading={translating}>翻译 SQL</Button>
        </Space.Compact>
        <Paragraph type="secondary" style={{ marginTop: 8, fontSize: 12 }}>
          支持查询：observability.trace_spans（调用链）、service_topology（拓扑）、log_records（日志）。SQL 生成后需人工确认再执行。
        </Paragraph>
      </Card>

      {error && (
        <Alert type="warning" showIcon message="无法生成/执行 SQL" description={error} style={{ marginBottom: 16 }} closable onClose={() => setError('')} />
      )}

      {result?.sql && (
        <Card
          title="生成的 SQL"
          extra={result.pending && !executed ? (
            <Button type="primary" icon={<PlayCircleOutlined />} onClick={handleExecute} loading={loading}>确认执行</Button>
          ) : executed ? <Tag color="green" icon={<CheckCircleOutlined />}>已执行</Tag> : null}
          style={{ marginBottom: 16 }}
        >
          <pre style={{ background: '#1a1a1a', padding: 12, borderRadius: 8, overflowX: 'auto', fontSize: 13, color: '#7ec699', margin: 0 }}>
            {result.sql}
          </pre>
          <Divider style={{ margin: '12px 0' }} />
          <Text type="secondary">意图：{result.explanation}</Text>
        </Card>
      )}

      {executed && cols.length > 0 && (
        <Card
          title={`查询结果（${rows.length} 行）`}
          extra={<Button icon={<ReloadOutlined />} onClick={handleExecute} loading={loading}>重新查询</Button>}
        >
          <Table
            size="small"
            rowKey={(_, i) => String(i)}
            columns={columns}
            dataSource={rows.map((r, i) => ({ ...r, __key: i }))}
            scroll={{ x: 'max-content' }}
            pagination={{ pageSize: 20, showTotal: (t: number) => `共 ${t} 行` }}
          />
        </Card>
      )}

      {executed && cols.length === 0 && !error && (
        <Card><Alert type="info" message="查询执行完成，但没有返回任何行" /></Card>
      )}
    </div>
  )
}

export default NL2SQL
