# Phase B · Skills/Agents MVP（HTTP API + 页面）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 暴露自研 skill_registry/experts 为 HTTP API，新增 Skills 目录页（含单 skill 执行）+ Agents 只读页。MVP 聚焦只读展示 + 主要工具执行（补 params schema），agent CRUD/持久化列为二期。

**Architecture:** 后端 orchestrator：给 `ToolDef` 补 `params` schema + 新增 `execute_skill(key, params)` 执行器 + 在 `main.py` 暴露 `GET /api/v1/ai/skills`、`GET /api/v1/ai/skills/{key}`、`POST /api/v1/ai/skills/{key}/execute`、`GET /api/v1/ai/agents`、`GET /api/v1/ai/agents/{name}`（只读）。前端：Skills 目录页（表格+详情+执行表单）、Agents 卡片页、路由菜单、AgentSidePanel 改用 `/agents`。

**Tech Stack:** Python FastAPI / React18 / AntD5。

## Global Constraints

- 后端：`ai-orchestrator/skill_registry.py`（ToolDef/SkillDef/ExpertDef/Registry）、`orchestrator.py`、`main.py`。
- **现状**：`ToolDef.params` 全空（无 schema）；无 `execute_skill`；无 `/ai/skills`、`/ai/agents` 接口。
- 前端：`observability-frontend/src/pages/`（无 Agents/Skills 页）、`components/AgentSidePanel.tsx`（硬编码 agent）、`App.tsx`（菜单/路由）。
- 合规：页面/接口为自研功能，独立实现，不复刻 ongrid 代码。
- 基线：`github.com/Jw-Jm/aiops-edge` main=`d7f0521`，每任务提交。

---

## Task 1: ToolDef 补 params schema + 序列化方法

**Files:**
- Modify: `aiops/ai-orchestrator/skill_registry.py`

**Interfaces:**
- Consumes: `ToolDef`/`SkillDef`/`ExpertDef` dataclass。
- Produces: `ToolDef.params` 支持 `param_name -> {type, required, default, desc}`；新增 `to_summary()` 方法导出 skill 元数据（key/name/title/description/tools/category/params/experts）。

- [ ] **Step 1: 加 to_summary 方法**

在 `SkillDef` 加：
```python
    def to_summary(self) -> dict:
        """导出技能元数据，供 /ai/skills 接口与前端渲染。"""
        tools = []
        for tn in self.tools:
            t = ToolRegistry.get(tn)
            tools.append({
                "name": tn,
                "description": t.description if t else "",
                "category": t.category if t else "general",
                "requires_approval": t.requires_approval if t else False,
                "params": list((t.params or {}).keys()),
            })
        return {
            "key": self.name,
            "name": self.title,
            "description": self.description,
            "intent_keywords": self.intent_keywords,
            "tools": tools,
            "system_prompt": self.system_prompt,
        }
```

- [ ] **Step 2: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-orchestrator/skill_registry.py
git commit -m "feat(orchestrator): skill metadata serialization"
```

---

## Task 2: 为 6 个核心工具补 params schema

**Files:**
- Modify: `aiops/ai-orchestrator/tools.py`（`@ToolRegistry.register()` 装饰器调用处，为 query_metrics/query_traces/query_topology/get_service_list/case_search/case_feedback 补 `params`）

**Interfaces:**
- Consumes: `ToolRegistry.register` 的 `params` 参数（ToolDef 已有）。
- Produces: 核心工具带参数 schema（param_name -> {type, required, default, desc}），前端执行表单可渲染。

- [ ] **Step 1: 读现有 register 调用并补 params**

```python
# tools.py 现有形如：
# @ToolRegistry.register(name="query_metrics", description="查询服务指标", category="metrics")
# 改为：
@ToolRegistry.register(name="query_metrics", description="查询服务指标", category="metrics",
                       params={"service": {"type": "string", "required": True, "default": "", "desc": "服务名"},
                               "metric": {"type": "string", "required": False, "default": "error_rate", "desc": "指标名(error_rate/latency/calls)"}})
```
对以下工具补齐（按各工具实际 func 签名）：
- `query_metrics(service, metric)` 
- `query_traces(service, limit)`
- `query_topology(service)`
- `get_service_list()`
- `case_search(query)`
- `case_feedback(case_id, useful)`（或签名按实际）

> 说明：`ToolRegistry.register` 装饰器需支持 `params` 关键字传入（当前签名是 `register(name, description, category, requires_approval)`，需加 `**kwargs` 或显式 `params`）。若当前未接收 `params`，同步修改 `ToolRegistry.register` 签名接收并传给 `ToolDef`。

- [ ] **Step 2: 语法校验**

Run: `cd aiops/ai-orchestrator && python3 -m py_compile tools.py skill_registry.py`
Expected: 无语法错误。

- [ ] **Step 3: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-orchestrator/tools.py ai-orchestrator/skill_registry.py
git commit -m "feat(orchestrator): params schema for core tools"
```

---

## Task 3: 新增 execute_skill 执行器

**Files:**
- Modify: `aiops/ai-orchestrator/skill_registry.py`（`SkillRegistry` 加方法）

**Interfaces:**
- Consumes: `SkillRegistry` 的 skill 定义 + `ToolRegistry`。
- Produces: `SkillRegistry.execute_skill(key, params) -> dict`：按 skill 取 tools，逐个调用 `ToolRegistry.get(tool).func`，返回 `{"result": {...}}`；需审批工具（requires_approval）返回 `{"requires_approval": true, "tool": name}`。

- [ ] **Step 1: 实现 execute_skill**

```python
    @classmethod
    def execute_skill(cls, key: str, params: dict) -> dict:
        """执行技能：遍历其 tools，调用对应工具函数。"""
        skill = cls._skills.get(key)
        if not skill:
            raise KeyError(f"skill not found: {key}")
        out = {}
        for tn in skill.tools:
            t = ToolRegistry.get(tn)
            if not t:
                out[tn] = {"error": "tool not found"}
                continue
            if t.requires_approval:
                out[tn] = {"requires_approval": True, "tool": tn}
                continue
            try:
                # 按工具参数名从 params 取子集
                tool_params = {k: v for k, v in (params or {}).items() if k in (t.params or {})}
                result = t.func(**tool_params) if tool_params else t.func()
                out[tn] = {"result": str(result)[:1000]}
            except Exception as e:
                out[tn] = {"error": str(e)}
        return {"skill": key, "outputs": out}
```
> 注：`SkillRegistry._skills` 需确认实际字段名（`_skills` 还是 `skills`），按实际改。

- [ ] **Step 2: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-orchestrator/skill_registry.py
git commit -m "feat(orchestrator): execute_skill dispatcher"
```

---

## Task 4: 暴露 /ai/skills、/ai/agents HTTP 接口

**Files:**
- Modify: `aiops/ai-orchestrator/main.py`

**Interfaces:**
- Consumes: `SkillRegistry`/`ExpertRegistry`（Task 1-3）、`_get_brain()`。
- Produces:
  - `GET /api/v1/ai/skills` → `{skills: [{key,name,description,tools,experts}]}`
  - `GET /api/v1/ai/skills/{key}` → skill 详情
  - `POST /api/v1/ai/skills/{key}/execute`（body `{params}`）→ `execute_skill`
  - `GET /api/v1/ai/agents` → `{agents: [{name,role,goal,description,skills,tools}]}`
  - `GET /api/v1/ai/agents/{name}` → agent 详情

- [ ] **Step 1: 加接口**

```python
# main.py，仿现有 /ai/sessions 接口风格
@app.get("/api/v1/ai/skills")
async def ai_skills():
    brain = _get_brain()
    registry = brain.get_skill_registry()  # 或直接引用 SkillRegistry
    return {"skills": [s.to_summary() for s in registry.list_all()]}

@app.get("/api/v1/ai/skills/{key}")
async def ai_skill_detail(key: str):
    registry = _get_brain().get_skill_registry()
    skill = registry.get(key)
    if not skill:
        raise HTTPException(404, "skill not found")
    return skill.to_summary()

@app.post("/api/v1/ai/skills/{key}/execute")
async def ai_skill_execute(key: str, body: dict):
    params = body.get("params", {})
    try:
        return SkillRegistry.execute_skill(key, params)
    except KeyError:
        raise HTTPException(404, "skill not found")

@app.get("/api/v1/ai/agents")
async def ai_agents():
    brain = _get_brain()
    experts = brain.get_expert_registry()  # 或 ExpertRegistry
    return {"agents": [{"name": e.name, "role": e.role, "goal": e.goal,
                        "description": e.goal, "skills": e.skills, "tools": e.tools}
                       for e in experts.list_all()]}

@app.get("/api/v1/ai/agents/{name}")
async def ai_agent_detail(name: str):
    brain = _get_brain()
    e = brain.get_expert_registry().get(name)
    if not e:
        raise HTTPException(404, "agent not found")
    return {"name": e.name, "role": e.role, "goal": e.goal, "backstory": e.backstory,
            "skills": e.skills, "tools": e.tools}
```
> 注：`_get_brain().get_skill_registry()`/`get_expert_registry()` 方法名需按 orchestrator 实际 BrainOrchestrator 暴露方式调整（可能需在 BrainOrchestrator 加访问器，或直接 import SkillRegistry/ExpertRegistry 类）。

- [ ] **Step 2: 语法校验 + 手动冒烟**

Run: `cd aiops/ai-orchestrator && python3 -m py_compile main.py`
Expected: 无语法错误。

- [ ] **Step 3: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-orchestrator/main.py
git commit -m "feat(orchestrator): /ai/skills + /ai/agents read-only endpoints"
```

---

## Task 5: 前端 api 封装 + Skills 页 + Agents 页 + 路由菜单

**Files:**
- Modify: `aiops/observability-frontend/src/api/client.ts`
- Create: `aiops/observability-frontend/src/pages/Skills/index.tsx`
- Create: `aiops/observability-frontend/src/pages/Agents/index.tsx`
- Modify: `aiops/observability-frontend/src/App.tsx`（路由 + 菜单）
- Modify: `aiops/observability-frontend/src/components/AgentSidePanel.tsx`（用 /agents 替换硬编码）

**Interfaces:**
- Consumes: Task 4 接口。
- Produces: Skills 目录页（表格+详情弹窗+执行表单）、Agents 卡片页、路由 `/skills`、`/agents`、菜单项、AgentSidePanel 动态数据。

- [ ] **Step 1: client.ts 封装**

```ts
// ===== AI Skills / Agents =====
export const listSkills = () => api.get('/ai/skills')
export const getSkill = (key: string) => api.get(`/ai/skills/${encodeURIComponent(key)}`)
export const executeSkill = (key: string, params: Record<string, unknown>) => api.post(`/ai/skills/${encodeURIComponent(key)}/execute`, { params })
export const listAgents = () => api.get('/ai/agents')
export const getAgent = (name: string) => api.get(`/ai/agents/${encodeURIComponent(name)}`)
```

- [ ] **Step 2: Skills/index.tsx（目录表 + 详情弹窗 + 执行）**

```tsx
// src/pages/Skills/index.tsx
import React, { useEffect, useState } from 'react'
import { Card, Table, Tag, Button, Drawer, Descriptions, Form, Input, InputNumber, Select, Switch, message } from 'antd'
import { listSkills, getSkill, executeSkill } from '../../api/client'

interface Skill { key: string; name: string; description: string; tools: any[] }
interface ToolParam { name: string; type: string; required?: boolean; default?: any }

const Skills: React.FC = () => {
  const [skills, setSkills] = useState<Skill[]>([])
  const [loading, setLoading] = useState(true)
  const [detail, setDetail] = useState<any>(null)
  const [runOpen, setRunOpen] = useState(false)
  const [runParams, setRunParams] = useState<Record<string, any>>({})
  const [runResult, setRunResult] = useState('')

  const load = async () => {
    setLoading(true)
    try { const r = await listSkills(); setSkills(r?.data?.skills || []) } catch { message.error('加载技能失败') } finally { setLoading(false) }
  }
  useEffect(() => { load() }, [])

  const openDetail = async (key: string) => {
    try { const r = await getSkill(key); setDetail(r?.data); setRunParams({}) } catch {}
  }
  const doRun = async () => {
    try {
      const r = await executeSkill(detail.key, runParams)
      setRunResult(JSON.stringify(r?.data, null, 2))
    } catch { message.error('执行失败') }
  }

  const columns = [
    { title: '技能', dataIndex: 'name', key: 'name' },
    { title: '描述', dataIndex: 'description', key: 'description', ellipsis: true },
    { title: '工具数', dataIndex: 'tools', key: 'tools', render: (v: any[]) => v?.length || 0 },
    { title: '操作', key: 'op', render: (_: any, s: Skill) => <a onClick={() => openDetail(s.key)} style={{ color: '#60a5fa' }}>详情/执行</a> },
  ]
  return (
    <Card title="技能目录" style={{ background: 'var(--surface)', borderColor: 'var(--border)', borderRadius: 10 }} extra={<Button size="small" onClick={load}>刷新</Button>}>
      <Table rowKey="key" columns={columns} dataSource={skills} loading={loading} pagination={false} size="small" />
      <Drawer title={detail?.name} open={!!detail} onClose={() => setDetail(null)} width={520} style={{ background: 'var(--surface)' }}>
        <Descriptions column={1} size="small">
          <Descriptions.Item label="Key">{detail?.key}</Descriptions.Item>
          <Descriptions.Item label="描述">{detail?.description}</Descriptions.Item>
          <Descriptions.Item label="工具">
            {(detail?.tools || []).map((t: any) => <Tag key={t.name} style={{ margin: 2 }}>{t.name}</Tag>)}
          </Descriptions.Item>
        </Descriptions>
        <div style={{ marginTop: 16 }}>
          <Button type="primary" onClick={() => setRunOpen(true)}>执行</Button>
        </div>
      </Drawer>
      <Drawer title={`执行 ${detail?.name}`} open={runOpen} onClose={() => setRunOpen(false)} width={480} style={{ background: 'var(--surface)' }}>
        <Form layout="vertical">
          {(detail?.tools || []).flatMap((t: any) => (t.params || []).map((pn: string) => ({ pn, t }))).map(({ pn, t }: any) => (
            <Form.Item key={pn} label={`${pn}（${t.name}）`}>
              <Input value={runParams[pn] || ''} onChange={(e) => setRunParams({ ...runParams, [pn]: e.target.value })} />
            </Form.Item>
          ))}
          <Form.Item><Button type="primary" onClick={doRun}>执行</Button></Form.Item>
        </Form>
        {runResult && <pre style={{ background: 'var(--surface-2)', padding: 12, borderRadius: 8, color: 'var(--text)', fontSize: 12, whiteSpace: 'pre-wrap' }}>{runResult}</pre>}
      </Drawer>
    </Card>
  )
}
export default Skills
```
> 注：params 渲染基于 `to_summary` 输出的 `tools[].params`（工具参数名列表）。若需类型/必填渲染，Task 2 的 params schema 需透传到 to_summary（可在 to_summary 输出完整 param 对象）。本次 MVP 用 Input 文本即可。

- [ ] **Step 3: Agents/index.tsx（只读卡片）**

```tsx
// src/pages/Agents/index.tsx
import React, { useEffect, useState } from 'react'
import { Card, Col, Row, Tag, message, Spin } from 'antd'
import { listAgents } from '../../api/client'

const Agents: React.FC = () => {
  const [agents, setAgents] = useState<any[]>([])
  const [loading, setLoading] = useState(true)
  const load = async () => {
    setLoading(true)
    try { const r = await listAgents(); setAgents(r?.data?.agents || []) } catch { message.error('加载助理失败') } finally { setLoading(false) }
  }
  useEffect(() => { load() }, [])
  if (loading) return <Spin style={{ display: 'block', margin: '40px auto' }} />
  return (
    <div>
      <Row gutter={[16, 16]}>
        {agents.map((a) => (
          <Col span={8} key={a.name}>
            <Card title={a.name} style={{ background: 'var(--surface)', borderColor: 'var(--border)', borderRadius: 10 }}>
              <div style={{ color: 'var(--text-muted)', fontSize: 13, marginBottom: 8 }}>{a.role}</div>
              <div style={{ color: 'var(--text)', fontSize: 13, marginBottom: 8 }}>{a.goal}</div>
              <div>{(a.skills || []).map((s: string) => <Tag key={s} style={{ margin: 2 }}>{s}</Tag>)}</div>
            </Card>
          </Col>
        ))}
      </Row>
    </div>
  )
}
export default Agents
```

- [ ] **Step 4: App.tsx 路由 + 菜单**

```tsx
import Skills from './pages/Skills'
import Agents from './pages/Agents'
<Route path="/skills" element={<Skills />} />
<Route path="/agents" element={<Agents />} />
```
menuGroups "智能运维" 区段加：
```tsx
{ key: '/skills', icon: <ToolOutlined />, label: '技能目录' },
{ key: '/agents', icon: <RobotOutlined />, label: 'AI 助理' },
```

- [ ] **Step 5: AgentSidePanel 改用 /agents**

```tsx
// components/AgentSidePanel.tsx 用 listAgents 替换 AGENTS 硬编码
import { listAgents } from '../api/client'
const [agents, setAgents] = useState<any[]>([])
useEffect(() => { listAgents().then((r) => setAgents(r?.data?.agents || [])).catch(() => {}) }, [])
// 渲染 agents 替代 AGENTS
```

- [ ] **Step 6: 构建验证**

Run: `cd aiops/observability-frontend && npm run build 2>&1 | tail -3`
Expected: 构建成功。

- [ ] **Step 7: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add observability-frontend/src/api/client.ts observability-frontend/src/pages/Skills observability-frontend/src/pages/Agents observability-frontend/src/App.tsx observability-frontend/src/components/AgentSidePanel.tsx
git commit -m "feat(frontend): skills & agents pages + routes + panel dynamic"
```

---

## Task 6: 本机部署验证 + 推送

**Files:**
- 无新代码；构建 + 部署 + 验证 + 推送。

**Interfaces:**
- Consumes: Task 1-5。

- [ ] **Step 1: 重建镜像**

Run: `cd /Users/mssc/Documents/Code/agent/aiops && docker build -t ai-orchestrator:latest ai-orchestrator && docker tag ai-orchestrator:latest docker.io/library/ai-orchestrator:latest && docker build -t observability-frontend:latest observability-frontend && docker tag observability-frontend:latest docker.io/library/observability-frontend:latest`
Expected: 构建成功（arm64）。

- [ ] **Step 2: 升级部署 + 滚动更新**

Run: `cd /Users/mssc/Documents/Code/agent/aiops && helm upgrade aiops deploy/helm/aiops --namespace observability --set deepflow.enabled=false --set secrets.jwtSecret="dev-jwt-secret-change-me" --set secrets.internalToken="dev-internal-token" --set secrets.ingestApiKey="dev-ingest-key" --set secrets.clickhousePassword="dev-ch-pass" --set secrets.redisPassword="dev-redis-pass" --set secrets.minioAccessKey="minioadmin" --set secrets.minioSecretKey="minioadmin123" --set secrets.mysqlRootPassword="dev-mysql-pass" && kubectl -n observability rollout restart deploy/ai-orchestrator deploy/frontend`
Expected: deployed + 滚动更新。

- [ ] **Step 3: 验证接口**

Run（登录 JWT）：
```bash
JWT=$(curl -s -X POST http://localhost:30253/api/v1/auth/login -H "Content-Type: application/json" -d '{"username":"admin","password":"admin123"}' | grep -o '"token":"[^"]*"' | cut -d'"' -f4)
curl -s "http://localhost:30253/api/v1/ai/skills" -H "Authorization: Bearer $JWT" | head -c 400
```
Expected: 返回 `{"skills":[{key,name,description,...}]}`（7 个 skill）。
```bash
curl -s "http://localhost:30253/api/v1/ai/agents" -H "Authorization: Bearer $JWT" | head -c 400
```
Expected: 返回 `{"agents":[{name,role,goal,...}]}`（4 个 expert）。

- [ ] **Step 4: 验证前端页**

Run: `curl -s -o /dev/null -w "%{http_code}\n" http://localhost:30253/skills`
Expected: 200。
Run: `curl -s -o /dev/null -w "%{http_code}\n" http://localhost:30253/agents`
Expected: 200。

- [ ] **Step 5: 提交验证通过（如有修复）**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add -A
git commit -m "fix: deployment verification fixes" || echo "无改动"
```

- [ ] **Step 6: 推送**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git push origin main
```
Expected: 推送成功。
