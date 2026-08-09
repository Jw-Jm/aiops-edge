import React, { useEffect, useState, useCallback } from 'react'
import {
  Card, Tabs, Table, Tag, Button, Modal, Form, Input, Select,
  InputNumber, Switch, Space, message, Popconfirm, Spin, Typography, Descriptions, Tooltip,
} from 'antd'
import {
  AlertOutlined, PlusOutlined, ReloadOutlined, BellOutlined,
  RobotOutlined, DownloadOutlined, FilePdfOutlined,
} from '@ant-design/icons'
import {
  getAlertRules, createAlertRule, updateAlertRule, deleteAlertRule,
  getAlertEvents, rcaAlertAnalysis,
} from '../../api/client'
import { fmtLocalTime } from '../../utils/date'

const { Text } = Typography

const { Option } = Select

// ---- Types ----

interface AlertRule {
  id: string
  name: string
  service: string
  type: 'threshold' | 'mutation' | 'anomaly' | 'forecast' | 'burn_rate' | 'metric_raw' | 'log' | 'trace_latency' | 'trace_error_rate'
  metric: string
  condition: string
  threshold: number
  duration: number
  severity: 'critical' | 'warning' | 'info'
  enabled: boolean
  cooldown?: number
  dampening?: number
}

interface AlertEvent {
  id: string
  rule_id: string
  rule_name: string
  service: string
  severity: 'critical' | 'warning' | 'info'
  message: string
  count: number
  first_timestamp: string
  last_timestamp: string
}

const severityColors: Record<string, string> = {
  critical: 'red',
  warning: 'orange',
  info: 'blue',
}

// ---- Main Component ----

const Alerts: React.FC = () => {
  const [activeTab, setActiveTab] = useState('rules')

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 16 }}>
        <AlertOutlined style={{ fontSize: 24, color: '#fa8c16' }} />
        <h2 style={{ margin: 0 }}>告警中心</h2>
      </div>
      <Tabs
        activeKey={activeTab}
        onChange={setActiveTab}
        items={[
          { key: 'rules', label: <span><BellOutlined /> 告警规则</span>, children: <AlertRulesTab /> },
          { key: 'events', label: <span><AlertOutlined /> 告警事件</span>, children: <AlertEventsTab /> },
        ]}
      />
    </div>
  )
}

// ---- Alert Rules Tab ----

const AlertRulesTab: React.FC = () => {
  const [rules, setRules] = useState<AlertRule[]>([])
  const [loading, setLoading] = useState(false)
  const [modalOpen, setModalOpen] = useState(false)
  const [editingRule, setEditingRule] = useState<AlertRule | null>(null)
  const [form] = Form.useForm()

  const fetchRules = useCallback(async () => {
    setLoading(true)
    try {
      const res = await getAlertRules()
      const data = res.data?.data || res.data
      setRules(Array.isArray(data) ? data : data?.rules || [])
    } catch {
      // Silently fail
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { fetchRules() }, [fetchRules])

  const openCreateModal = () => {
    setEditingRule(null)
    form.resetFields()
    form.setFieldsValue({
      type: 'threshold',
      metric: 'error_rate',
      severity: 'warning',
      duration: 5,
      threshold: 5,
      condition: '>',
      enabled: true,
    })
    setModalOpen(true)
  }

  const handleSave = async () => {
    try {
      const values = await form.validateFields()
      if (editingRule) {
        await updateAlertRule(editingRule.id, { ...values, enabled: editingRule.enabled })
        message.success('规则已更新')
      } else {
        await createAlertRule(values)
        message.success('规则已创建')
      }
      setModalOpen(false)
      fetchRules()
    } catch (err: any) {
      if (err?.errorFields) return // validation error
      message.error('保存失败')
    }
  }

  const handleDelete = async (id: string) => {
    try {
      await deleteAlertRule(id)
      message.success('规则已删除')
      fetchRules()
    } catch {
      message.error('删除失败')
    }
  }

  const handleToggle = async (rule: AlertRule) => {
    try {
      await updateAlertRule(rule.id, { enabled: !rule.enabled })
      message.success(rule.enabled ? '已禁用' : '已启用')
      fetchRules()
    } catch {
      message.error('操作失败')
    }
  }

  const columns = [
    { title: '规则名称', dataIndex: 'name', key: 'name', ellipsis: true },
    { title: '服务', dataIndex: 'service', key: 'service', render: (s: string) => <Tag>{s || '-'}</Tag> },
    {
      title: '类型', dataIndex: 'type', key: 'type', width: 100,
      render: (t: string) => t === 'threshold' ? <Tag color="blue">阈值</Tag> : <Tag color="purple">突变</Tag>,
    },
    {
      title: '指标', dataIndex: 'metric', key: 'metric', width: 120,
      render: (m: string) => {
        const labels: Record<string, string> = { error_rate: '错误率', latency_p99: 'P99延迟', call_count: '调用量' }
        return labels[m] || m
      },
    },
    {
      title: '条件', key: 'condition', width: 160,
      render: (_: unknown, r: AlertRule) => `${r.condition || '>'} ${r.threshold}${r.metric === 'error_rate' ? '%' : r.metric === 'latency_p99' ? 'ms' : ''} / ${r.duration}min`,
    },
    {
      title: '严重级别', dataIndex: 'severity', key: 'severity', width: 100,
      render: (s: string) => <Tag color={severityColors[s]}>{s?.toUpperCase()}</Tag>,
    },
    {
      title: '启用', dataIndex: 'enabled', key: 'enabled', width: 80,
      render: (enabled: boolean, record: AlertRule) => (
        <Switch size="small" checked={enabled} onChange={() => handleToggle(record)} />
      ),
    },
    {
      title: '操作', key: 'actions', width: 140,
      render: (_: unknown, record: AlertRule) => (
        <Space size={4}>
          <Button type="link" size="small" onClick={() => { setEditingRule(record); form.setFieldsValue(record); setModalOpen(true) }}>编辑</Button>
          <Popconfirm title="确定删除此规则？" onConfirm={() => handleDelete(record.id)} okText="确定" cancelText="取消">
            <Button type="link" danger size="small">删除</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ]

  return (
    <Card
      extra={
        <Space>
          <Button icon={<ReloadOutlined />} onClick={fetchRules} loading={loading}>刷新</Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreateModal}>新建规则</Button>
        </Space>
      }
    >
      <Spin spinning={loading}>
        <Table
          columns={columns}
          dataSource={rules}
          rowKey="id"
          pagination={{ pageSize: 10, showTotal: (t) => `共 ${t} 条规则` }}
          locale={{ emptyText: '暂无告警规则' }}
        />
      </Spin>

      <Modal
        title={editingRule ? '编辑告警规则' : '新建告警规则'}
        open={modalOpen}
        onOk={handleSave}
        onCancel={() => setModalOpen(false)}
        okText="保存"
        cancelText="取消"
        width={560}
      >
        <Form form={form} layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item name="name" label="规则名称" rules={[{ required: true, message: '请输入规则名称' }]}>
            <Input placeholder="例如：订单服务高错误率告警" />
          </Form.Item>
          <Form.Item name="service" label="服务名称" rules={[{ required: true, message: '请输入服务名称' }]}>
            <Input placeholder="例如：order-service" />
          </Form.Item>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
            <Form.Item name="type" label="告警类型" rules={[{ required: true }]}>
              <Select>
                <Option value="threshold">阈值告警</Option>
                <Option value="mutation">突变告警</Option>
                <Option value="anomaly">异常检测</Option>
                <Option value="forecast">预测偏差</Option>
                <Option value="burn_rate">燃尽率</Option>
                <Option value="metric_raw">原始指标</Option>
                <Option value="log">日志</Option>
                <Option value="trace_latency">链路延迟</Option>
                <Option value="trace_error_rate">链路错误率</Option>
              </Select>
            </Form.Item>
            <Form.Item name="metric" label="监控指标" rules={[{ required: true }]}>
              <Select>
                <Option value="error_rate">错误率</Option>
                <Option value="latency_p99">P99 延迟</Option>
                <Option value="call_count">调用量</Option>
              </Select>
            </Form.Item>
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 16 }}>
            <Form.Item name="condition" label="条件" rules={[{ required: true }]}>
              <Select>
                <Option value=">">&gt;</Option>
                <Option value=">=">&gt;=</Option>
                <Option value="<">&lt;</Option>
                <Option value="<=">&lt;=</Option>
              </Select>
            </Form.Item>
            <Form.Item name="threshold" label="阈值" rules={[{ required: true }]}>
              <InputNumber min={0} style={{ width: '100%' }} />
            </Form.Item>
            <Form.Item name="duration" label="持续时间(分钟)" rules={[{ required: true }]}>
              <InputNumber min={1} max={60} style={{ width: '100%' }} />
            </Form.Item>
          </div>
          <Form.Item name="severity" label="严重级别" rules={[{ required: true }]}>
            <Select style={{ width: 200 }}>
              <Option value="critical">严重 (Critical)</Option>
              <Option value="warning">警告 (Warning)</Option>
              <Option value="info">信息 (Info)</Option>
            </Select>
          </Form.Item>
          <div style={{ display: 'flex', gap: 12 }}>
            <Form.Item name="cooldown" label="冷却(分钟)" tooltip="冷却期内不重复告警，0=不启用">
              <InputNumber min={0} style={{ width: 140 }} placeholder="0" />
            </Form.Item>
            <Form.Item name="dampening" label="连续确认" tooltip="连续 N 次触发才告警，1=立即">
              <InputNumber min={1} style={{ width: 140 }} placeholder="1" />
            </Form.Item>
          </div>
        </Form>
      </Modal>
    </Card>
  )
}

// ---- Alert Events Tab ----

const AlertEventsTab: React.FC = () => {
  const [events, setEvents] = useState<AlertEvent[]>([])
  const [loading, setLoading] = useState(false)
  const [serviceFilter, setServiceFilter] = useState<string>('')
  const [severityFilter, setSeverityFilter] = useState<string>('')

  // RCA 联动状态
  const [rcaEvent, setRcaEvent] = useState<AlertEvent | null>(null)
  const [rcaOpen, setRcaOpen] = useState(false)
  const [rcaLoading, setRcaLoading] = useState(false)
  const [rcaResult, setRcaResult] = useState<any>(null)

  const fetchEvents = useCallback(async () => {
    setLoading(true)
    try {
      const params: Record<string, string> = {}
      if (serviceFilter) params.service = serviceFilter
      if (severityFilter) params.severity = severityFilter
      const res = await getAlertEvents(params)
      const data = res.data?.data || res.data
      setEvents(Array.isArray(data) ? data : data?.events || [])
    } catch {
      // Silently fail
    } finally {
      setLoading(false)
    }
  }, [serviceFilter, severityFilter])

  useEffect(() => { fetchEvents() }, [fetchEvents])

  // Auto-refresh every 30 seconds
  useEffect(() => {
    const interval = setInterval(fetchEvents, 30000)
    return () => clearInterval(interval)
  }, [fetchEvents])

  const handleRCA = async (record: AlertEvent) => {
    setRcaEvent(record)
    setRcaOpen(true)
    setRcaResult(null)
    setRcaLoading(true)
    try {
      const res = await rcaAlertAnalysis({
        service: record.service || 'kubernetes',
        rule_id: record.rule_id,
        rule_name: record.rule_name,
        severity: record.severity,
        message: record.message,
        count: record.count,
        first_timestamp: record.first_timestamp,
        last_timestamp: record.last_timestamp,
      })
      setRcaResult(res.data || res)
    } catch (err: any) {
      message.error(err?.response?.data?.detail || '根因分析失败')
      setRcaResult(null)
    } finally {
      setRcaLoading(false)
    }
  }

  const exportRCA = async (fmt: 'markdown' | 'pdf') => {
    if (!rcaEvent) return
    const payload = {
      service: rcaEvent.service || 'kubernetes',
      rule_id: rcaEvent.rule_id,
      rule_name: rcaEvent.rule_name,
      severity: rcaEvent.severity,
      message: rcaEvent.message,
      count: rcaEvent.count,
      first_timestamp: rcaEvent.first_timestamp,
      last_timestamp: rcaEvent.last_timestamp,
    }
    try {
      const res = await fetch(`/api/v1/ops/rca/alert/export?format=${fmt}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Tenant-ID': 'default' },
        body: JSON.stringify(payload),
      })
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      if (fmt === 'pdf') {
        // PDF: 打开 HTML 供浏览器打印
        const html = await res.text()
        const win = window.open('', '_blank')
        if (win) { win.document.write(html); win.document.close() }
      } else {
        // Markdown: 触发下载
        const blob = await res.blob()
        const url = URL.createObjectURL(blob)
        const a = document.createElement('a')
        a.href = url
        a.download = `rca-report-${rcaEvent.rule_id || rcaEvent.service || 'unknown'}.md`
        a.click()
        URL.revokeObjectURL(url)
      }
    } catch (err: any) {
      message.error(err?.message || '导出失败')
    }
  }

  const columns = [
    {
      title: '最近触发', dataIndex: 'last_timestamp', key: 'last_timestamp', width: 170,
      render: (v: string) => fmtLocalTime(v),
    },
    {
      title: '严重级别', dataIndex: 'severity', key: 'severity', width: 100,
      render: (s: string) => <Tag color={severityColors[s]}>{s?.toUpperCase()}</Tag>,
    },
    { title: '服务', dataIndex: 'service', key: 'service', width: 140, render: (s: string) => <Tag>{s || '-'}</Tag> },
    {
      title: '规则名称', dataIndex: 'rule_name', key: 'rule_name', ellipsis: true,
      render: (v: string, record: AlertEvent) => (
        <a onClick={() => { window.location.href = `/alerts/incidents/${record.id}` }} style={{ color: '#60a5fa' }}>{v}</a>
      ),
    },
    {
      title: '次数', dataIndex: 'count', key: 'count', width: 90, align: 'center' as const,
      render: (v: number) => <Tag color={v >= 10 ? 'red' : v >= 5 ? 'orange' : 'blue'}>{v || 0}</Tag>,
    },
    {
      title: '消息', dataIndex: 'message', key: 'message',
      render: (v: string) => v ? (
        <Tooltip title={v}>
          <Typography.Paragraph
            style={{ marginBottom: 0, whiteSpace: 'normal', wordBreak: 'break-word' }}
            ellipsis={{ rows: 2, expandable: false, tooltip: v }}
          >
            {v}
          </Typography.Paragraph>
        </Tooltip>
      ) : '-',
    },
    {
      title: '操作', key: 'actions', width: 120, align: 'center' as const,
      render: (_: unknown, record: AlertEvent) => (
        <Button
          type="link"
          size="small"
          icon={<RobotOutlined />}
          onClick={() => handleRCA(record)}
        >根因分析</Button>
      ),
    },
  ]

  // Extract unique services for filter
  const services = [...new Set(events.map(e => e.service).filter(Boolean))]

  // 解析 RCA 结果展示
  const mode = rcaResult?.mode || 'deterministic'
  const detResult = rcaResult?.result || {}
  const hypResult = detResult.hypothesis_result

  return (
    <Card
      extra={
        <Space>
          <Select
            allowClear
            placeholder="按服务筛选"
            style={{ width: 160 }}
            value={serviceFilter || undefined}
            onChange={(v) => setServiceFilter(v || '')}
            options={services.map(s => ({ value: s, label: s }))}
          />
          <Select
            allowClear
            placeholder="按级别筛选"
            style={{ width: 130 }}
            value={severityFilter || undefined}
            onChange={(v) => setSeverityFilter(v || '')}
            options={[
              { value: 'critical', label: '严重' },
              { value: 'warning', label: '警告' },
              { value: 'info', label: '信息' },
            ]}
          />
          <Button icon={<ReloadOutlined />} onClick={fetchEvents} loading={loading}>刷新</Button>
        </Space>
      }
    >
      <Spin spinning={loading}>
        <Table
          columns={columns}
          dataSource={events}
          rowKey="id"
          pagination={{ pageSize: 10, showTotal: (t) => `共 ${t} 条事件` }}
          locale={{ emptyText: '暂无告警事件' }}
        />
      </Spin>

      <Modal
        title={<Space><RobotOutlined style={{ color: '#722ed1' }} /> AI 根因分析{rcaEvent ? ` · ${rcaEvent.rule_name}` : ''}</Space>}
        open={rcaOpen}
        onCancel={() => setRcaOpen(false)}
        footer={
          <Space>
            {rcaResult && rcaEvent && (
              <>
                <Button icon={<DownloadOutlined />} onClick={() => exportRCA('markdown')}>导出 Markdown</Button>
                <Button icon={<FilePdfOutlined />} onClick={() => exportRCA('pdf')}>导出 PDF</Button>
              </>
            )}
            <Button onClick={() => setRcaOpen(false)}>关闭</Button>
          </Space>
        }
        width={760}
        destroyOnClose
      >
        <Spin spinning={rcaLoading}>
          {rcaEvent && !rcaLoading && (
            <Descriptions size="small" column={2} style={{ marginBottom: 16 }}>
              <Descriptions.Item label="告警规则">{rcaEvent.rule_name}</Descriptions.Item>
              <Descriptions.Item label="级别">
                <Tag color={severityColors[rcaEvent.severity]}>{rcaEvent.severity?.toUpperCase()}</Tag>
              </Descriptions.Item>
              <Descriptions.Item label="触发次数">{rcaEvent.count}</Descriptions.Item>
              <Descriptions.Item label="最近触发">{fmtLocalTime(rcaEvent.last_timestamp)}</Descriptions.Item>
            </Descriptions>
          )}

          {rcaResult ? (
            <div>
              <Tag color={mode === 'hypothesis_engine' ? 'purple' : 'blue'}>
                模式：{mode === 'hypothesis_engine' ? '假设引擎（深度分析）' : '确定性分析'}
              </Tag>

              {/* 根因结论 */}
              <div style={{ margin: '12px 0', padding: 12, background: '#f6f6f6', borderRadius: 6 }}>
                <Text strong>根因服务：</Text>
                <Text>{detResult.root_cause_service || '未知'}</Text>
                <div style={{ marginTop: 4 }}>
                  <Text strong>因果方向：</Text>
                  <Text>{detResult.causality_direction || '—'}</Text>
                </div>
                <div style={{ marginTop: 4 }}>
                  <Text strong>置信度：</Text>
                  <Text type={detResult.confidence >= 0.7 ? 'success' : 'warning'}>
                    {(detResult.confidence ?? 0).toFixed(2)}
                  </Text>
                </div>
                {detResult.recommendation && (
                  <div style={{ marginTop: 4 }}>
                    <Text strong>建议：</Text>
                    <Text>{detResult.recommendation}</Text>
                  </div>
                )}
                {rcaResult.message && (
                  <div style={{ marginTop: 4 }}>
                    <Text type="secondary">{rcaResult.message}</Text>
                  </div>
                )}
              </div>

              {/* 证据链 */}
              {Array.isArray(detResult.evidence_chain) && detResult.evidence_chain.length > 0 && (
                <>
                  <Text strong>证据链：</Text>
                  <ul style={{ paddingLeft: 20, marginTop: 8 }}>
                    {detResult.evidence_chain.map((ev: any, i: number) => (
                      <li key={i} style={{ marginBottom: 4 }}>
                        <Tag color="green">{ev.layer}</Tag>
                        {ev.finding}
                      </li>
                    ))}
                  </ul>
                </>
              )}

              {/* 假设引擎详情 */}
              {hypResult && (
                <>
                  <DividerText />
                  <Text strong>假设引擎证伪结论：</Text>
                  <div style={{ marginTop: 8 }}>
                    {hypResult.best_hypothesis ? (
                      <>
                        <Text type="success">最佳假设：</Text>
                        <Text>{hypResult.best_hypothesis.hypothesis}</Text>
                        <div style={{ marginTop: 4 }}>
                          <Text strong>置信度：</Text>
                          <Text>{(hypResult.best_hypothesis.confidence ?? 0).toFixed(2)}</Text>
                        </div>
                      </>
                    ) : (
                      <Text type="warning">未确认任何假设（所有假设被证伪或不确定）</Text>
                    )}
                  </div>

                  {hypResult.evidence_log && hypResult.evidence_log.length > 0 && (
                    <>
                      <div style={{ margin: '12px 0 8px' }}>
                        <Text strong>检查证据：</Text>
                      </div>
                      <Table
                        size="small"
                        rowKey={(r: any, i?: number) => `${r.hypothesis}-${i}`}
                        pagination={false}
                        dataSource={hypResult.evidence_log}
                        columns={[
                          { title: '假设', dataIndex: 'hypothesis', ellipsis: true },
                          { title: '集群检查', dataIndex: 'check', ellipsis: true },
                          {
                            title: '判定', dataIndex: 'verdict', width: 110,
                            render: (v: string) => {
                              const c: Record<string, string> = {
                                confirm: 'green', falsify: 'red', inconclusive: 'orange',
                              }
                              const label: Record<string, string> = {
                                confirm: '支持', falsify: '证伪', inconclusive: '不确定',
                              }
                              return <Tag color={c[v] || 'default'}>{label[v] || v}</Tag>
                            },
                          },
                          { title: '置信度', dataIndex: 'confidence', width: 90, render: (v: number) => (v ?? 0).toFixed(2) },
                        ]}
                      />
                    </>
                  )}
                </>
              )}
            </div>
          ) : (
            !rcaLoading && <Text type="secondary">暂无分析结果，请重试。</Text>
          )}
        </Spin>
      </Modal>
    </Card>
  )
}

const DividerText: React.FC = () => (
  <div style={{ borderTop: '1px solid #f0f0f0', margin: '16px 0' }} />
)

export default Alerts
