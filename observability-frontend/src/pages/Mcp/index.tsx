import { useEffect, useState } from 'react'
import { Table, Button, Drawer, Form, Input, message, Empty, Space, Typography } from 'antd'
import { PlayCircleOutlined, ReloadOutlined } from '@ant-design/icons'
import { getMcpTools, callMcpTool } from '../../api/client'

interface ToolItem {
  name: string
  description?: string
  parameters?: Record<string, string>
}

export default function Mcp() {
  const [tools, setTools] = useState<ToolItem[]>([])
  const [loading, setLoading] = useState(false)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [current, setCurrent] = useState<ToolItem | null>(null)
  const [callResult, setCallResult] = useState('')
  const [calling, setCalling] = useState(false)
  const [form] = Form.useForm()

  const fetchTools = async () => {
    setLoading(true)
    try {
      const r = await getMcpTools()
      setTools((r.data?.tools) || [])
    } catch {
      message.error('获取 MCP 工具失败')
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => { fetchTools() }, [])

  const openDrawer = (t: ToolItem) => {
    setCurrent(t)
    setCallResult('')
    form.resetFields()
    setDrawerOpen(true)
  }

  const doCall = async () => {
    if (!current) return
    const args = form.getFieldsValue()
    setCalling(true)
    try {
      const r = await callMcpTool(current.name, args)
      setCallResult(JSON.stringify(r.data?.result ?? r.data, null, 2))
      message.success('调用成功')
    } catch {
      message.error('调用失败')
    } finally {
      setCalling(false)
    }
  }

  const params = current?.parameters ?? {}

  return (
    <div style={{ padding: 24 }}>
      <Space style={{ marginBottom: 16 }} size="large">
        <Typography.Title level={4} style={{ margin: 0 }}>MCP 工具</Typography.Title>
        <Button icon={<ReloadOutlined />} onClick={fetchTools}>刷新</Button>
      </Space>
      {tools.length === 0 && !loading ? (
        <Empty description="暂无 MCP 工具" />
      ) : (
        <Table
          rowKey="name"
          dataSource={tools}
          loading={loading}
          size="middle"
          pagination={false}
          columns={[
            { title: '工具', dataIndex: 'name', render: (t) => <b>{t}</b> },
            { title: '描述', dataIndex: 'description', render: (d) => d || '-' },
            {
              title: '操作', width: 120,
              render: (_, r) => (
                <Button size="small" type="primary" icon={<PlayCircleOutlined />} onClick={() => openDrawer(r)}>调用</Button>
              ),
            },
          ]}
        />
      )}
      <Drawer
        title={`调用 ${current?.name}`}
        width={520}
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        extra={<Button type="primary" loading={calling} onClick={doCall} icon={<PlayCircleOutlined />}>执行</Button>}
      >
        <Form form={form} layout="vertical">
          {Object.keys(params).length > 0 ? (
            Object.keys(params).map((k) => (
              <Form.Item key={k} label={`${k} (${params[k]})`} name={k}>
                <Input placeholder={`参数: ${k}`} />
              </Form.Item>
            ))
          ) : (
            <Form.Item label="参数">
              <Input.TextArea name="__args" placeholder="JSON 参数（可选）" />
            </Form.Item>
          )}
        </Form>
        {callResult && (
          <div>
            <Typography.Text strong>返回结果</Typography.Text>
            <pre
              style={{
                background: 'rgba(255,255,255,0.04)', padding: 12, borderRadius: 6,
                marginTop: 8, whiteSpace: 'pre-wrap', fontSize: 12,
              }}
            >{callResult}</pre>
          </div>
        )}
      </Drawer>
    </div>
  )
}
