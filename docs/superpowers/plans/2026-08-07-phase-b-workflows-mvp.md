# Phase B · Workflows 查看 + 运行 MVP（路径 B）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将自研硬编码固定 LangGraph DAG（full/chat 两套）暴露为可查看、可触发的 workflow：后端 `describe_graph` 序列化 + `/ai/flows`（只读列表/详情）+ `/ai/flows/{id}/run`（触发现有 brain 运行）；前端新增 `/workflows` 页（列表 + 只读图 + 运行 + 结果查看）。**不做可编辑引擎**（路径 C 单独立项）。

**Architecture:** 后端 orchestrator：新增 `describe_graph(mode)` 返回 `{nodes,edges}`（手写节点/边清单，与 `build_graph` 对齐）；main.py 暴露 `/ai/flows` 只读 + `/ai/flows/{key}/run`（调 `brain.execute_sync_full` 或 stream，写任务）。前端新增 Workflows 页（antd 卡片列表 + 只读流程图 + 运行触发）。

**Tech Stack:** Python FastAPI / React18 / AntD5。

## Global Constraints

- 后端：`ai-orchestrator/orchestrator.py`（`build_graph` L658、节点 L250-656、`BrainOrchestrator` L716）、`main.py`。
- **现状**：`build_graph(mode="full"|"chat")` 硬编码两套 DAG；无 flow 存储/接口。
- 本 plan **不引入 workflow 持久化**（workflow 定义即内置 DAG 模板，运行状态复用现有任务/会话）；`/ai/flows` 返回内置两套定义（只读）。
- 合规：页面/接口为自研功能，独立实现，不复刻 ongrid 代码（仅借鉴"查看+运行"交互思想）。
- 基线：`github.com/Jw-Jm/aiops-edge` main=`643a12c`，每任务提交。

---

## Task 1: 新增 describe_graph 序列化内置 DAG

**Files:**
- Modify: `aiops/ai-orchestrator/orchestrator.py`

**Interfaces:**
- Consumes: `build_graph` 的固定节点/边（full/chat 两套）。
- Produces: `describe_graph(mode) -> {key,name,nodes:[{id,label,desc}],edges:[{source,target}]}`；与 `build_graph` 节点清单一致。

- [ ] **Step 1: 实现 describe_graph**

```python
# orchestrator.py，加到 BrainOrchestrator 或模块级
GRAPH_DEFS = {
    "full": {
        "key": "workflow.full_diagnosis",
        "name": "完整诊断流程",
        "description": "采集→清洗→根因→RAG→AI分析→方案→风险→审批→执行→验证→报告→记忆→汇总",
        "nodes": [
            {"id": "collect", "label": "数据采集", "desc": "采集服务指标/调用链/错误"},
            {"id": "clean", "label": "数据清洗", "desc": "清洗归一化采集数据"},
            {"id": "rca", "label": "RCA 根因分析", "desc": "确定性+假设证伪定位根因"},
            {"id": "rag", "label": "RAG 案例匹配", "desc": "检索相似历史案例"},
            {"id": "crewai", "label": "CrewAI 专家分析", "desc": "多专家协同分析"},
            {"id": "plan", "label": "生成方案", "desc": "生成可执行运维方案"},
            {"id": "risk", "label": "风险评估", "desc": "评估方案风险"},
            {"id": "wait_approval", "label": "人工审批", "desc": "等待审批中断"},
            {"id": "execute", "label": "执行方案", "desc": "执行审批通过的脚本"},
            {"id": "verify", "label": "执行验证", "desc": "验证执行效果"},
            {"id": "report", "label": "生成报告", "desc": "生成诊断报告"},
            {"id": "memorize", "label": "记忆学习", "desc": "沉淀案例到 RAG"},
            {"id": "summarize", "label": "汇总输出", "desc": "生成最终总结"},
        ],
        "edges": [
            ("collect", "clean"), ("clean", "rca"), ("rca", "rag"), ("rag", "crewai"),
            ("crewai", "plan"), ("plan", "risk"), ("risk", "wait_approval"),
            ("wait_approval", "execute"), ("execute", "verify"), ("verify", "report"),
            ("report", "memorize"), ("memorize", "summarize"),
        ],
    },
    "chat": {
        "key": "workflow.chat_diagnosis",
        "name": "交互诊断流程",
        "description": "采集→清洗→根因→RAG→AI分析→汇总（对话用）",
        "nodes": [
            {"id": "collect", "label": "数据采集", "desc": "采集服务指标/调用链/错误"},
            {"id": "clean", "label": "数据清洗", "desc": "清洗归一化采集数据"},
            {"id": "rca", "label": "RCA 根因分析", "desc": "定位根因"},
            {"id": "rag", "label": "RAG 案例匹配", "desc": "检索相似案例"},
            {"id": "crewai", "label": "CrewAI 专家分析", "desc": "专家分析"},
            {"id": "summarize", "label": "汇总输出", "desc": "生成最终总结"},
        ],
        "edges": [
            ("collect", "clean"), ("clean", "rca"), ("rca", "rag"), ("rag", "crewai"), ("crewai", "summarize"),
        ],
    },
}

def describe_graph(mode: str = "full") -> dict:
    """返回内置 workflow 定义（nodes/edges 用 dict 结构），供 /ai/flows 展示。"""
    g = GRAPH_DEFS.get(mode, GRAPH_DEFS["full"])
    return {
        "key": g["key"],
        "name": g["name"],
        "description": g["description"],
        "mode": mode,
        "nodes": g["nodes"],
        "edges": [{"source": s, "target": t} for s, t in g["edges"]],
    }
```

- [ ] **Step 2: 语法校验**

Run: `cd aiops/ai-orchestrator && python3 -m py_compile orchestrator.py`
Expected: 无语法错误。

- [ ] **Step 3: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-orchestrator/orchestrator.py
git commit -m "feat(orchestrator): describe_graph for built-in workflows"
```

---

## Task 2: 暴露 /ai/flows 只读 + /ai/flows/{key}/run

**Files:**
- Modify: `aiops/ai-orchestrator/main.py`

**Interfaces:**
- Consumes: `describe_graph`（Task 1）、现有 `_get_brain()`/`execute_sync`。
- Produces:
  - `GET /api/v1/ai/flows` → `{flows:[describe_graph(full), describe_graph(chat)]}`
  - `GET /api/v1/ai/flows/{key}` → 单个 flow 定义
  - `POST /api/v1/ai/flows/{key}/run`（body `{service,message}`）→ 触发 `_get_brain().execute_sync(...)` 运行，返回 `{run_id, result}`

- [ ] **Step 1: 加接口**

```python
# main.py，/ai/agents 后加
from orchestrator import describe_graph

@app.get("/api/v1/ai/flows")
async def ai_flows():
    return {"flows": [describe_graph("full"), describe_graph("chat")]}

@app.get("/api/v1/ai/flows/{key}")
async def ai_flow_detail(key: str):
    mode = "chat" if key.endswith("chat_diagnosis") else "full"
    return describe_graph(mode)

@app.post("/api/v1/ai/flows/{key}/run")
async def ai_flow_run(key: str, body: dict = None):
    mode = "chat" if key.endswith("chat_diagnosis") else "full"
    service = (body or {}).get("service", "")
    message = (body or {}).get("message", "对服务进行完整诊断")
    brain = _get_brain()
    result = await asyncio.to_thread(brain.execute_sync, mode, service, message, "default")
    return {"run_id": f"run_{int(time.time()*1000)}", "result": result}
```
> 注：`brain.execute_sync` 签名需确认（mode/intent/service/message/tenant）。若签名不同按实际调整；`asyncio.to_thread` 需 Python3.9+（镜像 3.12 支持）。

- [ ] **Step 2: 语法校验**

Run: `cd aiops/ai-orchestrator && python3 -m py_compile main.py`
Expected: 无语法错误。

- [ ] **Step 3: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-orchestrator/main.py
git commit -m "feat(orchestrator): /ai/flows read-only + run endpoints"
```

---

## Task 3: 前端 Workflows 页（列表 + 只读图 + 运行）

**Files:**
- Modify: `aiops/observability-frontend/src/api/client.ts`
- Create: `aiops/observability-frontend/src/pages/Workflows/index.tsx`
- Modify: `aiops/observability-frontend/src/App.tsx`（路由 + 菜单）

**Interfaces:**
- Consumes: Task 2 接口。
- Produces: `/workflows` 页——两张 workflow 卡片（名称/描述/节点数），点击展开只读流程图（简单 SVG 渲染节点链）+ "运行"按钮（填 service/message → 调 run → 显示结果）。

- [ ] **Step 1: client.ts 封装**

```ts
export const listFlows = () => api.get('/ai/flows')
export const getFlow = (key: string) => api.get(`/ai/flows/${encodeURIComponent(key)}`)
export const runFlow = (key: string, params: Record<string, unknown>) => api.post(`/ai/flows/${encodeURIComponent(key)}/run`, params)
```

- [ ] **Step 2: Workflows/index.tsx**

```tsx
// src/pages/Workflows/index.tsx
import React, { useEffect, useState } from 'react'
import { Card, Row, Col, Button, Tag, Drawer, Form, Input, message, Spin } from 'antd'
import { listFlows, runFlow } from '../../api/client'

interface Flow { key: string; name: string; description: string; nodes: any[]; edges: any[] }

const Workflows: React.FC = () => {
  const [flows, setFlows] = useState<Flow[]>([])
  const [loading, setLoading] = useState(true)
  const [detail, setDetail] = useState<Flow | null>(null)
  const [runOpen, setRunOpen] = useState(false)
  const [runParams, setRunParams] = useState({ service: '', message: '' })
  const [runResult, setRunResult] = useState('')
  const [running, setRunning] = useState(false)

  const load = async () => {
    setLoading(true)
    try { const r = await listFlows(); setFlows(r?.data?.flows || []) } catch { message.error('加载工作流失败') } finally { setLoading(false) }
  }
  useEffect(() => { load() }, [])

  const doRun = async (key: string) => {
    setRunning(true); setRunResult('运行中...')
    try {
      const r = await runFlow(key, runParams)
      const res = r?.data?.result
      setRunResult(typeof res === 'string' ? res : JSON.stringify(res, null, 2))
    } catch { setRunResult('运行失败') } finally { setRunning(false) }
  }

  return (
    <div>
      <Row gutter={[16, 16]}>
        {flows.map((f) => (
          <Col span={12} key={f.key}>
            <Card title={f.name} style={{ background: 'var(--surface)', borderColor: 'var(--border)', borderRadius: 10 }}
              extra={<Button size="small" onClick={() => { setDetail(f); setRunParams({ service: '', message: '' }); setRunResult('') }}>查看/运行</Button>}>
              <div style={{ color: 'var(--text-muted)', fontSize: 13, marginBottom: 8 }}>{f.description}</div>
              <div>{f.nodes?.length} 个节点 · {f.edges?.length} 条边</div>
            </Card>
          </Col>
        ))}
      </Row>
      <Drawer title={detail?.name} open={!!detail} onClose={() => setDetail(null)} width={560}>
        {/* 只读流程图：SVG 垂直节点链 */}
        <div style={{ background: 'var(--surface-2)', padding: 16, borderRadius: 8, overflowX: 'auto' }}>
          <svg width="320" height={(detail?.nodes?.length || 1) * 56} style={{ display: 'block' }}>
            {(detail?.nodes || []).map((n: any, i: number) => (
              <g key={n.id}>
                <rect x="60" y={i * 56} width="200" height="36" rx="8" fill="#27272a" stroke="#3f3f46" />
                <text x="160" y={i * 56 + 23} textAnchor="middle" fill="#f4f4f5" fontSize="12">{n.label}</text>
                {i < (detail?.nodes?.length || 1) - 1 && <line x1="160" y1={i * 56 + 36} x2="160" y2={(i + 1) * 56} stroke="#3f3f46" />}
              </g>
            ))}
          </svg>
        </div>
        <div style={{ marginTop: 16 }}>
          <Button type="primary" onClick={() => setRunOpen(true)}>运行</Button>
        </div>
      </Drawer>
      <Drawer title={`运行 ${detail?.name}`} open={runOpen} onClose={() => setRunOpen(false)} width={520}>
        <Form layout="vertical">
          <Form.Item label="服务"><Input value={runParams.service} onChange={(e) => setRunParams({ ...runParams, service: e.target.value })} placeholder="如 deepflow-server" /></Form.Item>
          <Form.Item label="诊断诉求"><Input.TextArea value={runParams.message} onChange={(e) => setRunParams({ ...runParams, message: e.target.value })} /></Form.Item>
          <Form.Item><Button type="primary" loading={running} onClick={() => detail && doRun(detail.key)}>执行诊断</Button></Form.Item>
        </Form>
        {runResult && <pre style={{ background: 'var(--surface-2)', padding: 12, borderRadius: 8, color: 'var(--text)', fontSize: 12, whiteSpace: 'pre-wrap', maxHeight: 300, overflow: 'auto' }}>{runResult}</pre>}
      </Drawer>
    </div>
  )
}
export default Workflows
```

- [ ] **Step 3: App.tsx 路由 + 菜单**

```tsx
import Workflows from './pages/Workflows'
<Route path="/workflows" element={<Workflows />} />
```
menuGroups "智能运维" 区段加：
```tsx
{ key: '/workflows', icon: <DeploymentUnitOutlined />, label: '工作流' },
```

- [ ] **Step 4: 构建验证**

Run: `cd aiops/observability-frontend && npm run build 2>&1 | tail -3`
Expected: 构建成功。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add observability-frontend/src/api/client.ts observability-frontend/src/pages/Workflows observability-frontend/src/App.tsx
git commit -m "feat(frontend): workflows page (view built-in DAG + run)"
```

---

## Task 4: 本机部署验证 + 推送

**Files:**
- 无新代码；构建 + 部署 + 验证 + 推送。

**Interfaces:**
- Consumes: Task 1-3。

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
curl -s "http://localhost:30253/api/v1/ai/flows" -H "Authorization: Bearer $JWT" | python3 -c "import sys,json; d=json.load(sys.stdin); print('flows:', [f['key'] for f in d.get('flows',[])])"
```
Expected: `flows: ['workflow.full_diagnosis', 'workflow.chat_diagnosis']`。

- [ ] **Step 4: 验证前端页**

Run: `curl -s -o /dev/null -w "%{http_code}\n" http://localhost:30253/workflows`
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
