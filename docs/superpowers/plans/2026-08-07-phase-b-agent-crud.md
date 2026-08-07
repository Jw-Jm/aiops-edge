# Phase B 增强 · Agent CRUD（ExpertRegistry 持久化 + 前端增删改）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 ExpertRegistry 增加 delete/update + JSON 文件持久化（用户自定义专家），暴露 `/ai/agents` CRUD 接口，前端 Agents 页支持新建/编辑/删除。内置 4 专家保持不变。

**Architecture:** 后端 skill_registry：ExpertRegistry 加 `delete(name)`、`update(name,...)`、`save_custom_store()`/`load_custom_store()`（用户自定义专家存 `/tmp/expert_store.json`，启动时加载）；main.py 加 `POST /ai/agents`、`PUT /ai/agents/{name}`、`DELETE /ai/agents/{name}`。前端 Agents 页加新建/编辑/删除。

**Tech Stack:** Python FastAPI / React18 / AntD5。

## Global Constraints

- 后端：`ai-orchestrator/skill_registry.py`（ExpertRegistry L186-228）、`main.py`。
- **现状**：ExpertRegistry 纯内存 `_experts`；`register` 创建/覆盖；无 delete；`_init_defaults()` 幂等短路（SkillRegistry 非空即 return）；内置 4 专家（inspection/diagnosis/ops/query）由 `init_experts()` 注册。
- **设计**：不破坏 `_init_defaults` 幂等。内置专家走 `init_experts()`；**用户自定义专家**存 JSON（`EXPERTS_STORE=/tmp/expert_store.json`），启动时 `load_custom_store()` 合并。`delete` 只删用户自定义，不删内置。
- 合规：独立实现，不复刻 ongrid 代码。
- 基线：`github.com/Jw-Jm/aiops-edge` main=`71bb36b`，每任务提交。

---

## Task 1: ExpertRegistry 增 delete/update + 自定义专家持久化

**Files:**
- Modify: `aiops/ai-orchestrator/skill_registry.py`

**Interfaces:**
- Consumes: `ExpertRegistry` classmethod 模式、`ExpertDef`。
- Produces: `delete(name)`、`update(name,...)`、`save_custom_store()`、`load_custom_store()`、`is_builtin(name)`。

- [ ] **Step 1: 实现方法**

在 ExpertRegistry 类内（`skills_of` 后）加：
```python
    BUILTIN_EXPERTS = {"inspection", "diagnosis", "ops", "query"}

    @classmethod
    def update(cls, name: str, **fields):
        """更新专家字段（role/goal/backstory/intent_keywords/skills/tools/system_prompt_template）。"""
        expert = cls._experts.get(name)
        if not expert:
            return False
        for k, v in fields.items():
            if hasattr(expert, k):
                setattr(expert, k, v)
        cls.save_custom_store()
        return True

    @classmethod
    def delete(cls, name: str) -> bool:
        """删除用户自定义专家；内置专家不可删。"""
        if name in cls.BUILTIN_EXPERTS:
            return False
        if name in cls._experts:
            del cls._experts[name]
            cls.save_custom_store()
            return True
        return False

    @classmethod
    def _store_path(cls) -> str:
        return os.environ.get("EXPERTS_STORE", "/tmp/expert_store.json")

    @classmethod
    def save_custom_store(cls):
        """将用户自定义专家（非内置）持久化到 JSON。"""
        custom = {k: {
            "name": v.name, "role": v.role, "goal": v.goal, "backstory": v.backstory,
            "intent_keywords": v.intent_keywords, "skills": v.skills, "tools": v.tools,
            "system_prompt_template": v.system_prompt_template,
        } for k, v in cls._experts.items() if k not in cls.BUILTIN_EXPERTS}
        try:
            with open(cls._store_path(), "w") as f:
                json.dump(custom, f, ensure_ascii=False, indent=2)
        except Exception as e:
            print(f"[skill_registry] 保存自定义专家失败: {e}")

    @classmethod
    def load_custom_store(cls):
        """启动时加载用户自定义专家（与内置专家合并）。"""
        try:
            with open(cls._store_path()) as f:
                data = json.load(f)
        except (FileNotFoundError, json.JSONDecodeError):
            return
        for k, v in data.items():
            if k in cls.BUILTIN_EXPERTS:
                continue
            cls._experts[k] = ExpertDef(**v)
```
> 需确认 `os`/`json` 已 import（skill_registry.py 顶部应有）。`ExpertDef(**v)` 字段名需与 ExpertDef dataclass 一致（name/role/goal/backstory/intent_keywords/skills/tools/system_prompt_template）。

- [ ] **Step 2: _init_defaults 后加载自定义专家**

在 `_init_defaults()` 末尾（init_skills/init_experts 后）加：
```python
    ExpertRegistry.load_custom_store()
```
> 注意：`_init_defaults` 幂等短路（SkillRegistry 非空即 return）在首次调用后不再执行，所以 load_custom_store 在首次初始化时执行一次即可（内置+自定义合并）。

- [ ] **Step 3: 语法校验**

Run: `cd aiops/ai-orchestrator && python3 -m py_compile skill_registry.py`
Expected: 无语法错误。

- [ ] **Step 4: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-orchestrator/skill_registry.py
git commit -m "feat(orchestrator): ExpertRegistry delete/update + custom expert persistence"
```

---

## Task 2: /ai/agents CRUD 接口

**Files:**
- Modify: `aiops/ai-orchestrator/main.py`

**Interfaces:**
- Consumes: `ExpertRegistry.delete/update/register`（Task 1）。
- Produces: `POST /api/v1/ai/agents`（创建自定义）、`PUT /api/v1/ai/agents/{name}`（更新）、`DELETE /api/v1/ai/agents/{name}`（删除自定义）。

- [ ] **Step 1: 加 CRUD 接口**

```python
# main.py，/ai/agents/{name} (GET) 后加
from pydantic import BaseModel

class AgentPayload(BaseModel):
    name: str = ""
    role: str = ""
    goal: str = ""
    backstory: str = ""
    intent_keywords: list = []
    skills: list = []
    tools: list = []
    system_prompt_template: str = ""

@app.post("/api/v1/ai/agents")
async def ai_agent_create(body: AgentPayload):
    if not body.name:
        raise HTTPException(400, "name required")
    try:
        if not ExpertRegistry.list_all():
            init_experts()
    except Exception:
        pass
    ExpertRegistry.register(
        name=body.name, role=body.role, goal=body.goal, backstory=body.backstory,
        intent_keywords=body.intent_keywords, skills=body.skills, tools=body.tools,
        system_prompt_template=body.system_prompt_template,
    )
    ExpertRegistry.save_custom_store()
    return ExpertRegistry.get(body.name).__dict__ if ExpertRegistry.get(body.name) else {}

@app.put("/api/v1/ai/agents/{name}")
async def ai_agent_update(name: str, body: AgentPayload):
    ok = ExpertRegistry.update(name, **body.dict(exclude={"name"}))
    if not ok:
        raise HTTPException(404, "agent not found")
    return ExpertRegistry.get(name).__dict__

@app.delete("/api/v1/ai/agents/{name}")
async def ai_agent_delete(name: str):
    if not ExpertRegistry.delete(name):
        raise HTTPException(400, "cannot delete built-in or not found")
    return {"deleted": name}
```
> 注：`ExpertDef` 是否为 dataclass（`__dict__` 可用）需确认。若为普通 class，手动构造 dict 返回。

- [ ] **Step 2: 语法校验**

Run: `cd aiops/ai-orchestrator && python3 -m py_compile main.py`
Expected: 无语法错误。

- [ ] **Step 3: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-orchestrator/main.py
git commit -m "feat(orchestrator): /ai/agents CRUD endpoints"
```

---

## Task 3: 前端 Agents 页增删改

**Files:**
- Modify: `aiops/observability-frontend/src/api/client.ts`
- Modify: `aiops/observability-frontend/src/pages/Agents/index.tsx`

**Interfaces:**
- Consumes: Task 2 接口。
- Produces: Agents 页加"新建助理"按钮 + 编辑/删除；client 加 `createAgent`/`updateAgent`/`deleteAgent`。

- [ ] **Step 1: client.ts 封装**

```ts
export const createAgent = (data: Record<string, unknown>) => api.post('/ai/agents', data)
export const updateAgent = (name: string, data: Record<string, unknown>) => api.put(`/ai/agents/${encodeURIComponent(name)}`, data)
export const deleteAgent = (name: string) => api.delete(`/ai/agents/${encodeURIComponent(name)}`)
```

- [ ] **Step 2: Agents 页加操作**

在 Agents 卡片加编辑/删除按钮 + 新建按钮 + 抽屉表单：
```tsx
// 组件加 state
const [modalOpen, setModalOpen] = useState(false)
const [editing, setEditing] = useState<any>(null)
const [form, setForm] = useState({ name: '', role: '', goal: '', backstory: '', intent_keywords: '', skills: '' })

const openCreate = () => { setEditing(null); setForm({ name: '', role: '', goal: '', backstory: '', intent_keywords: '', skills: '' }); setModalOpen(true) }
const openEdit = (a: any) => { setEditing(a); setForm({ name: a.name, role: a.role || '', goal: a.goal || '', backstory: a.backstory || '', intent_keywords: (a.intent_keywords || []).join(','), skills: (a.skills || []).join(',') }); setModalOpen(true) }
const onSave = async () => {
  const payload = { ...form, intent_keywords: form.intent_keywords.split(',').filter(Boolean), skills: form.skills.split(',').filter(Boolean) }
  try {
    if (editing) await updateAgent(editing.name, payload)
    else await createAgent(payload)
    message.success('已保存'); setModalOpen(false); load()
  } catch { message.error('保存失败') }
}
const onDelete = async (a: any) => {
  try { await deleteAgent(a.name); message.success('已删除'); load() } catch { message.error('删除失败') }
}
```
卡片 extra 加 `编辑`/`删除`（内置专家删除会失败，可提示）。加 `新建助理`按钮 + `Modal`（表单 name/role/goal/backstory/intent_keywords/skills）。

- [ ] **Step 3: 构建验证**

Run: `cd aiops/observability-frontend && npm run build 2>&1 | tail -3`
Expected: 构建成功。

- [ ] **Step 4: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add observability-frontend/src/api/client.ts observability-frontend/src/pages/Agents/index.tsx
git commit -m "feat(frontend): agents page CRUD (create/edit/delete)"
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

- [ ] **Step 3: 验证 CRUD**

Run（登录 JWT）：
```bash
JWT=$(curl -s -X POST http://localhost:30253/api/v1/auth/login -H "Content-Type: application/json" -d '{"username":"admin","password":"admin123"}' | grep -o '"token":"[^"]*"' | cut -d'"' -f4)
# 创建
curl -s -X POST "http://localhost:30253/api/v1/ai/agents" -H "Content-Type: application/json" -H "Authorization: Bearer $JWT" -d '{"name":"custom-agent","role":"自定义助理","goal":"测试自定义 agent"}' | head -c 200
# 更新
curl -s -X PUT "http://localhost:30253/api/v1/ai/agents/custom-agent" -H "Content-Type: application/json" -H "Authorization: Bearer $JWT" -d '{"role":"更新后","goal":"updated"}' | head -c 200
# 删除
curl -s -X DELETE "http://localhost:30253/api/v1/ai/agents/custom-agent" -H "Authorization: Bearer $JWT" | head -c 100
# 内置不可删
curl -s -X DELETE "http://localhost:30253/api/v1/ai/agents/ops" -H "Authorization: Bearer $JWT" | head -c 100
```
Expected: 创建返回对象、更新成功、删除返回 `{"deleted":...}`、内置删除返回 400。

- [ ] **Step 4: 验证前端页**

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
