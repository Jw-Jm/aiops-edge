import React, { useEffect, useState } from 'react'
import { Card, Table, Tag, Button, Drawer, Descriptions, Form, Input, message } from 'antd'
import { listSkills, getSkill, executeSkill } from '../../api/client'

interface Skill {
  key: string
  name: string
  description: string
  tools: any[]
}

const Skills: React.FC = () => {
  const [skills, setSkills] = useState<Skill[]>([])
  const [loading, setLoading] = useState(true)
  const [detail, setDetail] = useState<any>(null)
  const [runOpen, setRunOpen] = useState(false)
  const [runParams, setRunParams] = useState<Record<string, any>>({})
  const [runResult, setRunResult] = useState('')

  const load = async () => {
    setLoading(true)
    try {
      const r = await listSkills()
      setSkills(r?.data?.skills || [])
    } catch {
      message.error('加载技能失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const openDetail = async (key: string) => {
    try {
      const r = await getSkill(key)
      setDetail(r?.data)
      setRunParams({})
      setRunResult('')
    } catch {
      message.error('加载技能详情失败')
    }
  }

  const doRun = async () => {
    if (!detail) return
    try {
      const r = await executeSkill(detail.key, runParams)
      setRunResult(JSON.stringify(r?.data, null, 2))
    } catch {
      message.error('执行失败')
    }
  }

  const columns = [
    { title: '技能', dataIndex: 'name', key: 'name' },
    { title: '描述', dataIndex: 'description', key: 'description', ellipsis: true },
    { title: '工具数', dataIndex: 'tools', key: 'tools', render: (v: any[]) => v?.length || 0 },
    {
      title: '操作',
      key: 'op',
      render: (_: any, s: Skill) => <a onClick={() => openDetail(s.key)} style={{ color: '#60a5fa' }}>详情/执行</a>,
    },
  ]

  return (
    <Card
      title="技能目录"
      style={{ background: 'var(--surface)', borderColor: 'var(--border)', borderRadius: 10 }}
      extra={<Button size="small" onClick={load}>刷新</Button>}
    >
      <Table rowKey="key" columns={columns} dataSource={skills} loading={loading} pagination={false} size="small" />
      <Drawer title={detail?.name} open={!!detail} onClose={() => setDetail(null)} width={520}>
        <Descriptions column={1} size="small">
          <Descriptions.Item label="Key">{detail?.key}</Descriptions.Item>
          <Descriptions.Item label="描述">{detail?.description}</Descriptions.Item>
          <Descriptions.Item label="工具">
            {(detail?.tools || []).map((t: any) => (
              <Tag key={t.name} style={{ margin: 2 }}>
                {t.name}
                {t.cls ? <Tag color={t.cls === 'safe' ? 'green' : t.cls === 'mutating' ? 'orange' : 'red'} style={{ marginLeft: 4, fontSize: 11 }}>{t.cls === 'safe' ? '只读' : t.cls === 'mutating' ? '可变更' : '危险'}</Tag> : null}
                {t.requires_approval ? ' (审批)' : ''}
              </Tag>
            ))}
          </Descriptions.Item>
        </Descriptions>
        <div style={{ marginTop: 16 }}>
          <Button type="primary" onClick={() => setRunOpen(true)}>执行</Button>
        </div>
      </Drawer>
      <Drawer title={`执行 ${detail?.name}`} open={runOpen} onClose={() => setRunOpen(false)} width={480}>
        <Form layout="vertical">
          {(detail?.tools || [])
            .flatMap((t: any) => (t.params || []).map((pn: string) => ({ pn, t })))
            .map(({ pn, t }: any) => (
              <Form.Item key={`${t.name}-${pn}`} label={`${pn}（${t.name}）`}>
                <Input value={runParams[pn] || ''} onChange={(e) => setRunParams({ ...runParams, [pn]: e.target.value })} />
              </Form.Item>
            ))}
          <Form.Item>
            <Button type="primary" onClick={doRun}>执行</Button>
          </Form.Item>
        </Form>
        {runResult && (
          <pre style={{ background: 'var(--surface-2)', padding: 12, borderRadius: 8, color: 'var(--text)', fontSize: 12, whiteSpace: 'pre-wrap' }}>
            {runResult}
          </pre>
        )}
      </Drawer>
    </Card>
  )
}

export default Skills
