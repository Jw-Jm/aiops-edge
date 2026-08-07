import React, { useEffect, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { Card, Descriptions, Tag, Button, Spin, Timeline, Space, message } from 'antd'
import { getAlertEventByID, ackAlertEvent, resolveAlertEvent, rcaAlertAnalysis } from '../../api/client'
import { fmtLocalTime } from '../../utils/date'

const STATUS_COLOR: Record<string, string> = { firing: 'red', acknowledged: 'orange', resolved: 'green' }

const IncidentDetail: React.FC = () => {
  const { id } = useParams()
  const navigate = useNavigate()
  const [ev, setEv] = useState<any>(null)
  const [rca, setRca] = useState('')
  const [loading, setLoading] = useState(true)

  const load = async () => {
    setLoading(true)
    try {
      const r = await getAlertEventByID(id!)
      setEv(r?.data)
    } catch {
      message.error('加载告警失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id])

  const onAck = async () => {
    try {
      await ackAlertEvent(id!)
      message.success('已确认')
      load()
    } catch {
      message.error('确认失败')
    }
  }
  const onResolve = async () => {
    try {
      await resolveAlertEvent(id!)
      message.success('已解决')
      load()
    } catch {
      message.error('解决失败')
    }
  }
  const onRCA = async () => {
    if (!ev) return
    setRca('分析中...')
    try {
      const res = await rcaAlertAnalysis({
        service: ev.service || 'kubernetes',
        rule_id: ev.rule_id,
        rule_name: ev.rule_name,
      })
      setRca(res?.data?.analysis || res?.data?.result || '无分析结果')
    } catch {
      setRca('根因分析失败')
    }
  }

  if (loading) return <Spin />
  if (!ev) return <div style={{ color: 'var(--text-muted)' }}>未找到告警</div>
  return (
    <Card
      title={`告警详情 · ${ev.rule_name}`}
      style={{ background: 'var(--surface)', borderColor: 'var(--border)', borderRadius: 10 }}
    >
      <Space style={{ marginBottom: 16 }}>
        <Tag color={STATUS_COLOR[ev.status] || 'blue'}>{ev.status}</Tag>
        <Button size="small" onClick={onAck} disabled={ev.status !== 'firing'}>确认</Button>
        <Button size="small" onClick={onResolve} disabled={ev.status === 'resolved'}>解决</Button>
        <Button size="small" onClick={onRCA} type="primary">AI 根因分析</Button>
        <Button size="small" onClick={() => navigate('/alerts')}>返回</Button>
      </Space>
      <Descriptions column={2} size="small">
        <Descriptions.Item label="服务">{ev.service}</Descriptions.Item>
        <Descriptions.Item label="严重级别">{ev.severity}</Descriptions.Item>
        <Descriptions.Item label="触发次数">{ev.count}</Descriptions.Item>
        <Descriptions.Item label="首次触发">{fmtLocalTime(ev.first_timestamp)}</Descriptions.Item>
        <Descriptions.Item label="最近触发">{fmtLocalTime(ev.last_timestamp)}</Descriptions.Item>
        <Descriptions.Item label="消息">{ev.message}</Descriptions.Item>
      </Descriptions>
      <Timeline style={{ marginTop: 16 }}>
        <Timeline.Item color="red">firing · {fmtLocalTime(ev.first_timestamp)}</Timeline.Item>
        {ev.acknowledged_at && (
          <Timeline.Item color="orange">acknowledged by {ev.acknowledged_by} · {fmtLocalTime(ev.acknowledged_at)}</Timeline.Item>
        )}
        {ev.resolved_at && (
          <Timeline.Item color="green">resolved by {ev.resolved_by} · {fmtLocalTime(ev.resolved_at)}</Timeline.Item>
        )}
      </Timeline>
      {rca && (
        <Card size="small" style={{ marginTop: 12, background: 'var(--surface-2)' }}>
          <pre style={{ color: 'var(--text)', whiteSpace: 'pre-wrap' }}>{rca}</pre>
        </Card>
      )}
    </Card>
  )
}

export default IncidentDetail
