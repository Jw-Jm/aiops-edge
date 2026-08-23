import React, { useEffect, useState } from 'react'
import { Alert, Button, Card, Col, Descriptions, Row, Space, Spin, Tag, Typography } from 'antd'
import { useLocation, useNavigate, useParams } from 'react-router-dom'
import { getRun, getRunEvidence, RunEvidence } from '../../api/client'
import { PageHeader } from '../../components/ui/PageKit'

const { Text } = Typography

// Evidence 详情页（只读）：GET /api/v1/ai/runs/:runId/evidences/:evidenceId
// tenant+cluster+run 三元授权；403 → scope 拒绝访问，404 → 证据不存在。
// tenant/cluster 来源：location.state（列表页跳转携带）优先，否则经 getRun 回填。

interface LocationState {
  tenant_id?: string
  cluster_id?: string
}

type LoadError = 'forbidden' | 'not_found' | null

const EvidenceDetailView: React.FC = () => {
  const { runId, evidenceId } = useParams<{ runId: string; evidenceId: string }>()
  const navigate = useNavigate()
  const location = useLocation()
  const state = (location.state ?? {}) as LocationState

  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<LoadError>(null)
  const [evidence, setEvidence] = useState<RunEvidence | null>(null)
  const [scope, setScope] = useState<{ tenant_id: string; cluster_id: string }>({
    tenant_id: state.tenant_id ?? '',
    cluster_id: state.cluster_id ?? '',
  })

  useEffect(() => {
    if (!runId || !evidenceId) return
    let cancelled = false
    const load = async () => {
      setLoading(true)
      setError(null)
      try {
        let tenant = state.tenant_id ?? ''
        let cluster = state.cluster_id ?? ''
        if (!tenant || !cluster) {
          // 深链直达时缺 scope → 经 Run 详情回填
          const r = (await getRun(runId)).data?.run
          if (!r) { if (!cancelled) setError('not_found'); return }
          tenant = r.tenant_id ?? ''
          cluster = r.primary_cluster_id ?? ''
        }
        if (cancelled) return
        setScope({ tenant_id: tenant, cluster_id: cluster })
        const resp = await getRunEvidence(runId, evidenceId, { tenant_id: tenant, cluster_id: cluster })
        if (cancelled) return
        setEvidence(resp.data?.evidence ?? null)
      } catch (e) {
        if (cancelled) return
        const status = (e as { response?: { status?: number } })?.response?.status
        setError(status === 403 ? 'forbidden' : 'not_found')
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    load()
    return () => { cancelled = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runId, evidenceId])

  const factBody = String(evidence?.fact ?? '')
  const metaEntries = Object.entries(evidence ?? {}).filter(([k]) => k !== 'fact')

  return (
    <div>
      <PageHeader
        title="证据详情"
        desc={`Run ${runId} · Evidence ${evidenceId}`}
        actions={
          <Space>
            <Button onClick={() => navigate(`/investigation/${runId}`)}>返回调查</Button>
            <Button onClick={() => window.history.back()}>返回</Button>
          </Space>
        }
      />
      {loading && <div style={{ textAlign: 'center', padding: 80 }}><Spin size="large" /></div>}
      {!loading && error === 'forbidden' && (
        <Alert type="warning" showIcon message="scope 拒绝访问"
          description="当前 tenant/cluster 与该证据的归属不一致（tenant+cluster+run 三元授权失败）。" />
      )}
      {!loading && error === 'not_found' && (
        <Alert type="error" showIcon message="证据不存在"
          description="Run 或证据未注册（内存态登记，进程重启后即失）。" />
      )}
      {!loading && !error && evidence && (
        <Row>
          <Col span={24}>
            <Card title="Evidence 元数据" size="small">
              <Descriptions size="small" column={2} bordered>
                <Descriptions.Item label="Tenant">{scope.tenant_id}</Descriptions.Item>
                <Descriptions.Item label="Cluster">{scope.cluster_id}</Descriptions.Item>
                {metaEntries.map(([k, v]) => (
                  <Descriptions.Item key={k} label={k}>
                    {k === 'type' || k === 'layer' ? <Tag>{String(v ?? '-')}</Tag> : <Text>{formatValue(v)}</Text>}
                  </Descriptions.Item>
                ))}
              </Descriptions>
            </Card>
            <Card title="Fact（事实内容）" size="small" style={{ marginTop: 16 }}>
              <Text style={{ whiteSpace: 'pre-wrap' }}>{factBody || '—'}</Text>
            </Card>
          </Col>
        </Row>
      )}
    </div>
  )
}

function formatValue(v: unknown): string {
  if (v === null || v === undefined || v === '') return '-'
  if (typeof v === 'object') return JSON.stringify(v)
  return String(v)
}

export default EvidenceDetailView
