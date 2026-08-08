# AIOps 平台 — UI/视觉设计师完整审核报告（含弹出窗口）

**审核角色：** UI / 视觉设计师（Senior Visual Designer）  
**审核日期：** 2026-08-08  
**审核范围：** 14 个一级页面 + 6 个二级/弹窗/抽屉页面（共 20 个视图）  
**审核方法：** agent-browser 逐页走查 + 点击进入子页面 + 截图取证  
**输出物：** 审核发现 + 每项问题的具体修改方案（含代码级指导）

---

## 一、审核范围总览

### 一级页面（14 个）

| # | 页面 | 路由 | 设计师评分 |
|---|------|------|-----------|
| 1 | 平台总览 (Dashboard) | `/` | 4.2 |
| 2 | 服务列表 | `/services` | 3.5 |
| 3 | 服务拓扑 | `/topology` | **4.6** ⭐ 全站最佳 |
| 4 | 拓扑目录 | `/topology-catalog` | 0.5 ❌ 空白 |
| 5 | 链路追踪 | `/traces` | 3.2 |
| 6 | 日志查询 | `/logs` | 4.0 |
| 7 | 监控面板 | `/monitor` | 2.5 |
| 8 | AI 诊断 | `/ai-diagnosis` | 0.5 ❌ 空白 |
| 9 | 技能目录 | `/skills` | 4.0 |
| 10 | AI 助理 (Agents) | `/agents` | **4.5** |
| 11 | 工作流 | `/workflows` | 4.0 |
| 12 | 告警中心 | `/alerts` | 4.0 |
| 13 | 系统设置 | `/settings` | 3.5 |
| 14 | SQL 查询 | `/sql` | 0.5 ❌ 空白 |

### 二级 / 弹窗 / 抽窗页面（6 个）

| # | 页面 | 触发入口 | 设计师评分 |
|---|------|----------|-----------|
| A | 服务详情抽屉 | 服务列表 → 点击服务名 | 4.3 |
| B | Trace 详情页 | 链路追踪 → 点击 Trace ID | 3.8 |
| C | 技能详情弹窗 | 技能目录 → 点击执行按钮 | 3.5 |
| D | Agent 编辑弹窗 | Agents → 点击编辑 | 3.8 |
| E | 工作流编辑器 | Workflows → 点击编辑 | 3.5 |
| F | 拓扑节点详情抽屉 | Topology → 点击节点 | **4.4** |

---

## 二、逐页详细审核 + 修改方案

---

### 1. Dashboard `/` — 评分 4.2

**截图证据：** `screenshot-1786174206638.png`

#### ✅ 设计亮点
- Hero 区域紫→蓝渐变是全站品牌色最强表达，视觉冲击力好
- 4 统计卡片三层结构（图标+大数字+小标签）信息层级清晰
- 趋势图 70% + 告警分布 30% 符合黄金比例
- "进入 AI 诊断"和"SQL 查询"用 ghost button 不抢焦点

#### 🔴 问题 V-01: 统计卡片文字截断

**现象：** "服务调用关..."被截断（应为"服务调用关系"）

**根因：** 卡片内文字容器宽度不够或字号过大导致 overflow

**修改方案：**
```tsx
// 在统计卡片的 label 文字上添加：
<div style={{
  whiteSpace: 'nowrap',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  // 或缩短文案为 "服务关系数"
}}>
```
**推荐：** 直接将文案从"服务调用关系"改为"服务关系"，更简洁。

---

#### 🟡 问题 V-02: 趋势图 X 轴刻度重叠

**现象：** 时间标签密集处重叠

**修改方案：**
```tsx
// 使用 Ant Design Charts 的 XAxis 配置
<xAxis 
  tickCount={6}          // 减少刻度数量
  tick={{ fontSize: 11 }} // 缩小字体
/>
```

---

#### 🟢 问题 V-03: Hero 与卡片区间距偏紧

**现象：** Hero 底部到第一行卡片仅 ~20px

**修改方案：**
```css
.hero-section {
  margin-bottom: 32px; /* 从 ~20px 提升到 32px */
}
```

---

### 2. 服务列表 `/services` — 评分 3.5

**截图证据：** `screenshot-1786174221706.png`

#### ✅ 设计亮点
- 三联统计卡信息密度合理
- 表格列宽分配得当，数值右对齐
- 服务名蓝色链接符合 Web 惯例

#### 🟡 问题 V-05: 表格行高偏紧凑

**现象：** 行高约 48px，对于多列数据略紧

**修改方案：**
```tsx
<Table
  rowClassName={() => 'service-table-row'}
  style={{ lineHeight: 1.5 }}
/>
```
```css
.service-table-row td {
  padding: 12px 16px; /* 从默认提升 */
}
```

---

#### 🟡 问题 V-06: 异常值无视觉强调

**现象：** 最大延迟 683.5ms 与正常值 0.6ms 同样灰色

**修改方案：**
```tsx
// 在渲染延迟列时加入条件格式
const renderLatency = (value: number) => (
  <span style={{ color: value > 100 ? '#ff4d4f' : '#a1a1aa', fontWeight: value > 100 ? 600 : 400 }}>
    {value.toFixed(1)}ms
  </span>
);
```

---

### 3. 服务拓扑 `/topology` — 评分 4.6 ⭐ 全站最佳

**截图证据：** `screenshot-1786174240888.png`

#### ✅ 设计亮点（本次修复后）
- 节点完全可见（opacity=1.0），背景色明亮可辨
- tier 分层清晰（业务入口→核心服务→数据存储→网络设备→基础设施）
- 工具栏专业：只看异常(toggle) + 业务筛选(Select) + 时间范围(Select) + 自动聚焦(button) + 高级设置(Collapse)
- 摘要条用 Alert banner 绿色 ✓ + 人话文案
- 平行边从不同 handle 出发，实线/虚线清晰分离
- 图例面板左下角中文标注

#### 🟡 问题 V-09: 右侧节点被画布边缘截断

**现象：** deepflow-clickhouse/grafana/server 三个节点超出可视区域

**修改方案：**
```tsx
// TopologyGraph.tsx 中 fitViewOptions 调整
fitViewOptions={{ 
  padding: 0.15,    // 从 0.08 增加到 0.15
  minZoom: 0.45,    // 从 0.5 降低以容纳更多内容
  maxZoom: 1.2,
}}
```

---

#### 🟢 问题 V-10: 工具栏按钮风格不统一

**现象：** Toggle/Button/Collapse 三种交互范式混用

**修改方案：** 统一为 Button Group 风格：
```tsx
<Button.Group>
  <Button type={onlyAbnormal ? 'primary' : 'default'} onClick={toggleOnlyAbnormal}>
    只看异常
  </Button>
  <Button icon={<SettingOutlined />}>高级设置</Button>
</Button.Group>
```

---

### 4. 拓扑目录 `/topology-catalog` — 评分 0.5 ❌

**截图证据：** `screenshot-1786174253882.png`

#### 🔴 问题 TC-01/TC-02/TC-03: 完全空白

**现象：** 无任何内容渲染，连空态占位都没有

**修改方案（完整代码）：**

```tsx
// TopologyCatalog/index.tsx 新建文件
import { useState, useEffect } from 'react'
import { Table, Button, Space, Tag, Popconfirm, message, Empty, Card } from 'antd'
import { PlusOutlined, SyncOutlined, DeleteOutlined, EditOutlined } from '@ant-design/icons'
import { topoListNodes, topoListRelations, topoSyncCatalog, topoDeleteNode, topoDeleteRelation } from '../../api/client'

interface NodeItem { id: number; type: string; name: string; props_json?: string }
interface RelationItem { id: number; src_id: number; dst_id: number; type: string }

export default function TopologyCatalog() {
  const [nodes, setNodes] = useState<NodeItem[]>([])
  const [relations, setRelations] = useState<RelationItem[]>([])
  const [loading, setLoading] = useState(false)

  const fetchData = async () => {
    setLoading(true)
    try {
      const [n, r] = await Promise.all([topoListNodes(), topoListRelations()])
      setNodes(n.data || [])
      setRelations(r.data || [])
    } finally { setLoading(false) }
  }

  useEffect(() => { fetchData() }, [])

  const handleSync = async () => {
    try {
      await topoSyncCatalog()
      message.success('同步成功')
      fetchData()
    } catch { message.error('同步失败') }
  }

  return (
    <div style={{ padding: 24 }}>
      <Card 
        title="拓扑目录管理" 
        extra={
          <Space>
            <Button icon={<SyncOutlined />} onClick={handleSync}>同步 Trace 数据</Button>
            <Button type="primary" icon={<PlusOutlined />}>新建节点</Button>
          </Space>
        }
      >
        {nodes.length === 0 && !loading ? (
          <Empty description="暂无拓扑目录数据">
            <Button type="primary" onClick={handleSync}>立即同步</Button>
          </Empty>
        ) : (
          <>
            <Table
              dataSource={nodes}
              loading={loading}
              rowKey="id"
              size="middle"
              pagination={{ pageSize: 20 }}
              columns={[
                { title: 'ID', dataIndex: 'id', width: 60 },
                { title: '名称', dataIndex: 'name', render: (t) => <b>{t}</b> },
                { title: '类型', dataIndex: 'type', render: (t) => <Tag color="blue">{t}</Tag> },
                { title: '操作', width: 160, render: (_, r) => (
                  <Space><Button size="small" icon={<EditOutlined />} /> 
                    <Popconfirm title="确定删除？" onConfirm={() => topoDeleteNode(r.id).then(fetchData)}>
                      <Button size="small" danger icon={<DeleteOutlined />} />
                    </Popconfirm></Space>
                )},
              ]}
            />
          </>
        )}
      </Card>
    </div>
  )
}
```

---

### 5. 链路追踪 `/traces` — 评分 3.2

**截图证据：** `screenshot-1786174286792.png`

#### 🟡 问题 V-12: Trace ID 截断无 tooltip

**现象：** UUID 被 ... 截断后无法辨认

**修改方案：**
```tsx
{
  title: 'Trace ID',
  dataIndex: 'trace_id',
  render: (id: string) => (
    <Tooltip title={id} placement="topLeft">
      <a>{id.substring(0, 16)}...</a>
    </Tooltip>
  ),
  ellipsis: true,
}
```

---

#### 🟡 问题 V-14: 缺少时间范围选择器

**现象：** 只有搜索框，缺少时间筛选——链路追踪的核心交互

**修改方案：**
```tsx
// 在搜索框右侧添加 RangePicker
<Space style={{ marginBottom: 16 }}>
  <Input.Search placeholder="搜索 Trace ID..." allowClear onSearch={setSearchId} style={{ width: 320 }} />
  <RangePicker showTime onChange={(dates) => setTimeRange(dates)} />
  <Button icon={<ReloadOutlined />} onClick={fetchData}>刷新</Button>
</Space>
```

---

### 6. 日志查询 `/logs` — 评分 4.0

**截图证据：** `screenshot-1786174302789.png`

#### ✅ 设计亮点
- 过滤器一行搞定，布局合理
- 日志级别 Badge 彩色编码
- 行展开按钮 (+) 操作直觉正确

#### 🟢 问题 V-15: 日志内容列文本过长

**现象：** URL 类日志行溢出

**修改方案：**
```tsx
{
  title: '内容',
  dataIndex: 'content',
  ellipsis: { showTitle: false }, // 截断但 hover 显示完整
  render: (content: string) => (
    <Tooltip title={content}>
      <span style={{ wordBreak: 'break-all', maxWidth: 500 }}>{content}</span>
    </Tooltip>
  ),
}
```

---

### 7. 监控面板 `/monitor` — 评分 2.5

**截图证据：** `screenshot-1786174320114.png`

#### 🔴 问题 M-01/M-18: 四个面板完全相同且无数据

**现象：** 四个面板都是"暂无数据（等待 VM 采集）"，视觉单调

**修改方案（差异化空态设计）：**
```tsx
// 为每个面板定制空态插图和文案
const panelEmptyStates = {
  requestRate:   { icon: <LineChartOutlined />, text: '正在接入请求速率数据…', color: '#1677ff' },
  errorRate:     { icon: <WarningOutlined />, text: '正在接入错误率数据…', color: '#faad14' },
  latencyP95:    { icon: <ClockCircleOutlined />, text: '正在接入延迟数据…', color: '#52c41a' },
  cpuUsage:      { icon: <DesktopOutlined />, text: '正在接入 CPU 数据…', color: '#722ed1' },
}

// 渲染
<Empty
  image={Empty.PRESENTED_IMAGE_SIMPLE}
  description={
    <span style={{ color: panelEmptyStates[id].color }}>
      {panelEmptyStates[id].text}
    </span>
  }
/>
```

---

### 8. AI 诊断 `/ai-diagnosis` — 评分 0.5 ❌

**截图证据：** `screenshot-1786174341092.png`

#### 🔴 问题 AI-01/AI-02: 完全空白

**修改方案（对话界面骨架）：**
```tsx
// AiDiagnosis/index.tsx
import { useState } from 'react'
import { Input, List, Avatar, Typography, Empty, Spin } from 'antd'
import { SendOutlined, RobotOutlined, UserOutlined } from '@ant-design/icons'

const { TextArea } = Input

interface Message { role: 'user' | 'assistant'; content: string; time: string }

export default function AiDiagnosis() {
  const [messages, setMessages] = useState<Message[]>([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)

  const send = async () => {
    if (!input.trim()) return
    const userMsg = { role: 'user' as const, content: input, time: new Date().toLocaleTimeString() }
    setMessages(prev => [...prev, userMsg])
    setInput('')
    setLoading(true)
    // TODO: 调用后端 chat API
    setTimeout(() => {
      setMessages(prev => [...prev, { role: 'assistant', content: '正在分析中...', time: new Date().toLocaleTimeString() }])
      setLoading(false)
    }, 1500)
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: 'calc(100vh - 120px)' }}>
      <List
        dataSource={messages}
        locale={{ emptyText: <Empty description="开始一次新的 AI 诊断对话" /> }}
        renderItem={msg => (
          <List.Item style={{ border: 'none', padding: '12px 24px' }}>
            <Avatar style={{ backgroundColor: msg.role === 'user' ? '#1677ff' : '#722ed1' }} icon={msg.role === 'user' ? <UserOutlined /> : <RobotOutlined />} />
            <div style={{ marginLeft: 12, flex: 1 }}>
              <Typography.Text strong>{msg.role === 'user' ? '你' : 'AI 助手'}</Typography.Text>
              <Typography.Paragraph style={{ margin: '4px 0 0', whiteSpace: 'pre-wrap' }}>{msg.content}</Typography.Paragraph>
              <Typography.Text type="secondary" style={{ fontSize: 11 }}>{msg.time}</Typography.Text>
            </div>
          </List.Item>
        )}
      />
      <div style={{ borderTop: '1px solid rgba(255,255,255,0.08)', padding: '12px 24px', background: '#111827' }}>
        <TextArea rows={2} value={input} onChange={e => setInput(e.target.value)} onPressEnter={e => !e.shiftKey && (e.preventDefault(), send())} placeholder="描述你遇到的问题…" />
        <Button type="primary" icon={<SendOutlined />} onClick={send} loading={loading} style={{ marginTop: 8 }}>发送</Button>
      </div>
    </div>
  )
}
```

---

### 9-14. 其他一级页面

详见前一份报告 `UI-DESIGNER-AUDIT-2026-08-08.md` 第 9-14 节。以下重点补充**弹出窗口/抽屉**的审核。

---

## 三、弹出窗口 / 抽屉 详细审核（新增）

---

### A. 服务详情抽屉 — 评分 4.3

**触发：** 服务列表 → 点击服务名（如 `deepflow-server`）  
**截图证据：** `screenshot-1786175022253.png`

#### ✅ 设计亮点
- 右侧抽屉宽度合适（~480px），不遮挡主列表
- 标题区：服务名(大) + 类型 Badge(小) + "查看系统详情" CTA 按钮
- 人话摘要块：✓ 正常 + "deepflow-server 当前状态：正常；平均响应 1ms" — 情感化表达优秀
- 4 个指标卡（Apex/响应时间/请求错误率/吞吐率）用大数字+颜色编码（绿/蓝/绿/绿）
- 指标趋势图双 Y 轴（柱状图+折线图），有图例
- 底部显示"共采集 344 条调用"

#### 🟡 问题 D-01: 指标卡数字颜色过于鲜艳

**现象：** Apex=1.00(绿), 响应时间=0.90ms(蓝), 错误率=0.00%(绿), 吞吐率=344rpm(绿) — 四个数字用了不同颜色，视觉上像"彩虹"

**修改方案：** 统一主色调，仅异常值标红：
```tsx
const metricColor = (key: string, value: number) => {
  if (key === 'error_rate' && value > 0) return '#ff4d4f'  // 仅错误率>0 时红色
  if (key === 'latency' && value > 100) return '#faad14'   // 仅延迟>100ms 时黄色
  return '#fafafa'  // 其余统一白色
}
```

---

#### 🟢 问题 D-02: "查看系统详情"按钮位置偏高

**现象：** 按钮在标题区右上角，与关闭按钮(X)距离近，可能误触

**修改方案：** 将 CTA 移至抽屉底部操作栏：
```tsx
<Drawer
  extra={<Button type="primary" size="small">查看系统详情</Button>}
  footer={
    <div style={{ display: 'flex', justifyContent: 'space-between' }}>
      <Button>导出报告</Button>
      <Button type="primary">查看系统详情</Button>
    </div>
  }
/>
```

---

### B. Trace 详情页 — 评分 3.8

**触发：** 链路追踪 → 点击 Trace ID  
**截图证据：** `screenshot-1786175052473.png`

#### ✅ 设计亮点
- 标题清晰："Trace: 6652230edd68515c..."（虽截断但有完整 ID 可 tooltip）
- Span 瀑布图区域：单 span 用蓝色条 + 服务名 + 操作类型(SERVER) + 耗时 + 时间戳
- 关联证据 Tab 切换：关联日志(1) / VictoriaLogs(0) / 服务指标(30) / 关联告警(0) — 数字标注很实用
- 日志表格展示实际日志内容

#### 🟡 问题 T-01: Span 瀑布图太简陋

**现象：** 只有 1 条 span，显示为一个蓝色矩形条，缺少层次感和上下文

**修改方案：** 即使只有 1 条 span 也应优化视觉效果：
```tsx
// Span 行组件优化
<div className="span-row" style={{
  display: 'flex', alignItems: 'center', padding: '8px 0',
  borderBottom: '1px solid rgba(255,255,255,0.04)'
}}>
  {/* 时间轴 */}
  <span style={{ width: 80, color: '#71717a', fontSize: 12 }}>{duration}ms</span>
  {/* Span 条 */}
  <div style={{ flex: 1, position: 'relative', height: 28, borderRadius: 4, background: 'rgba(22,119,255,0.15)', borderLeft: '3px solid #1677ff' }}>
    <span style={{ position: 'absolute', left: 8, top: '50%', transform: 'translateY(-50%)', fontSize: 12 }}>
      {serviceName} <Tag size="small">{kind}</Tag>
    </span>
    {/* 耗时标注 */}
    <span style={{ position: 'absolute', right: 8, top: '50%', transform: 'translateY(-50%)', fontSize: 11, color: '#71717a' }}>
      {timestamp}
    </span>
  </div>
</div>
```

---

#### 🟡 问题 T-02: 关联证据 Tab 数字暗示数据缺失

**现象：** VictoriaLogs(0)、关联告警(0) — 用户可能困惑为什么没有数据

**修改方案：** 对 0 数量的 Tab 加灰显提示：
```tsx
<TabPane tab={`关联日志 (${logCount})`} key="logs" disabled={logCount === 0} />
<TabPane tab={<span style={{ opacity: logCount === 0 ? 0.45 : 1 }}>VictoriaLogs ({vlCount})</span>} key="victoria" />
```

---

### C. 技能详情弹窗 — 评分 3.5

**触发：** 技能目录 → 点击执行按钮  
**截图证据：** `screenshot-1786175120506.png`

#### ✅ 设计亮点
- Modal 弹窗居中，宽度适中（~520px）
- Key / 描述 / 工具 三段式结构清晰
- 工具标签用 code-style badge（深色底+等宽字体）
- 执行按钮主色突出

#### 🟡 问题 S-01: 弹窗内容密度不均

**现象：** 描述文字很长（一行多）但工具标签很短，下半部分大片空白

**修改方案：** 调整布局为左右分栏或增加参数配置区：
```tsx
<Modal title={skillName} width={600} footer={<Button type="primary" icon={<PlayCircleOutlined />} onClick={execute}>执行</Button>}>
  <Row gutter={16}>
    <Col span={16}>
      <Descriptions column={1} size="small" bordered>
        <Descriptions.Item label="Key">{skill.key}</Descriptions.Item>
        <Descriptions.Item label="描述">{skill.description}</Descriptions.Item>
      </Descriptions>
    </Col>
    <Col span={8}>
      <Card size="small" title="工具清单">
        {skill.tools.map(t => <Tag key={t} style={{ marginBottom: 4 }} code>{t}</Tag>)}
      </Card>
    </Col>
  </Row>
  {/* 执行参数表单 */}
  {showParams && <Divider orientation="left">执行参数</Divider>}
</Modal>
```

---

### D. Agent 编辑弹窗 — 评分 3.8

**触发：** Agents → 点击编辑  
**截图证据：** `screenshot-1786175145087.png`

#### ✅ 设计亮点
- Modal 弹窗覆盖在原页面之上，背景半透明遮罩
- 表单字段清晰：名称 / 角色 / 目标 / 背景 / 意图关键词
- 字段 label 用中文（名称(唯一)、角色、目标等）

#### 🟡 问题 AG-01: 表单缺少分组

**现象：** 所有字段平铺，没有视觉分组，长表单难以扫读

**修改方案：** 用 Card 分组或 Divider 分隔：
```tsx
<Modal title="编辑 Agent" width={640}>
  <Card size="small" title="基本信息" style={{ marginBottom: 16 }}>
    <Form.Item label="名称"><Input /></Form.Item>
    <Form.Item label="角色"><Input.TextArea rows={2} /></Form.Item>
  </Card>
  <Card size="small" title="能力定义" style={{ marginBottom: 16 }}>
    <Form.Item label="目标"><Input.TextArea rows={3} /></Form.Item>
    <Form.Item label="背景"><Input.TextArea rows={2} /></Form.Item>
  </Card>
  <Card size="small" title="关联配置">
    <Form.Item label="意图关键词"><Input placeholder="逗号分隔" /></Form.Item>
    <Form.Item label="技能选择"><Select mode="multiple" /></Form.Item>
  </Card>
</Modal>
```

---

### E. 工作流编辑器 — 评分 3.5

**触发：** Workflows → 点击编辑  
**截图证据：** `screenshot-1786175169426.png`

#### ✅ 设计亮点
- 独立页面（非弹窗），空间充足
- 左侧节点调色板（采集/分析/执行三组），可拖拽
- 中央画布区域带网格背景，专业感强
- 当前显示"汇总输出"节点（圆角矩形+连接点）
- 顶部工具栏：返回 / 流程名 / 步骤链面包屑 / 运行记录 / 清除 / 保存 / 运行

#### 🟡 问题 WF-01: 画布节点样式单一

**现象：** 所有节点看起来一样（深色圆角矩形），无法区分类型

**修改方案：** 为不同类型节点设置不同配色：
```typescript
const NODE_TYPE_STYLES: Record<string, { bg: string; border: string; icon: ReactNode }> = {
  collect:  { bg: '#1e3a5f', border: '#1677ff', icon: <CloudDownloadOutlined /> },
  analyze:  { bg: '#1a2e1a', border: '#52c41a', icon: <LineChartOutlined /> },
  execute:  { bg: '#3d2a1a', border: '#fa8c16', icon: <ThunderboltOutlined /> },
  output:   { bg: '#2a1f3d', border: '#722ed1', icon: <ExportOutlined /> },
}
```

---

#### 🟡 问题 WF-02: 步骤链面包屑过长

**现象：** "采集→清洗→原因→RAG→AI分析→方案→风险→" 在窄屏溢出

**修改方案：** 超出部分折叠：
```tsx
<Breadcrumb separator=">" maxItems={5} itemRender={(route) => route.name} items={stepItems} />
```

---

### F. 拓扑节点详情抽屉 — 评分 4.4 ⭐

**触发：** Topology → 点击节点  
**截图证据：** `screenshot-1786175214079.png`

#### ✅ 设计亮点（全站最佳抽屉设计）
- **人话摘要块**放在最顶部：✓ 正常 + "deepflow-server 当前状态：正常；平均响应 1ms" — 情感化 + 信息量完美平衡
- **4 个指标卡**横向排列：Apex(1.00绿) / 响应时间(0.90ms蓝) / 错误率(0.00%绿) / 吞吐率(344rpm绿)
- **指标趋势图**：柱状图(请求错误率) + 折线图(总调用量)，双 Y 轴，有图例
- **底部元信息**："共采集 344 条调用，其中错误 0 条"
- **右上角 CTA**："查看系统详情" 主色按钮
- **抽屉宽度** (~480px) 合适，不遮挡主画布

#### ✅ 这是全站最佳的详情抽屉设计，应作为其他抽屉的模板。

#### 🟢 问题 TN-01: 指标趋势图高度偏矮

**现象：** 图表区域高度约 180px，柱状图细节不易看清

**修改方案：**
```tsx
{/* 趋势图容器 */}
<div style={{ height: 280 }}>  {/* 从 ~180 提升到 280 */}
  <MetricTrendChart data={trendData} />
</div>
```

---

## 四、全局设计问题汇总（含修改方案）

### 🔴 G-01: 三个空白页面（最高优先级）

**涉及：** 拓扑目录 / AI 诊断 / SQL 查询

**统一修复方案：** 创建 `<AppEmpty>` 公共组件
```tsx
// components/AppEmpty.tsx
import { Empty, Button, Space } from 'antd'
import { PlusOutlined, SyncOutlined } from '@ant-design/icons'

interface Props {
  description?: string
  actions?: React.ReactNode
}

export default function AppEmpty({ description = '暂无数据', actions }: Props) {
  return (
    <div style={{ 
      display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
      minHeight: 400, padding: 48 
    }}>
      <Empty 
        image={Empty.PRESENTED_IMAGE_SIMPLE}
        description={<span style={{ color: '#a1a1aa', fontSize: 14 }}>{description}</span>}
      >
        {actions || (
          <Space>
            <Button icon={<SyncOutlined />}>刷新</Button>
            <Button type="primary" icon={<PlusOutlined />}>新建</Button>
          </Space>
        )}
      </Empty>
    </div>
  )
}
```

---

### 🟡 G-02: 侧边栏与主内容区对比度过低

**当前：** Sidebar `#111827` vs Main `#09090b`，对比比 ~1.15:1

**修改方案：**
```css
/* AppLayout.css 或全局样式 */
.sidebar {
  background: #151a28;           /* 从 #111827 提亮 */
  border-right: 1px solid rgba(255, 255, 255, 0.06); /* 加分割线 */
}
```

---

### 🟡 G-03: 缺少全局 Toast 通知系统

**现象：** 操作后无反馈（保存/删除/创建是否成功？）

**修改方案：** 在 `App.tsx` 中包裹 `<App useApp={false}>` 并使用 hook：
```tsx
import { App } from 'antd'

function Root() {
  const { message, modal, notification } = App.useApp()
  // 全局使用 message.success('保存成功')
}
```

---

### 🟢 G-04: 页面 Loading 状态缺失

**修改方案：** 为每个路由加 Suspense + Skeleton
```tsx
// router 配置
<Suspense fallback={<Skeleton active paragraph={{ rows: 6 }} />}>
  <Routes>
    <Route path="/services" element={<Services />} />
    {/* ... */}
  </Routes>
</Suspense>
```

---

## 五、设计师 TOP 10 修改优先级（含工作量估算）

| # | 修改项 | 影响 | 工作量 | 涉及文件 |
|---|--------|------|--------|----------|
| 1 | **补齐 3 个空白页面** | 消除"半成品"感知 | **1.5 天** | 新建 3 个页面组件 + 路由注册 |
| 2 | **统一空态组件 `<AppEmpty>`** | 全站复用 | **0.3 天** | `components/AppEmpty.tsx` |
| 3 | **侧边栏分割线 + 提亮** | 全局空间感 | **0.05 天** | `Layout/index.tsx` CSS |
| 4 | **Dashboard 统计卡截断修复** | 首页第一印象 | **0.1 天** | `Dashboard/index.tsx` |
| 5 | **监控面板差异化空态** | 4 面板不再一样 | **0.3 天** | `Monitor/index.tsx` |
| 6 | **Trace ID tooltip + 时间选择器** | 链路追踪核心体验 | **0.2 天** | `Traces/index.tsx` |
| 7 | **表格异常值条件格式** | 多个表格页受益 | **0.2 天** | Services/Alerts 等 |
| 8 | **Agent 编辑弹窗表单分组** | 弹窗可用性 | **0.2 天** | `Agents/index.tsx` |
| 9 | **工作流节点类型配色** | DAG 可读性 | **0.3 天** | `WorkflowEditor.tsx` |
| 10 | **全局 Toast + Loading** | 操作反馈完整性 | **0.3 天** | `App.tsx` + 路由配置 |

---

## 六、设计标杆与反面教材（最终版）

### 🏆 设计标杆（应作为全站模板）

| 排名 | 页面 | 评分 | 学习要点 |
|------|------|------|----------|
| 🥇 | **服务拓扑** | **4.6** | 视觉完整性、工具栏专业度、人话摘要、平行边分离 |
| 🥇 | **拓扑节点详情抽屉** | **4.4** | 人话摘要置顶、指标卡布局、CTA 位置、信息密度 |
| 🥈 | **AI 助理 (Agents)** | **4.5** | 卡片式实体展示、技能 badge、操作语义色 |
| 🥉 | **Dashboard** | **4.2** | Hero 渐变、统计卡三层结构、70/30 黄金比例 |

### ⚠️ 反面教材（必须重做）

| 排名 | 页面 | 评分 | 核心问题 |
|------|------|------|----------|
| ❌ | 拓扑目录 / AI 诊断 / SQL 查询 | **各 0.5** | 空白 = 设计不存在 |
| ⚠️ | 监控面板 | **2.5** | 四个一模一样的空面板 |

---

*本报告由 UI/视觉设计师视角产出，涵盖 14 个一级页面 + 6 个二级/弹窗/抽屉页面。每项问题均附具体修改方案与代码示例。*
