import React, { useState, useEffect, useCallback, useRef } from 'react'
import { Card, Table, Tag, Button, Space, Typography, Modal, Tabs, Popconfirm, message, Empty, Input, Form, Select, Row, Col, Badge, Statistic, Progress, Tooltip } from 'antd'
import { CheckOutlined, CloseOutlined, EyeOutlined, ReloadOutlined, ToolOutlined, PlusOutlined, ThunderboltOutlined, DownloadOutlined, WifiOutlined, HistoryOutlined, FileTextOutlined, LineChartOutlined } from '@ant-design/icons'
import api from '../../api/client'
import { fmtLocalTime } from '../../utils/date'

const { Text, Paragraph } = Typography

interface Task {
  id: string; status: string; source: string; service: string
  context: string; diagnosis: string; plan: string; script: string
  risk_score: number; risk_reason: string; report: string
  created_at: string; done_at: string
}

const STATUS_MAP: Record<string, { color: string; label: string }> = {
  pending:     { color: 'default', label: '排队' },
  queued:      { color: 'default', label: '待诊断' },
  diagnosing:  { color: 'processing', label: '诊断中' },
  waiting:     { color: 'warning', label: '待审批' },
  approved:    { color: 'cyan', label: '已批准' },
  running:     { color: 'processing', label: '执行中' },
  done:        { color: 'green', label: '已完成' },
  failed:      { color: 'red', label: '失败' },
  rejected:    { color: 'default', label: '已拒绝' },
}

const SOURCE_TAGS: Record<string, { color: string; label: string }> = {
  alert:        { color: 'error', label: '🔔 告警' },
  log_anomaly:  { color: 'warning', label: '📋 日志异常' },
  manual:       { color: 'processing', label: '👤 手动' },
  ai_chat:      { color: 'purple', label: '🤖 AI建议' },
}

const Tasks: React.FC = () => {
  const [tasks, setTasks] = useState<Task[]>([])
  const [loading, setLoading] = useState(false)
  const [activeTab, setActiveTab] = useState('all')
  const [selected, setSelected] = useState<Task | null>(null)
  const [detailOpen, setDetailOpen] = useState(false)
  const [createOpen, setCreateOpen] = useState(false)
  const [form] = Form.useForm()

  // 报告历史
  const [pageTab, setPageTab] = useState('tasks')
  const [reports, setReports] = useState<any[]>([])
  const [reportsLoading, setReportsLoading] = useState(false)
  const [trend, setTrend] = useState<any[]>([])
  const [serviceFilter, setServiceFilter] = useState('')

  const fetchReports = useCallback(async () => {
    setReportsLoading(true)
    try {
      const params: Record<string, string> = {}
      if (serviceFilter) params.service = serviceFilter
      const r = await api.get('/ops/reports/history', { params })
      setReports(r.data?.reports || [])
    } catch { /* ignore */ }
    setReportsLoading(false)
  }, [serviceFilter])

  const fetchTrend = useCallback(async () => {
    try {
      const r = await api.get('/ops/reports/trend', { params: { days: 14 } })
      setTrend(r.data?.trend || [])
    } catch { /* ignore */ }
  }, [])

  useEffect(() => {
    if (pageTab === 'reports') { fetchReports(); fetchTrend() }
  }, [pageTab, fetchReports, fetchTrend])

  const fetchTasks = useCallback(async () => {
    setLoading(true)
    try {
      const r = await api.get(`/ops/tasks${activeTab !== 'all' ? '?status=' + activeTab : ''}`)
      setTasks(r.data?.tasks || [])
    } catch { /* ignore */ }
    setLoading(false)
  }, [activeTab])

  const [wsStatus, setWsStatus] = useState<'connected'|'disconnected'>('disconnected')
  const wsRef = useRef<WebSocket|null>(null)

  useEffect(() => {
    fetchTasks()
    const t = setInterval(fetchTasks, 30000) // 30s 轮询作为 WebSocket 降级

    // WebSocket 实时推送（后端未实现时自动降级为轮询，不报错）
    let ws: WebSocket | null = null
    try {
      const proto = location.protocol === 'https:' ? 'wss' : 'ws'
      ws = new WebSocket(`${proto}://${location.host}/api/v1/ops/ws`)
      wsRef.current = ws
      ws.onopen = () => setWsStatus('connected')
      ws.onclose = () => setWsStatus('disconnected')
      ws.onerror = () => { /* 静默降级，使用 30s 轮询 */ }
      ws.onmessage = (ev) => {
        try {
          const d = JSON.parse(ev.data)
          if (d.type === 'task_update' && d.task) {
            setTasks(prev => {
              const idx = prev.findIndex(t => t.id === d.task.id)
              if (idx >= 0) {
                const next = [...prev]
                next[idx] = { ...next[idx], ...d.task }
                return next
              }
              return [d.task, ...prev]
            })
          }
        } catch {}
      }
    } catch {
      setWsStatus('disconnected')
    }
    return () => {
      clearInterval(t)
      ws?.close()
    }
  }, [fetchTasks])

  const createTask = async (vals: any) => {
    try {
      const r = await api.post('/ops/tasks', vals)
      message.success('任务已创建, 可在操作中手动触发诊断')
      setCreateOpen(false)
      form.resetFields()
      fetchTasks()
    } catch { message.error('创建失败') }
  }

  const runDiagnosis = async (id: string) => {
    try { await api.post(`/ops/tasks/${id}/run`); message.success('已触发诊断'); fetchTasks() }
    catch { message.error('触发诊断失败') }
  }

  const approve = async (id: string) => {
    try { await api.post(`/ops/tasks/${id}/approve`); message.success('已通过, 进入执行队列'); fetchTasks() }
    catch { message.error('审批失败') }
  }

  const reject = async (id: string) => {
    try { await api.post(`/ops/tasks/${id}/reject`); message.info('已拒绝'); fetchTasks() }
    catch { message.error('操作失败') }
  }

  const tabs = [
    { key: 'all', label: '全部' },
    { key: 'waiting', label: '待审批' },
    { key: 'running', label: '执行中' },
    { key: 'done', label: '已完成' },
    { key: 'failed', label: '失败' },
  ]

  const columns = [
    { title: '时间', dataIndex: 'created_at', key: 'ts', width: 130,
      render: (v: string) => <Text style={{ fontSize: 11, color: '#888' }}>{fmtLocalTime(v, '-', 'MM-DD HH:mm:ss')}</Text> },
    { title: '来源', dataIndex: 'source', key: 'src', width: 100,
      render: (v: string) => {
        const t = SOURCE_TAGS[v] || { color: 'default', label: v || '?' }
        return <Tag color={t.color} style={{ margin: 0 }}>{t.label}</Tag>
      }},
    { title: '服务', dataIndex: 'service', key: 'svc', width: 120,
      render: (v: string) => v ? <Text code style={{ fontSize: 12 }}>{v}</Text> : <Text type='secondary'>-</Text> },
    { title: '状态', dataIndex: 'status', key: 'st', width: 85,
      render: (v: string) => {
        const s = STATUS_MAP[v] || { color: 'default', label: v || '?' }
        return <Badge status={s.color === 'green' ? 'success' : s.color === 'red' ? 'error' : s.color === 'warning' ? 'warning' : s.color === 'processing' ? 'processing' : 'default'} text={<Text style={{ fontSize: 12 }}>{s.label}</Text>} />
      }},
    { title: '风险', dataIndex: 'risk_score', key: 'risk', width: 70, align: 'center' as const,
      render: (v: number) => {
        // risk_score 为 0-1 浮点；改为高/中/低标签，避免"星星数越多越危险"的方向误解
        if (!v || v <= 0) return <Text type='secondary'>-</Text>
        const pct = v * 100
        if (pct >= 70) return <Tag color='red'>高</Tag>
        if (pct >= 40) return <Tag color='orange'>中</Tag>
        return <Tag color='green'>低</Tag>
      } },
    { title: '摘要', dataIndex: 'context', key: 'ctx', ellipsis: true,
      render: (v: string) => <Text style={{ fontSize: 13 }}>{v || '-'}</Text> },
    { title: '操作', key: 'act', width: 240,
      render: (_: any, r: Task) => (
        <Space size={2}>
          <Button size='small' type='link' icon={<EyeOutlined />} onClick={() => { setSelected(r); setDetailOpen(true) }}>详情</Button>
          {(r.status === 'queued' || r.status === 'failed') && (
            <Button size='small' type='link' icon={<ThunderboltOutlined />} style={{ color: '#722ed1' }}
              onClick={() => runDiagnosis(r.id)}>手动诊断</Button>
          )}
          {r.status === 'done' && (
            <a href={`/api/v1/ops/reports/${r.id}/download`} target='_blank' rel='noreferrer'>
              <Button size='small' type='link' icon={<DownloadOutlined />}>报告</Button>
            </a>
          )}
          {r.status === 'waiting' && (
            <>
              <Popconfirm key='ok' title='确认通过？' onConfirm={() => approve(r.id)}>
                <Button size='small' type='link' style={{ color: '#52c41a' }} icon={<CheckOutlined />}>通过</Button>
              </Popconfirm>
              <Popconfirm key='no' title='确认拒绝？' onConfirm={() => reject(r.id)}>
                <Button size='small' type='link' danger icon={<CloseOutlined />}>拒绝</Button>
              </Popconfirm>
            </>
          )}
        </Space>
      ),
    },
  ]

  const counts = {
    all: tasks.length,
    waiting: tasks.filter(t => t.status === 'waiting').length,
    running: tasks.filter(t => t.status === 'running' || t.status === 'approved').length,
    done: tasks.filter(t => t.status === 'done').length,
    failed: tasks.filter(t => t.status === 'failed' || t.status === 'rejected').length,
  }

  // 报告历史列
  const reportColumns = [
    { title: '时间', dataIndex: 'created_at', key: 'ts', width: 150,
      render: (v: string) => <Text style={{ fontSize: 12 }}>{fmtLocalTime(v, '-', 'YYYY-MM-DD HH:mm:ss')}</Text> },
    { title: '服务', dataIndex: 'service_name', key: 'svc', width: 130,
      render: (v: string) => v && v !== '-' ? <Text code style={{ fontSize: 12 }}>{v}</Text> : <Text type='secondary'>-</Text> },
    { title: '类型', dataIndex: 'report_type', key: 'rt', width: 100,
      render: (v: string) => <Tag color={v === 'inspection' ? 'purple' : 'blue'}>{v === 'inspection' ? '巡检' : '报告'}</Tag> },
    { title: '健康判定', dataIndex: 'verdict', key: 'verdict', width: 110,
      render: (v: string) => {
        const c: Record<string, string> = { 健康: 'green', 关注: 'orange', 异常: 'red', unknown: 'default' }
        return <Tag color={c[v] || 'default'}>{v === 'unknown' ? '未知' : v}</Tag>
      } },
    { title: '风险分', dataIndex: 'risk_score', key: 'risk', width: 130,
      render: (v: number) => (
        <Tooltip title={`${((v || 0) * 100).toFixed(0)}%`}>
          <Progress percent={Math.round((v || 0) * 100)} size="small"
            strokeColor={v >= 0.7 ? '#ff4d4f' : v >= 0.4 ? '#faad14' : '#52c41a'} />
        </Tooltip>
      ) },
    { title: '摘要', dataIndex: 'summary', key: 'summary', ellipsis: true,
      render: (v: string) => <Text style={{ fontSize: 12 }}>{v || '-'}</Text> },
    { title: '操作', key: 'act', width: 90,
      render: (_: any, r: any) => (
        <a href={`/api/v1/ops/reports/${r.task_id}/download`} target='_blank' rel='noreferrer'>
          <Button size='small' type='link' icon={<DownloadOutlined />}>报告</Button>
        </a>
      ) },
  ]

  const services = [...new Set(reports.map((r: any) => r.service_name).filter((s: string) => s && s !== '-'))]

  return (
    <div>
      <Tabs
        activeKey={pageTab}
        onChange={setPageTab}
        items={[
          {
            key: 'tasks',
            label: <span><ToolOutlined /> 任务列表</span>,
            children: (<Card
        title={<Space><ToolOutlined style={{ color: '#1677ff' }} /> 任务工作台</Space>}
        extra={
          <Space>
            <Tag icon={<WifiOutlined />} color={wsStatus === 'connected' ? 'green' : 'default'}>
              {wsStatus === 'connected' ? '实时' : '轮询'}
            </Tag>
            <Button type='primary' icon={<PlusOutlined />} onClick={() => setCreateOpen(true)} size='small'>新建任务</Button>
            <Button icon={<ReloadOutlined />} onClick={fetchTasks} size='small' />
          </Space>
        }
        size='small' style={{ marginBottom: 12 }}
      >
        <Row gutter={24} style={{ marginBottom: 16 }}>
          <Col span={4}><Statistic title='待审批' value={counts.waiting} valueStyle={{ color: counts.waiting > 0 ? '#faad14' : '#999' }} /></Col>
          <Col span={4}><Statistic title='执行中' value={counts.running} valueStyle={{ color: counts.running > 0 ? '#1677ff' : '#999' }} /></Col>
          <Col span={4}><Statistic title='已完成' value={counts.done} valueStyle={{ color: '#52c41a' }} /></Col>
          <Col span={4}><Statistic title='失败' value={counts.failed} valueStyle={{ color: '#ff4d4f' }} /></Col>
          <Col span={8}><Statistic title='总计' value={counts.all} /></Col>
        </Row>

        <Table columns={columns}
          dataSource={tasks.filter(t => activeTab === 'all' || t.status.startsWith(activeTab))}
          rowKey='id' loading={loading} size='small' pagination={{ pageSize: 15, showTotal: t => `共 ${t} 条` }}
          locale={{ emptyText: <Empty description={<span>暂无任务<br /><Text type='secondary'>告警触发或手动创建后自动诊断为可执行方案</Text></span>} image={Empty.PRESENTED_IMAGE_SIMPLE} /> }}
          bordered={false}
          rowClassName={(record) => record.status === 'waiting' ? 'ant-table-row-warning' : ''}
        />
      </Card>),
          },
          {
            key: 'reports',
            label: <span><HistoryOutlined /> 报告历史</span>,
            children: (
              <Card
                title={<Space><FileTextOutlined /> 巡检报告历史</Space>}
                extra={
                  <Space>
                    <Select
                      allowClear
                      placeholder='按服务筛选'
                      style={{ width: 160 }}
                      value={serviceFilter || undefined}
                      onChange={(v) => setServiceFilter(v || '')}
                      options={services.map((s: string) => ({ value: s, label: s }))}
                    />
                    <Button icon={<ReloadOutlined />} onClick={() => { fetchReports(); fetchTrend() }} size='small' />
                  </Space>
                }
                size='small'
              >
                {trend.length > 0 && (
                  <div style={{ marginBottom: 20 }}>
                    <Paragraph style={{ marginBottom: 8 }}>
                      <Text strong><LineChartOutlined /> 近 14 天巡检趋势</Text>
                    </Paragraph>
                    <Row gutter={[12, 12]}>
                      {trend.map((t: any) => (
                        <Col span={6} key={t.date}>
                          <div style={{ background: '#fafafa', borderRadius: 6, padding: 10 }}>
                            <Text style={{ fontSize: 12, color: '#888' }}>{t.date}</Text>
                            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 4 }}>
                              <Text strong style={{ fontSize: 18 }}>{t.count}</Text>
                              <Text type='secondary' style={{ fontSize: 12 }}>份报告</Text>
                            </div>
                            <Tooltip title={`平均风险 ${Math.round((t.avg_risk || 0) * 100)}%`}>
                              <Progress percent={Math.round((t.avg_risk || 0) * 100)} size='small'
                                strokeColor={(t.avg_risk || 0) >= 0.7 ? '#ff4d4f' : (t.avg_risk || 0) >= 0.4 ? '#faad14' : '#52c41a'} />
                            </Tooltip>
                          </div>
                        </Col>
                      ))}
                    </Row>
                  </div>
                )}
                <Table columns={reportColumns}
                  dataSource={reports}
                  rowKey='task_id' loading={reportsLoading} size='small' pagination={{ pageSize: 15, showTotal: t => `共 ${t} 条` }}
                  locale={{ emptyText: <Empty description='暂无巡检报告' image={Empty.PRESENTED_IMAGE_SIMPLE} /> }}
                />
              </Card>
            ),
          },
        ]}
      />

      {/* Create Task Modal */}
      <Modal title='新建运维任务' open={createOpen} onCancel={() => setCreateOpen(false)} onOk={() => form.submit()} okText='创建' cancelText='取消'>
        <Form form={form} onFinish={createTask} layout='vertical' initialValues={{ source: 'manual' }}>
          <Form.Item name='source' label='来源'>
            <Select options={[{ value: 'manual', label: '手动' }, { value: 'alert', label: '告警模拟' }]} />
          </Form.Item>
          <Form.Item name='service' label='目标服务'>
            <Input placeholder='输入服务名' />
          </Form.Item>
          <Form.Item name='context' label='问题描述' rules={[{ required: true, message: '请描述问题' }]}>
            <Input.TextArea rows={3} placeholder='例: user-service 的 P99 延迟突增至 5 秒' />
          </Form.Item>
        </Form>
      </Modal>

      {/* Task Detail */}
      <Modal title={<Space><ThunderboltOutlined /> 任务详情</Space>} open={detailOpen} onCancel={() => setDetailOpen(false)} footer={null} width={720}>
        {selected && (
          <Space direction='vertical' style={{ width: '100%' }} size='middle'>
            <Row gutter={16}>
              <Col span={6}><Text type='secondary'>服务</Text><br /><Text strong>{selected.service || '-'}</Text></Col>
              <Col span={6}><Text type='secondary'>状态</Text><br /><Tag color={STATUS_MAP[selected.status]?.color}>{STATUS_MAP[selected.status]?.label}</Tag></Col>
              <Col span={6}><Text type='secondary'>来源</Text><br /><Tag>{(SOURCE_TAGS[selected.source] || {}).label || selected.source}</Tag></Col>
              <Col span={6}><Text type='secondary'>风险</Text><br /><Text>{(selected.risk_score ? `${(selected.risk_score * 100).toFixed(0)}%` : '-')}</Text></Col>
            </Row>
            <Paragraph><Text strong>诊断分析</Text></Paragraph>
            <pre style={{ fontSize: 12, background: 'var(--surface-2)', padding: 12, borderRadius: 6, maxHeight: 200, minHeight: 60, overflow: 'auto', whiteSpace: 'pre-wrap', color: selected.diagnosis ? 'var(--text)' : 'var(--text-muted)', margin: 0 }}>
              {selected.diagnosis || '（该任务尚未生成诊断结果，可点击"手动诊断"触发）'}
            </pre>
            {selected.plan && (
              <>
                <Paragraph><Text strong>执行计划</Text></Paragraph>
                <pre style={{ fontSize: 12, background: 'var(--surface-2)', padding: 12, borderRadius: 6, maxHeight: 200, overflow: 'auto', whiteSpace: 'pre-wrap', border: '1px solid var(--border)', color: 'var(--text)' }}>
                  {selected.plan}
                </pre>
              </>
            )}
            {selected.report && (
              <>
                <Paragraph><Text strong>执行报告</Text></Paragraph>
                <pre style={{ fontSize: 12, background: 'var(--surface-2)', padding: 12, borderRadius: 6, maxHeight: 200, overflow: 'auto', whiteSpace: 'pre-wrap', border: '1px solid var(--border)', color: 'var(--text)' }}>
                  {selected.report}
                </pre>
              </>
            )}
          </Space>
        )}
      </Modal>
    </div>
  )
}

export default Tasks
