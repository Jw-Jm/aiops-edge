import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { Alert, Button, Card, Empty, Space, Table, Tag, Typography, message } from 'antd'
import { getKnowledgeIndexStatus, reindexKnowledge } from '../../api/knowledge'
import { useAuthStore } from '../../store/authStore'

interface KnowledgeIndexOperationsProps {
  clusterId?: string
  embedded?: boolean
}

interface IndexState {
  knowledgeId: string
  versionId: string
  status: string
  attempt: number | null
  lastError: string
  nextRetryAt: string
  indexedAt: string
}

function projectState(value: Record<string, unknown>): IndexState {
  return {
    knowledgeId: String(value.knowledge_id ?? value.knowledgeId ?? '未提供'),
    versionId: String(value.version_id ?? value.versionId ?? '未提供'),
    status: String(value.status ?? 'unknown'),
    attempt: typeof value.attempt === 'number' ? value.attempt : null,
    lastError: String(value.last_error ?? value.lastError ?? ''),
    nextRetryAt: String(value.next_retry_at ?? value.nextRetryAt ?? ''),
    indexedAt: String(value.indexed_at ?? value.indexedAt ?? ''),
  }
}

function statusLabel(status: string): { text: string; color?: string } {
  if (status === 'indexed' || status === 'success') return { text: '已索引', color: 'green' }
  if (status === 'pending' || status === 'queued') return { text: '待处理', color: 'blue' }
  if (status === 'failed' || status === 'error') return { text: '失败', color: 'red' }
  return { text: status || '未知', color: 'default' }
}

const KnowledgeIndexOperations: React.FC<KnowledgeIndexOperationsProps> = ({ clusterId, embedded = false }) => {
  const role = useAuthStore((state) => state.role)
  const [rows, setRows] = useState<IndexState[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [retrying, setRetrying] = useState(false)
  const canRetry = role === 'admin'

  const load = useCallback(async () => {
    if (!clusterId) {
      setRows([])
      setError('')
      return
    }
    setLoading(true)
    setError('')
    try {
      const response = await getKnowledgeIndexStatus(clusterId)
      setRows((response.items ?? []).map((item) => projectState(item)))
    } catch (reason: any) {
      setRows([])
      setError(reason?.response?.data?.error || reason?.message || '知识索引状态读取失败')
    } finally {
      setLoading(false)
    }
  }, [clusterId])

  useEffect(() => { void load() }, [load])

  const failed = rows.filter((row) => ['failed', 'error'].includes(row.status))
  const columns = useMemo(() => [
    { title: 'Knowledge', dataIndex: 'knowledgeId', key: 'knowledgeId', render: (value: string) => <Typography.Text code>{value}</Typography.Text> },
    { title: 'Version', dataIndex: 'versionId', key: 'versionId', render: (value: string) => <Typography.Text code>{value}</Typography.Text> },
    { title: '状态', dataIndex: 'status', key: 'status', render: (value: string) => { const item = statusLabel(value); return <Tag color={item.color}>{item.text}</Tag> } },
    { title: 'Attempt', dataIndex: 'attempt', key: 'attempt', render: (value: number | null) => value == null ? '未提供' : value },
    { title: '最近错误', dataIndex: 'lastError', key: 'lastError', render: (value: string) => value || <Typography.Text type="secondary">—</Typography.Text> },
    { title: '下次重试', dataIndex: 'nextRetryAt', key: 'nextRetryAt', render: (value: string) => value || <Typography.Text type="secondary">—</Typography.Text> },
  ], [])

  const retry = async () => {
    if (!clusterId || !canRetry) return
    setRetrying(true)
    try {
      await reindexKnowledge(clusterId)
      message.success('已提交索引重试')
      await load()
    } catch (reason: any) {
      message.error(reason?.response?.data?.error || reason?.message || '索引重试提交失败')
    } finally {
      setRetrying(false)
    }
  }

  const body = !clusterId ? (
    <Empty description="请选择集群后查看知识索引任务" />
  ) : (
    <>
      {error && <Alert type="warning" showIcon message="索引状态暂不可用" description={error} style={{ marginBottom: 12 }} />}
      {failed.length > 0 && <Alert type="warning" showIcon message="正文仍可浏览" description="语义索引存在失败项；知识正文与版本仍可正常查看，管理员可提交重试。" style={{ marginBottom: 12 }} />}
      <Space style={{ marginBottom: 12 }}>
        <Typography.Text type="secondary">集群：{clusterId}</Typography.Text>
        <Button size="small" onClick={() => void load()} loading={loading}>刷新</Button>
        {canRetry && <Button size="small" type="primary" onClick={() => void retry()} loading={retrying} disabled={failed.length === 0}>重试索引</Button>}
      </Space>
      <Table<IndexState> rowKey={(row) => `${row.knowledgeId}:${row.versionId}`} size="small" loading={loading} dataSource={rows} columns={columns} pagination={{ pageSize: 20 }} locale={{ emptyText: <Empty description="暂无索引任务" /> }} />
    </>
  )

  return embedded ? body : <Card title="知识索引任务" extra={canRetry ? <Tag color="blue">管理员可重试</Tag> : <Tag>只读</Tag>}>{body}</Card>
}

export default KnowledgeIndexOperations
