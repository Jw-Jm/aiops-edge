# Phase B 增强 · 工具 params 全量补齐 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 ToolRegistry 全部 16 个工具补齐 `params` schema（当前仅 query_metrics/query_traces 有），使 Skills 页所有工具能渲染执行表单并执行。识别 requires_approval 工具（execute_shell/vm_operate）的审批标记。

**Architecture:** 纯后端。在 `skills/*.py` 各 `register_*_skill()` 的 `ToolRegistry.register(...)` 调用处补 `params`（param_name -> {type, required, default, desc}）；`tools.py` 的 4 个工具已补 2 个（query_metrics/query_traces），补余下 query_topology/get_service_list；其余工具在对应 skills 文件补。

**Tech Stack:** Python。

## Global Constraints

- 后端：`ai-orchestrator/skills/*.py`（observability.py/vm_ops.py/rca_skill.py/rag_skill.py/alert_ops.py/infra.py/automation.py）+ `tools.py`。
- **现状**：16 工具仅 query_metrics/query_traces 有 params；execute_shell/vm_operate requires_approval=True。
- 工具签名（探查已确认）：
  - tools.py: query_metrics(service, tenant_id="default")✅; query_traces(service="", tenant_id="default")✅; query_topology(tenant_id="default"); get_service_list(tenant_id="default"); execute_shell(command, timeout=30)✅approval; k8sgpt_diagnose(namespace="observability"); deepflow_status(); get_infrastructure()
  - vm_ops.py: vm_list(); vm_status(vm_name); vm_operate(action, vm_name)✅approval
  - rca_skill.py: rca_analyze(service="")
  - rag_skill.py: case_search(query="", limit=5); case_feedback(case_id, outcome="success")
  - alert_ops.py: alert_rules(); alert_events(limit=10)
- 合规：独立实现，不复刻 ongrid 代码。
- 基线：`github.com/Jw-Jm/aiops-edge` main=`712b1fe`，每任务提交。

---

## Task 1: tools.py 补 query_topology/get_service_list params

**Files:**
- Modify: `aiops/ai-orchestrator/skills/observability.py`

**Interfaces:**
- Consumes: 现有 `ToolRegistry.register(name=...)`。
- Produces: `query_topology`/`get_service_list` 补 params。

- [ ] **Step 1: 补 params**

```python
# skills/observability.py
if not ToolRegistry.get("query_topology"):
    ToolRegistry.register(name="query_topology",
                          description="查询全局服务拓扑图",
                          category="trace",
                          params={})(query_topology)
if not ToolRegistry.get("get_service_list"):
    ToolRegistry.register(name="get_service_list",
                          description="获取所有服务列表及概览（含调用量/延迟/错误率）",
                          category="infra",
                          params={"limit": {"type": "int", "required": False, "default": 50, "desc": "返回服务数上限"}})(get_service_list)
```

- [ ] **Step 2: 语法校验**

Run: `cd aiops/ai-orchestrator && python3 -m py_compile skills/observability.py`
Expected: 无语法错误。

- [ ] **Step 3: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-orchestrator/skills/observability.py
git commit -m "feat(orchestrator): params for query_topology/get_service_list"
```

---

## Task 2: infra.py 补 k8sgpt_diagnose/deepflow_status/get_infrastructure params

**Files:**
- Modify: `aiops/ai-orchestrator/skills/infra.py`

**Interfaces:**
- Consumes: `ToolRegistry.register`。
- Produces: 3 个 infra 工具补 params。

- [ ] **Step 1: 补 params**

```python
# skills/infra.py
if not ToolRegistry.get("k8sgpt_diagnose"):
    ToolRegistry.register(name="k8sgpt_diagnose",
                          description="用 k8sgpt 诊断 K8s 问题",
                          category="infra",
                          params={"namespace": {"type": "string", "required": False, "default": "observability", "desc": "命名空间"}})(k8sgpt_diagnose)
if not ToolRegistry.get("deepflow_status"):
    ToolRegistry.register(name="deepflow_status",
                          description="查询 DeepFlow 采集状态",
                          category="infra",
                          params={})(deepflow_status)
if not ToolRegistry.get("get_infrastructure"):
    ToolRegistry.register(name="get_infrastructure",
                          description="获取基础设施(K8s 节点/Pod)概览",
                          category="infra",
                          params={})(get_infrastructure)
```

- [ ] **Step 2: 语法校验**

Run: `cd aiops/ai-orchestrator && python3 -m py_compile skills/infra.py`
Expected: 无语法错误。

- [ ] **Step 3: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-orchestrator/skills/infra.py
git commit -m "feat(orchestrator): params for infra tools"
```

---

## Task 3: vm_ops.py / rca_skill.py / rag_skill.py / alert_ops.py 补 params

**Files:**
- Modify: `aiops/ai-orchestrator/skills/vm_ops.py`
- Modify: `aiops/ai-orchestrator/skills/rca_skill.py`
- Modify: `aiops/ai-orchestrator/skills/rag_skill.py`
- Modify: `aiops/ai-orchestrator/skills/alert_ops.py`

**Interfaces:**
- Consumes: `ToolRegistry.register`。
- Produces: vm/automation 3 工具、rca 1、rag 2、alert 2 补 params（共 8 个）。

- [ ] **Step 1: vm_ops.py**

```python
if not ToolRegistry.get("vm_list"):
    ToolRegistry.register(name="vm_list", description="列出虚拟机", category="vm",
                          params={})(vm_list)
if not ToolRegistry.get("vm_status"):
    ToolRegistry.register(name="vm_status", description="查询虚拟机状态", category="vm",
                          params={"vm_name": {"type": "string", "required": True, "default": "", "desc": "虚拟机名"}})(vm_status)
if not ToolRegistry.get("vm_operate"):
    ToolRegistry.register(name="vm_operate", description="对虚拟机执行操作(start/stop/restart/migrate，需审批)", category="vm",
                          requires_approval=True,
                          params={"action": {"type": "string", "required": True, "default": "", "desc": "操作类型"},
                                  "vm_name": {"type": "string", "required": True, "default": "", "desc": "虚拟机名"}})(vm_operate)
```

- [ ] **Step 2: rca_skill.py**

```python
if not ToolRegistry.get("rca_analyze"):
    ToolRegistry.register(name="rca_analyze", description="对服务执行 RCA 根因分析", category="rca",
                          params={"service": {"type": "string", "required": False, "default": "", "desc": "服务名（空为自动检测）"}})(rca_analyze)
```

- [ ] **Step 3: rag_skill.py**

```python
if not ToolRegistry.get("case_search"):
    ToolRegistry.register(name="case_search", description="检索相似历史案例", category="rag",
                          params={"query": {"type": "string", "required": False, "default": "", "desc": "检索关键词"},
                                  "limit": {"type": "int", "required": False, "default": 5, "desc": "返回条数"}})(case_search)
if not ToolRegistry.get("case_feedback"):
    ToolRegistry.register(name="case_feedback", description="对案例反馈有用性", category="rag",
                          params={"case_id": {"type": "string", "required": True, "default": "", "desc": "案例 ID"},
                                  "outcome": {"type": "string", "required": False, "default": "success", "desc": "反馈结果"}})(case_feedback)
```

- [ ] **Step 4: alert_ops.py**

```python
if not ToolRegistry.get("alert_rules"):
    ToolRegistry.register(name="alert_rules", description="获取告警规则列表", category="alert",
                          params={})(alert_rules)
if not ToolRegistry.get("alert_events"):
    ToolRegistry.register(name="alert_events", description="获取告警事件", category="alert",
                          params={"limit": {"type": "int", "required": False, "default": 10, "desc": "返回条数"}})(alert_events)
```

- [ ] **Step 5: 语法校验**

Run: `cd aiops/ai-orchestrator && python3 -m py_compile skills/vm_ops.py skills/rca_skill.py skills/rag_skill.py skills/alert_ops.py`
Expected: 无语法错误。

- [ ] **Step 6: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-orchestrator/skills/vm_ops.py ai-orchestrator/skills/rca_skill.py ai-orchestrator/skills/rag_skill.py ai-orchestrator/skills/alert_ops.py
git commit -m "feat(orchestrator): params for vm/rca/rag/alert tools"
```

---

## Task 4: automation.py 补 execute_shell params（approval 工具）

**Files:**
- Modify: `aiops/ai-orchestrator/skills/automation.py`

**Interfaces:**
- Consumes: `ToolRegistry.register`。
- Produces: `execute_shell` 补 params（command/timeout，requires_approval 保持 True）。

- [ ] **Step 1: 补 params**

```python
if not ToolRegistry.get("execute_shell"):
    ToolRegistry.register(name="execute_shell", description="执行运维 shell 命令（写操作需审批）", category="automation",
                          requires_approval=True,
                          params={"command": {"type": "string", "required": True, "default": "", "desc": "要执行的命令"},
                                  "timeout": {"type": "int", "required": False, "default": 30, "desc": "超时秒数"}})(execute_shell)
```

- [ ] **Step 2: 语法校验**

Run: `cd aiops/ai-orchestrator && python3 -m py_compile skills/automation.py`
Expected: 无语法错误。

- [ ] **Step 3: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-orchestrator/skills/automation.py
git commit -m "feat(orchestrator): params for execute_shell (approval tool)"
```

---

## Task 5: 验证全部工具 params 生效 + 推送

**Files:**
- 无新代码；验证 + 推送。

**Interfaces:**
- Consumes: Task 1-4。

- [ ] **Step 1: 本地验证 params 齐全**

Run: `cd aiops/ai-orchestrator && python3 -c "
from skill_registry import SkillRegistry, ToolRegistry
from skills import init_skills, init_experts
init_skills(); init_experts()
missing = [n for n in ToolRegistry.list_all() if not ToolRegistry.get(n).params]
print('总工具:', len(ToolRegistry.list_all()))
print('无 params:', missing if missing else '无（全部有 params）')
approval = [n for n in ToolRegistry.list_all() if ToolRegistry.get(n).requires_approval]
print('需审批:', approval)
"`
Expected: `总工具: 16`，`无 params: 无（全部有 params）`，`需审批: ['execute_shell', 'vm_operate']`。

- [ ] **Step 2: 重建镜像 + 部署 + 验证 Skills 接口**

Run: `cd /Users/mssc/Documents/Code/agent/aiops && docker build -t ai-orchestrator:latest ai-orchestrator && docker tag ai-orchestrator:latest docker.io/library/ai-orchestrator:latest && helm upgrade aiops deploy/helm/aiops --namespace observability --set deepflow.enabled=false --set secrets.jwtSecret="dev-jwt-secret-change-me" --set secrets.internalToken="dev-internal-token" --set secrets.ingestApiKey="dev-ingest-key" --set secrets.clickhousePassword="dev-ch-pass" --set secrets.redisPassword="dev-redis-pass" --set secrets.minioAccessKey="minioadmin" --set secrets.minioSecretKey="minioadmin123" --set secrets.mysqlRootPassword="dev-mysql-pass" && kubectl -n observability rollout restart deploy/ai-orchestrator`
Expected: deployed + 滚动更新。

Run（登录 JWT）：
```bash
JWT=$(curl -s -X POST http://localhost:30253/api/v1/auth/login -H "Content-Type: application/json" -d '{"username":"admin","password":"admin123"}' | grep -o '"token":"[^"]*"' | cut -d'"' -f4)
curl -s "http://localhost:30253/api/v1/ai/skills" -H "Authorization: Bearer $JWT" | python3 -c "import sys,json; d=json.load(sys.stdin); [print(s['key'], [t['name'] for t in s['tools']]) for s in d.get('skills',[])]" 2>&1 | head -8
```
Expected: 各 skill 的 tools 显示（params 已补，前端可渲染执行表单）。

- [ ] **Step 3: 提交验证通过（如有修复）**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add -A
git commit -m "fix: deployment verification fixes" || echo "无改动"
```

- [ ] **Step 4: 推送**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git push origin main
```
Expected: 推送成功。
