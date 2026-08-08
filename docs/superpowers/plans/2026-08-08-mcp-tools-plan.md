# MCP 工具目录 + 调用测试页 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 `/mcp` 前端页（工具目录 + 调用测试），复用后端现有 `/api/v1/mcp/tools` 和 `/api/v1/mcp/call`。

**Architecture:** 纯前端。`pages/Mcp/index.tsx` 用 AntD Table + Drawer 展示工具目录 + 按 schema 渲染调用表单，调现有 MCP 接口。

**Tech Stack:** React, AntD, TS

## Global Constraints

- **零新增后端**：只复用 `GET /api/v1/mcp/tools`（返回 `{"tools":[...]}`）和 `POST /api/v1/mcp/call`（body `{name, args}`，返回 `{"result":...}`）
- 前端参数 schema 渲染复用 Skills 页已有模式
- `tsc --noEmit` + `npm run build` 通过
- 路由挂"智能运维"分组

---

### Task 1: client.ts 加 MCP API

**Files:**
- Modify: `observability-frontend/src/api/client.ts`

**Interfaces:**
- Consumes: 现有后端 `/api/v1/mcp/tools`、`/api/v1/mcp/call`
- Produces: `getMcpTools()`、`callMcpTool(name, args)` 供页面使用

- [ ] **Step 1: 在 client.ts 追加 MCP API**

在 `observability-frontend/src/api/client.ts` 末尾追加：

```ts
// ===== MCP =====
export const getMcpTools = () => api.get('/mcp/tools')
export const callMcpTool = (name: string, args: Record<string, any>) => api.post('/mcp/call', { name, args })
```

- [ ] **Step 2: 提交**

```bash
git add observability-frontend/src/api/client.ts
git commit -m "feat(web): MCP 工具 API 封装"
```

---

### Task 2: MCP 页面（工具目录 + 调用测试）

**Files:**
- Create: `observability-frontend/src/pages/Mcp/index.tsx`
- Modify: `observability-frontend/src/App.tsx`（注册路由）
- Test: `tsc --noEmit` + `npm run build`

**Interfaces:**
- Consumes: `getMcpTools()`、`callMcpTool(name, args)`（Task 1）
- Produces: `/mcp` 路由页面

- [ ] **Step 1: 创建 MCP 页面组件**

创建 `observability-frontend/src/pages/Mcp/index.tsx`：

```tsx
import { useEffect, useState } from 'react'
import { Table, Tag, Button, Drawer, Form, Input, message, Empty, Space, Typography } from 'antd'
import { PlayCircleOutlined, ReloadOutlined } from '@ant-design/icons'
import { getMcpTools, callMcpTool } from '../../api/client'

interface ToolItem { name: string; description?: string; parameters?: Record<string, any> }

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
    } finally { setLoading(false) }
  }
  useEffect(() => { fetchTools() }, [])

  const openDrawer = (t: ToolItem) => {
    setCurrent(t); setCallResult(''); form.resetFields(); setDrawerOpen(true)
  }

  const doCall = async () => {
    if (!current) return
    const args = form.getFieldsValue()
    setCalling(true)
    try {
      const r = await callMcpTool(current.name, args)
      setCallResult(JSON.stringify(r.data?.result ?? r.data, null, 2))
      message.success('调用成功')
    } catch { message.error('调用失败') } finally { setCalling(false) }
  }

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
          rowKey="name" dataSource={tools} loading={loading} size="middle"
          pagination={false}
          columns={[
            { title: '工具', dataIndex: 'name', render: (t) => <b>{t}</b> },
            { title: '描述', dataIndex: 'description', render: (d) => d || '-' },
            { title: '操作', width: 120, render: (_, r) => (
              <Button size="small" type="primary" icon={<PlayCircleOutlined />} onClick={() => openDrawer(r)}>调用</Button>
            )},
          ]}
        />
      )}
      <Drawer title={`调用 ${current?.name}`} width={520} open={drawerOpen} onClose={() => setDrawerOpen(false)}
        extra={<Button type="primary" loading={calling} onClick={doCall} icon={<PlayCircleOutlined />}>执行</Button>}>
        <Form form={form} layout="vertical">
          {(current?.parameters && Object.keys(current.parameters).length > 0)
            ? Object.keys(current.parameters).map((k) => (
                <Form.Item key={k} label={k} name={k}>
                  <Input placeholder={`参数: ${k}`} />
                </Form.Item>
              ))
            : <Form.Item label="参数"><Input.TextArea name="__args" placeholder="JSON 参数（可选）" /></Form.Item>}
        </Form>
        {callResult && (
          <div>
            <Typography.Text strong>返回结果</Typography.Text>
            <pre style={{ background: 'rgba(255,255,255,0.04)', padding: 12, borderRadius: 6, marginTop: 8,
              whiteSpace: 'pre-wrap', fontSize: 12 }}>{callResult}</pre>
          </div>
        )}
      </Drawer>
    </div>
  )
}
```

- [ ] **Step 2: 注册路由**

在 `observability-frontend/src/App.tsx`：
- 加 `import Mcp from './pages/Mcp'`
- 在 `<Routes>` 内加 `<Route path="/mcp" element={<Mcp />} />`
- 在侧边栏"智能运维"分组加 `{ key: '/mcp', icon: <ApiOutlined />, label: 'MCP' }`（若 `ApiOutlined` 未导入需加 import）

- [ ] **Step 3: tsc + build 验证**

Run: `cd observability-frontend && node_modules/.bin/tsc --noEmit && npm run build`
Expected: exit 0

- [ ] **Step 4: 提交**

```bash
git add observability-frontend/src
git commit -m "feat(web): MCP 工具目录 + 调用测试页"
```

---

### Task 3: 部署验证

- [ ] **Step 1: 重建前端镜像 + 部署**

按已有离线构建方式：基于本地旧 nginx 前端镜像 + 新 dist 构建 `observability-frontend:latest`，然后 `kubectl -n observability rollout restart deploy/frontend`。

- [ ] **Step 2: agent-browser 验证 /mcp**

Run: `agent-browser open "http://localhost:30253/mcp?t=$(date +%s)"`，登录后确认页面显示工具列表；点"调用"打开抽屉，填参执行返回结果。

---

## Self-Review

**1. Spec coverage:** 覆盖 MCP spec（工具目录 + 调用测试，零新增后端）。
**2. Placeholder scan:** 无 TBD/TODO；schema 渲染处理"无参数/JSON 参数"两种情形。
**3. Type consistency:** `getMcpTools`/`callMcpTool(name,args)` 与后端 `{name,args}` 契约一致。
